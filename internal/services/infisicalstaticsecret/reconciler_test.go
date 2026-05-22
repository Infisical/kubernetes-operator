package infisicalstaticsecret_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/api"
	"github.com/Infisical/infisical/k8-operator/internal/constants"
	"github.com/Infisical/infisical/k8-operator/internal/crypto"
	svc "github.com/Infisical/infisical/k8-operator/internal/services/infisicalstaticsecret"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func secret(key, env, path string) api.Secret {
	return api.Secret{
		SecretKey:   key,
		Environment: env,
		SecretPath:  path,
	}
}

var _ = Describe("MergeSecretSources", func() {

	type TestCase struct {
		secrets         []api.Secret
		importedSecrets []api.Secret
		expected        []api.Secret
	}

	reconciler := &svc.InfisicalStaticSecretReconciler{}

	DescribeTable("merging secrets from multiple sources",
		func(tc TestCase) {
			got := reconciler.MergeSecretSources(tc.secrets, tc.importedSecrets)
			Expect(got).To(HaveLen(len(tc.expected)))
			for i, exp := range tc.expected {
				Expect(got[i].SecretKey).To(Equal(exp.SecretKey))
				Expect(got[i].Environment).To(Equal(exp.Environment))
				Expect(got[i].SecretPath).To(Equal(exp.SecretPath))
			}
		},
		Entry("empty input returns empty", TestCase{
			secrets:         []api.Secret{},
			importedSecrets: []api.Secret{},
			expected:        []api.Secret{},
		}),
		Entry("no duplicates keeps all", TestCase{
			secrets:         []api.Secret{secret("A", "dev", "/")},
			importedSecrets: []api.Secret{secret("B", "dev", "/")},
			expected:        []api.Secret{secret("A", "dev", "/"), secret("B", "dev", "/")},
		}),
		Entry("base secret takes precedence over import with same key", TestCase{
			secrets:         []api.Secret{secret("A", "dev", "/")},
			importedSecrets: []api.Secret{secret("A", "staging", "/app")},
			expected:        []api.Secret{secret("A", "dev", "/")},
		}),
		Entry("multiple duplicates across sources", TestCase{
			secrets: []api.Secret{
				secret("A", "dev", "/"),
				secret("B", "dev", "/"),
			},
			importedSecrets: []api.Secret{
				secret("A", "staging", "/app"),
				secret("C", "staging", "/app"),
				secret("B", "prod", "/"),
			},
			expected: []api.Secret{
				secret("A", "dev", "/"),
				secret("B", "dev", "/"),
				secret("C", "staging", "/app"),
			},
		}),
		Entry("single secret", TestCase{
			secrets:         []api.Secret{secret("ONLY", "dev", "/")},
			importedSecrets: []api.Secret{},
			expected:        []api.Secret{secret("ONLY", "dev", "/")},
		}),
		Entry("all duplicates keeps only first", TestCase{
			secrets:         []api.Secret{secret("A", "dev", "/")},
			importedSecrets: []api.Secret{secret("A", "staging", "/"), secret("A", "prod", "/")},
			expected:        []api.Secret{secret("A", "dev", "/")},
		}),
		Entry("nil input returns empty", TestCase{
			secrets:         nil,
			importedSecrets: nil,
			expected:        []api.Secret{},
		}),
		Entry("only imports", TestCase{
			secrets:         []api.Secret{},
			importedSecrets: []api.Secret{secret("A", "dev", "/"), secret("B", "dev", "/")},
			expected:        []api.Secret{secret("A", "dev", "/"), secret("B", "dev", "/")},
		}),
	)
})

var _ = Describe("RenderTargetOutput", func() {

	reconciler := &svc.InfisicalStaticSecretReconciler{}

	secrets := []api.Secret{
		{SecretKey: "DB_HOST", SecretValue: "localhost", SecretPath: "/db"},
		{SecretKey: "DB_PORT", SecretValue: "5432", SecretPath: "/db"},
		{SecretKey: "DB_NAME", SecretValue: "mydb", SecretPath: "/db"},
	}

	Context("without a template", func() {
		It("maps each secret key to its value", func() {
			target := v1beta1.SecretTarget{
				Name:      "test-secret",
				Namespace: "default",
				Kind:      v1beta1.SecretTargetKindSecret,
			}

			data, err := reconciler.RenderTargetOutput(secrets, target)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(HaveLen(3))
			Expect(data).To(HaveKeyWithValue("DB_HOST", []byte("localhost")))
			Expect(data).To(HaveKeyWithValue("DB_PORT", []byte("5432")))
			Expect(data).To(HaveKeyWithValue("DB_NAME", []byte("mydb")))
		})

		It("returns empty map for empty secrets", func() {
			target := v1beta1.SecretTarget{
				Name:      "test-secret",
				Namespace: "default",
				Kind:      v1beta1.SecretTargetKindSecret,
			}

			data, err := reconciler.RenderTargetOutput([]api.Secret{}, target)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(BeEmpty())
		})
	})

	Context("with a template", func() {
		It("renders template data using .Value accessor", func() {
			target := v1beta1.SecretTarget{
				Name:      "db-dsn",
				Namespace: "default",
				Kind:      v1beta1.SecretTargetKindSecret,
				Template: &v1beta1.SecretTemplate{
					Data: map[string]string{
						"dsn": "postgresql://user:pass@{{ .DB_HOST.Value }}:{{ .DB_PORT.Value }}/{{ .DB_NAME.Value }}",
					},
				},
			}

			data, err := reconciler.RenderTargetOutput(secrets, target)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(HaveLen(4))
			Expect(data).To(HaveKeyWithValue("dsn", []byte("postgresql://user:pass@localhost:5432/mydb")))
			Expect(data).To(HaveKeyWithValue("DB_HOST", []byte("localhost")))
			Expect(data).To(HaveKeyWithValue("DB_PORT", []byte("5432")))
			Expect(data).To(HaveKeyWithValue("DB_NAME", []byte("mydb")))
		})

		It("renders template data using .SecretPath accessor", func() {
			target := v1beta1.SecretTarget{
				Name:      "test",
				Namespace: "default",
				Kind:      v1beta1.SecretTargetKindSecret,
				Template: &v1beta1.SecretTemplate{
					Data: map[string]string{
						"info": "{{ .DB_HOST.Value }} from {{ .DB_HOST.SecretPath }}",
					},
				},
			}

			data, err := reconciler.RenderTargetOutput(secrets, target)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(HaveKeyWithValue("info", []byte("localhost from /db")))
		})

		It("renders multiple template keys", func() {
			target := v1beta1.SecretTarget{
				Name:      "test",
				Namespace: "default",
				Kind:      v1beta1.SecretTargetKindConfigMap,
				Template: &v1beta1.SecretTemplate{
					Data: map[string]string{
						"host": "{{ .DB_HOST.Value }}",
						"port": "{{ .DB_PORT.Value }}",
					},
				},
			}

			data, err := reconciler.RenderTargetOutput(secrets, target)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(HaveLen(5))
			Expect(data).To(HaveKeyWithValue("host", []byte("localhost")))
			Expect(data).To(HaveKeyWithValue("port", []byte("5432")))
			Expect(data).To(HaveKeyWithValue("DB_HOST", []byte("localhost")))
			Expect(data).To(HaveKeyWithValue("DB_PORT", []byte("5432")))
			Expect(data).To(HaveKeyWithValue("DB_NAME", []byte("mydb")))
		})

		It("returns error for invalid template syntax", func() {
			target := v1beta1.SecretTarget{
				Name:      "bad",
				Namespace: "default",
				Kind:      v1beta1.SecretTargetKindSecret,
				Template: &v1beta1.SecretTemplate{
					Data: map[string]string{
						"bad": "{{ .DB_HOST",
					},
				},
			}

			_, err := reconciler.RenderTargetOutput(secrets, target)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to parse template"))
		})
	})
})

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	_ = v1beta1.AddToScheme(s)
	return s
}

func newReconciler(objs ...client.Object) *svc.InfisicalStaticSecretReconciler {
	s := newScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
	return svc.NewReconcilerForTest(fakeClient, s)
}

func newStaticSecret() *v1beta1.InfisicalStaticSecret {
	return &v1beta1.InfisicalStaticSecret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-static-secret",
			Namespace: "default",
			UID:       "test-uid",
		},
	}
}

var _ = Describe("SyncKubeSecret", func() {
	ctx := context.Background()

	data := map[string][]byte{
		"DB_HOST": []byte("localhost"),
		"DB_PORT": []byte("5432"),
	}

	target := v1beta1.SecretTarget{
		Name:           "my-secret",
		Namespace:      "default",
		Kind:           v1beta1.SecretTargetKindSecret,
		SecretType:     corev1.SecretTypeOpaque,
		CreationPolicy: v1beta1.CreationPolicyOrphan,
	}

	It("creates a new secret when it does not exist", func() {
		reconciler := newReconciler()
		owner := newStaticSecret()

		changed, err := reconciler.SyncKubeSecret(ctx, owner, data, target)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())

		created := &corev1.Secret{}
		err = reconciler.Client.Get(ctx, types.NamespacedName{Name: "my-secret", Namespace: "default"}, created)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Data).To(HaveKeyWithValue("DB_HOST", []byte("localhost")))
		Expect(created.Data).To(HaveKeyWithValue("DB_PORT", []byte("5432")))
		Expect(created.Type).To(Equal(corev1.SecretTypeOpaque))

		expectedEtag := crypto.ComputeEtag([]byte(fmt.Sprintf("%v", data)))
		Expect(created.Annotations[constants.SECRET_VERSION_ANNOTATION]).To(Equal(expectedEtag))
	})

	It("sets owner reference when creation policy is Owner", func() {
		reconciler := newReconciler()
		owner := newStaticSecret()

		ownerTarget := target
		ownerTarget.CreationPolicy = v1beta1.CreationPolicyOwner

		changed, err := reconciler.SyncKubeSecret(ctx, owner, data, ownerTarget)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())

		created := &corev1.Secret{}
		err = reconciler.Client.Get(ctx, types.NamespacedName{Name: "my-secret", Namespace: "default"}, created)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.OwnerReferences).To(HaveLen(1))
		Expect(created.OwnerReferences[0].Name).To(Equal(owner.GetName()))
	})

	It("does not set owner reference when creation policy is Orphan", func() {
		reconciler := newReconciler()
		owner := newStaticSecret()

		changed, err := reconciler.SyncKubeSecret(ctx, owner, data, target)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())

		created := &corev1.Secret{}
		err = reconciler.Client.Get(ctx, types.NamespacedName{Name: "my-secret", Namespace: "default"}, created)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.OwnerReferences).To(BeEmpty())
	})

	It("updates an existing secret when data changes", func() {
		existing := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-secret",
				Namespace: "default",
				Annotations: map[string]string{
					constants.SECRET_VERSION_ANNOTATION: "old-etag",
				},
			},
			Data: map[string][]byte{"OLD_KEY": []byte("old-value")},
		}
		reconciler := newReconciler(existing)
		owner := newStaticSecret()

		changed, err := reconciler.SyncKubeSecret(ctx, owner, data, target)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())

		updated := &corev1.Secret{}
		err = reconciler.Client.Get(ctx, types.NamespacedName{Name: "my-secret", Namespace: "default"}, updated)
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.Data).To(HaveKeyWithValue("DB_HOST", []byte("localhost")))
		Expect(updated.Data).To(HaveKeyWithValue("DB_PORT", []byte("5432")))
		Expect(updated.Data).NotTo(HaveKey("OLD_KEY"))

		expectedEtag := crypto.ComputeEtag([]byte(fmt.Sprintf("%v", data)))
		Expect(updated.Annotations[constants.SECRET_VERSION_ANNOTATION]).To(Equal(expectedEtag))
	})

	It("skips update when etag matches", func() {
		expectedEtag := crypto.ComputeEtag([]byte(fmt.Sprintf("%v", data)))
		existing := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-secret",
				Namespace: "default",
				Annotations: map[string]string{
					constants.SECRET_VERSION_ANNOTATION: expectedEtag,
				},
			},
			Data: data,
		}
		reconciler := newReconciler(existing)
		owner := newStaticSecret()

		changed, err := reconciler.SyncKubeSecret(ctx, owner, data, target)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeFalse())

		fetched := &corev1.Secret{}
		err = reconciler.Client.Get(ctx, types.NamespacedName{Name: "my-secret", Namespace: "default"}, fetched)
		Expect(err).NotTo(HaveOccurred())
		Expect(fetched.Annotations[constants.SECRET_VERSION_ANNOTATION]).To(Equal(expectedEtag))
	})
})

var _ = Describe("SyncKubeConfigMap", func() {
	ctx := context.Background()

	data := map[string][]byte{
		"DB_HOST": []byte("localhost"),
		"DB_PORT": []byte("5432"),
	}

	target := v1beta1.SecretTarget{
		Name:           "my-configmap",
		Namespace:      "default",
		Kind:           v1beta1.SecretTargetKindConfigMap,
		CreationPolicy: v1beta1.CreationPolicyOrphan,
	}

	It("creates a new config map when it does not exist", func() {
		reconciler := newReconciler()
		owner := newStaticSecret()

		changed, err := reconciler.SyncKubeConfigMap(ctx, owner, data, target)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())

		created := &corev1.ConfigMap{}
		err = reconciler.Client.Get(ctx, types.NamespacedName{Name: "my-configmap", Namespace: "default"}, created)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Data).To(HaveKeyWithValue("DB_HOST", "localhost"))
		Expect(created.Data).To(HaveKeyWithValue("DB_PORT", "5432"))

		expectedEtag := crypto.ComputeEtag([]byte(fmt.Sprintf("%v", data)))
		Expect(created.Annotations[constants.SECRET_VERSION_ANNOTATION]).To(Equal(expectedEtag))
	})

	It("sets owner reference when creation policy is Owner", func() {
		reconciler := newReconciler()
		owner := newStaticSecret()

		ownerTarget := target
		ownerTarget.CreationPolicy = v1beta1.CreationPolicyOwner

		changed, err := reconciler.SyncKubeConfigMap(ctx, owner, data, ownerTarget)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())

		created := &corev1.ConfigMap{}
		err = reconciler.Client.Get(ctx, types.NamespacedName{Name: "my-configmap", Namespace: "default"}, created)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.OwnerReferences).To(HaveLen(1))
		Expect(created.OwnerReferences[0].Name).To(Equal(owner.GetName()))
	})

	It("does not set owner reference when creation policy is Orphan", func() {
		reconciler := newReconciler()
		owner := newStaticSecret()

		changed, err := reconciler.SyncKubeConfigMap(ctx, owner, data, target)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())

		created := &corev1.ConfigMap{}
		err = reconciler.Client.Get(ctx, types.NamespacedName{Name: "my-configmap", Namespace: "default"}, created)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.OwnerReferences).To(BeEmpty())
	})

	It("updates an existing config map when data changes", func() {
		existing := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-configmap",
				Namespace: "default",
				Annotations: map[string]string{
					constants.SECRET_VERSION_ANNOTATION: "old-etag",
				},
			},
			Data: map[string]string{"OLD_KEY": "old-value"},
		}
		reconciler := newReconciler(existing)
		owner := newStaticSecret()

		changed, err := reconciler.SyncKubeConfigMap(ctx, owner, data, target)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())

		updated := &corev1.ConfigMap{}
		err = reconciler.Client.Get(ctx, types.NamespacedName{Name: "my-configmap", Namespace: "default"}, updated)
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.Data).To(HaveKeyWithValue("DB_HOST", "localhost"))
		Expect(updated.Data).To(HaveKeyWithValue("DB_PORT", "5432"))
		Expect(updated.Data).NotTo(HaveKey("OLD_KEY"))

		expectedEtag := crypto.ComputeEtag([]byte(fmt.Sprintf("%v", data)))
		Expect(updated.Annotations[constants.SECRET_VERSION_ANNOTATION]).To(Equal(expectedEtag))
	})

	It("skips update when etag matches", func() {
		expectedEtag := crypto.ComputeEtag([]byte(fmt.Sprintf("%v", data)))
		existing := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-configmap",
				Namespace: "default",
				Annotations: map[string]string{
					constants.SECRET_VERSION_ANNOTATION: expectedEtag,
				},
			},
			Data: map[string]string{"DB_HOST": "localhost", "DB_PORT": "5432"},
		}
		reconciler := newReconciler(existing)
		owner := newStaticSecret()

		changed, err := reconciler.SyncKubeConfigMap(ctx, owner, data, target)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeFalse())
	})

	It("stores data as strings not bytes", func() {
		reconciler := newReconciler()
		owner := newStaticSecret()

		_, err := reconciler.SyncKubeConfigMap(ctx, owner, data, target)
		Expect(err).NotTo(HaveOccurred())

		created := &corev1.ConfigMap{}
		err = reconciler.Client.Get(ctx, types.NamespacedName{Name: "my-configmap", Namespace: "default"}, created)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Data["DB_HOST"]).To(Equal("localhost"))
	})
})

var _ = Describe("PropagateSecretToWorkloads", func() {
	ctx := context.Background()
	const namespace = "default"
	const secretName = "app-secret"
	const etag = "v2-etag"
	annotationKey := fmt.Sprintf(svc.ManagedSecretAnnotationFmt, secretName)

	target := v1beta1.SecretTarget{
		Name:      secretName,
		Namespace: namespace,
		Kind:      v1beta1.SecretTargetKindSecret,
	}

	newTargetSecret := func() *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
				Annotations: map[string]string{
					constants.SECRET_VERSION_ANNOTATION: etag,
				},
			},
			Data: map[string][]byte{"KEY": []byte("value")},
		}
	}

	newDeployment := func(name string, autoReload bool, consumesSecret bool) *appsv1.Deployment {
		annotations := map[string]string{}
		if autoReload {
			annotations[svc.AutoReloadAnnotation] = "true"
		}

		var envFrom []corev1.EnvFromSource
		if consumesSecret {
			envFrom = []corev1.EnvFromSource{
				{SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
				}},
			}
		}

		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   namespace,
				Annotations: annotations,
			},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "main", Image: "busybox", EnvFrom: envFrom},
						},
					},
				},
			},
		}
	}

	newDaemonSet := func(name string, autoReload bool, consumesSecret bool) *appsv1.DaemonSet {
		annotations := map[string]string{}
		if autoReload {
			annotations[svc.AutoReloadAnnotation] = "true"
		}

		var envFrom []corev1.EnvFromSource
		if consumesSecret {
			envFrom = []corev1.EnvFromSource{
				{SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
				}},
			}
		}

		return &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   namespace,
				Annotations: annotations,
			},
			Spec: appsv1.DaemonSetSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "main", Image: "busybox", EnvFrom: envFrom},
						},
					},
				},
			},
		}
	}

	newStatefulSet := func(name string, autoReload bool, consumesSecret bool) *appsv1.StatefulSet {
		annotations := map[string]string{}
		if autoReload {
			annotations[svc.AutoReloadAnnotation] = "true"
		}

		var volumes []corev1.Volume
		if consumesSecret {
			volumes = []corev1.Volume{
				{Name: "secret-vol", VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{SecretName: secretName},
				}},
			}
		}

		return &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   namespace,
				Annotations: annotations,
			},
			Spec: appsv1.StatefulSetSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "main", Image: "busybox"},
						},
						Volumes: volumes,
					},
				},
			},
		}
	}

	It("reconciles a deployment that consumes the secret via envFrom", func() {
		dep := newDeployment("web", true, true)
		reconciler := newReconciler(newTargetSecret(), dep)

		err := reconciler.PropagateSecretToWorkloads(ctx, target)
		Expect(err).NotTo(HaveOccurred())

		updated := &appsv1.Deployment{}
		Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "web", Namespace: namespace}, updated)).To(Succeed())
		Expect(updated.Annotations[annotationKey]).To(Equal(etag))
		Expect(updated.Spec.Template.Annotations[annotationKey]).To(Equal(etag))
	})

	It("reconciles a daemonset that consumes the secret", func() {
		ds := newDaemonSet("agent", true, true)
		reconciler := newReconciler(newTargetSecret(), ds)

		err := reconciler.PropagateSecretToWorkloads(ctx, target)
		Expect(err).NotTo(HaveOccurred())

		updated := &appsv1.DaemonSet{}
		Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "agent", Namespace: namespace}, updated)).To(Succeed())
		Expect(updated.Annotations[annotationKey]).To(Equal(etag))
		Expect(updated.Spec.Template.Annotations[annotationKey]).To(Equal(etag))
	})

	It("reconciles a statefulset that consumes the secret via volume", func() {
		ss := newStatefulSet("db", true, true)
		reconciler := newReconciler(newTargetSecret(), ss)

		err := reconciler.PropagateSecretToWorkloads(ctx, target)
		Expect(err).NotTo(HaveOccurred())

		updated := &appsv1.StatefulSet{}
		Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "db", Namespace: namespace}, updated)).To(Succeed())
		Expect(updated.Annotations[annotationKey]).To(Equal(etag))
		Expect(updated.Spec.Template.Annotations[annotationKey]).To(Equal(etag))
	})

	It("skips workloads without auto-reload annotation", func() {
		dep := newDeployment("no-reload", false, true)
		reconciler := newReconciler(newTargetSecret(), dep)

		err := reconciler.PropagateSecretToWorkloads(ctx, target)
		Expect(err).NotTo(HaveOccurred())

		updated := &appsv1.Deployment{}
		Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "no-reload", Namespace: namespace}, updated)).To(Succeed())
		Expect(updated.Annotations).NotTo(HaveKey(annotationKey))
	})

	It("skips workloads that do not consume the secret", func() {
		dep := newDeployment("unrelated", true, false)
		reconciler := newReconciler(newTargetSecret(), dep)

		err := reconciler.PropagateSecretToWorkloads(ctx, target)
		Expect(err).NotTo(HaveOccurred())

		updated := &appsv1.Deployment{}
		Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "unrelated", Namespace: namespace}, updated)).To(Succeed())
		Expect(updated.Annotations).NotTo(HaveKey(annotationKey))
	})

	It("skips workloads already at the current etag", func() {
		dep := newDeployment("up-to-date", true, true)
		dep.Annotations[annotationKey] = etag
		dep.Spec.Template.Annotations = map[string]string{annotationKey: etag}
		reconciler := newReconciler(newTargetSecret(), dep)

		err := reconciler.PropagateSecretToWorkloads(ctx, target)
		Expect(err).NotTo(HaveOccurred())

		updated := &appsv1.Deployment{}
		Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "up-to-date", Namespace: namespace}, updated)).To(Succeed())
		Expect(updated.Annotations[annotationKey]).To(Equal(etag))
	})

	It("reconciles multiple workload types in the same namespace", func() {
		dep := newDeployment("web", true, true)
		ds := newDaemonSet("agent", true, true)
		ss := newStatefulSet("db", true, true)
		reconciler := newReconciler(newTargetSecret(), dep, ds, ss)

		err := reconciler.PropagateSecretToWorkloads(ctx, target)
		Expect(err).NotTo(HaveOccurred())

		updatedDep := &appsv1.Deployment{}
		Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "web", Namespace: namespace}, updatedDep)).To(Succeed())
		Expect(updatedDep.Annotations[annotationKey]).To(Equal(etag))
		Expect(updatedDep.Spec.Template.Annotations[annotationKey]).To(Equal(etag))

		updatedDS := &appsv1.DaemonSet{}
		Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "agent", Namespace: namespace}, updatedDS)).To(Succeed())
		Expect(updatedDS.Annotations[annotationKey]).To(Equal(etag))
		Expect(updatedDS.Spec.Template.Annotations[annotationKey]).To(Equal(etag))

		updatedSS := &appsv1.StatefulSet{}
		Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "db", Namespace: namespace}, updatedSS)).To(Succeed())
		Expect(updatedSS.Annotations[annotationKey]).To(Equal(etag))
		Expect(updatedSS.Spec.Template.Annotations[annotationKey]).To(Equal(etag))
	})

	It("returns error when target secret does not exist", func() {
		reconciler := newReconciler()

		err := reconciler.PropagateSecretToWorkloads(ctx, target)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to get target Secret"))
	})

	It("reconciles deployment consuming secret via env valueFrom", func() {
		dep := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "env-ref",
				Namespace: namespace,
				Annotations: map[string]string{
					svc.AutoReloadAnnotation: "true",
				},
			},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "env-ref"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "env-ref"}},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "main",
								Image: "busybox",
								Env: []corev1.EnvVar{
									{
										Name: "DB_PASS",
										ValueFrom: &corev1.EnvVarSource{
											SecretKeyRef: &corev1.SecretKeySelector{
												LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
												Key:                  "password",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}
		reconciler := newReconciler(newTargetSecret(), dep)

		err := reconciler.PropagateSecretToWorkloads(ctx, target)
		Expect(err).NotTo(HaveOccurred())

		updated := &appsv1.Deployment{}
		Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "env-ref", Namespace: namespace}, updated)).To(Succeed())
		Expect(updated.Annotations[annotationKey]).To(Equal(etag))
		Expect(updated.Spec.Template.Annotations[annotationKey]).To(Equal(etag))
	})

	It("does nothing when no workloads exist", func() {
		reconciler := newReconciler(newTargetSecret())

		err := reconciler.PropagateSecretToWorkloads(ctx, target)
		Expect(err).NotTo(HaveOccurred())
	})

	Context("with a ConfigMap target", func() {
		const configMapName = "app-config"
		configMapAnnotationKey := fmt.Sprintf(svc.ManagedSecretAnnotationFmt, configMapName)

		configMapTarget := v1beta1.SecretTarget{
			Name:      configMapName,
			Namespace: namespace,
			Kind:      v1beta1.SecretTargetKindConfigMap,
		}

		newTargetConfigMap := func() *corev1.ConfigMap {
			return &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configMapName,
					Namespace: namespace,
					Annotations: map[string]string{
						constants.SECRET_VERSION_ANNOTATION: etag,
					},
				},
				Data: map[string]string{"KEY": "value"},
			}
		}

		It("reconciles a deployment consuming the configmap via envFrom", func() {
			dep := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "web",
					Namespace: namespace,
					Annotations: map[string]string{
						svc.AutoReloadAnnotation: "true",
					},
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "main",
									Image: "busybox",
									EnvFrom: []corev1.EnvFromSource{
										{ConfigMapRef: &corev1.ConfigMapEnvSource{
											LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
										}},
									},
								},
							},
						},
					},
				},
			}
			reconciler := newReconciler(newTargetConfigMap(), dep)

			err := reconciler.PropagateSecretToWorkloads(ctx, configMapTarget)
			Expect(err).NotTo(HaveOccurred())

			updated := &appsv1.Deployment{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "web", Namespace: namespace}, updated)).To(Succeed())
			Expect(updated.Annotations[configMapAnnotationKey]).To(Equal(etag))
			Expect(updated.Spec.Template.Annotations[configMapAnnotationKey]).To(Equal(etag))
		})

		It("reconciles a deployment consuming the configmap via env valueFrom", func() {
			dep := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "env-ref",
					Namespace: namespace,
					Annotations: map[string]string{
						svc.AutoReloadAnnotation: "true",
					},
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "env-ref"}},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "env-ref"}},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "main",
									Image: "busybox",
									Env: []corev1.EnvVar{
										{
											Name: "APP_MODE",
											ValueFrom: &corev1.EnvVarSource{
												ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
													LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
													Key:                  "mode",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			}
			reconciler := newReconciler(newTargetConfigMap(), dep)

			err := reconciler.PropagateSecretToWorkloads(ctx, configMapTarget)
			Expect(err).NotTo(HaveOccurred())

			updated := &appsv1.Deployment{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "env-ref", Namespace: namespace}, updated)).To(Succeed())
			Expect(updated.Annotations[configMapAnnotationKey]).To(Equal(etag))
			Expect(updated.Spec.Template.Annotations[configMapAnnotationKey]).To(Equal(etag))
		})

		It("reconciles a statefulset consuming the configmap via volume", func() {
			ss := &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "db",
					Namespace: namespace,
					Annotations: map[string]string{
						svc.AutoReloadAnnotation: "true",
					},
				},
				Spec: appsv1.StatefulSetSpec{
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "db"}},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "db"}},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{Name: "main", Image: "busybox"},
							},
							Volumes: []corev1.Volume{
								{Name: "config-vol", VolumeSource: corev1.VolumeSource{
									ConfigMap: &corev1.ConfigMapVolumeSource{
										LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
									},
								}},
							},
						},
					},
				},
			}
			reconciler := newReconciler(newTargetConfigMap(), ss)

			err := reconciler.PropagateSecretToWorkloads(ctx, configMapTarget)
			Expect(err).NotTo(HaveOccurred())

			updated := &appsv1.StatefulSet{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "db", Namespace: namespace}, updated)).To(Succeed())
			Expect(updated.Annotations[configMapAnnotationKey]).To(Equal(etag))
			Expect(updated.Spec.Template.Annotations[configMapAnnotationKey]).To(Equal(etag))
		})

		It("skips a deployment consuming a secret but not the configmap", func() {
			dep := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret-only",
					Namespace: namespace,
					Annotations: map[string]string{
						svc.AutoReloadAnnotation: "true",
					},
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "secret-only"}},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "secret-only"}},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "main",
									Image: "busybox",
									EnvFrom: []corev1.EnvFromSource{
										{SecretRef: &corev1.SecretEnvSource{
											LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
										}},
									},
								},
							},
						},
					},
				},
			}
			reconciler := newReconciler(newTargetConfigMap(), dep)

			err := reconciler.PropagateSecretToWorkloads(ctx, configMapTarget)
			Expect(err).NotTo(HaveOccurred())

			updated := &appsv1.Deployment{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "secret-only", Namespace: namespace}, updated)).To(Succeed())
			Expect(updated.Annotations).NotTo(HaveKey(configMapAnnotationKey))
		})
	})
})
