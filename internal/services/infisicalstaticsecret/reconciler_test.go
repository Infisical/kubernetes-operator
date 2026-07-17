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
	k8Errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func secret(key, env, path string) api.Secret {
	return api.Secret{
		SecretKey:   key,
		Environment: env,
		SecretPath:  path,
	}
}

func validSpec() v1beta1.InfisicalStaticSecretSpec {
	return v1beta1.InfisicalStaticSecretSpec{
		InfisicalAuthRef: v1beta1.NamespacedName{Name: "auth", Namespace: "default"},
		SyncOptions:      &v1beta1.SyncOptions{RefreshInterval: "1m"},
		Sources: []v1beta1.SecretSource{
			{ProjectId: "proj-1", EnvironmentSlug: "dev", SecretPath: "/"},
		},
		Targets: []v1beta1.SecretTarget{
			{Name: "my-secret", Namespace: "default", Kind: v1beta1.SecretTargetKindSecret, CreationPolicy: v1beta1.CreationPolicyOrphan},
		},
	}
}

var _ = Describe("Validate", func() {
	reconciler := &svc.InfisicalStaticSecretReconciler{}

	It("passes with a valid spec", func() {
		obj := &v1beta1.InfisicalStaticSecret{Spec: validSpec()}
		Expect(reconciler.Validate(obj)).To(Succeed())
	})

	It("returns error for nil object", func() {
		Expect(reconciler.Validate(nil)).To(HaveOccurred())
	})

	It("returns error when syncOptions is nil", func() {
		spec := validSpec()
		spec.SyncOptions = nil
		obj := &v1beta1.InfisicalStaticSecret{Spec: spec}
		err := reconciler.Validate(obj)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("syncOptions is required"))
	})

	It("returns error for invalid refreshInterval", func() {
		spec := validSpec()
		spec.SyncOptions.RefreshInterval = "not-a-duration"
		obj := &v1beta1.InfisicalStaticSecret{Spec: spec}
		err := reconciler.Validate(obj)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid refreshInterval"))
	})

	It("accepts valid duration formats", func() {
		for _, d := range []string{"30s", "5m", "1h", "7d", "3w"} {
			spec := validSpec()
			spec.SyncOptions.RefreshInterval = d
			obj := &v1beta1.InfisicalStaticSecret{Spec: spec}
			Expect(reconciler.Validate(obj)).To(Succeed(), "expected %q to be valid", d)
		}
	})

	It("returns error when sources is empty", func() {
		spec := validSpec()
		spec.Sources = []v1beta1.SecretSource{}
		obj := &v1beta1.InfisicalStaticSecret{Spec: spec}
		err := reconciler.Validate(obj)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("at least one source is required"))
	})

	It("returns error when a source is missing both projectId and projectSlug", func() {
		spec := validSpec()
		spec.Sources[0].ProjectId = ""
		spec.Sources[0].ProjectSlug = ""
		obj := &v1beta1.InfisicalStaticSecret{Spec: spec}
		err := reconciler.Validate(obj)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("either sources[0].projectId or sources[0].projectSlug must be set"))
	})

	It("returns error when a source has both projectId and projectSlug", func() {
		spec := validSpec()
		spec.Sources[0].ProjectId = "proj-1"
		spec.Sources[0].ProjectSlug = "proj-slug"
		obj := &v1beta1.InfisicalStaticSecret{Spec: spec}
		err := reconciler.Validate(obj)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("you declared both sources[0].projectId and sources[0].projectSlug"))
	})

	It("returns error when a source is missing environmentSlug", func() {
		spec := validSpec()
		spec.Sources[0].EnvironmentSlug = ""
		obj := &v1beta1.InfisicalStaticSecret{Spec: spec}
		err := reconciler.Validate(obj)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("sources[0].environmentSlug is required"))
	})

	It("returns error when a source is missing secretPath", func() {
		spec := validSpec()
		spec.Sources[0].SecretPath = ""
		obj := &v1beta1.InfisicalStaticSecret{Spec: spec}
		err := reconciler.Validate(obj)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("sources[0].secretPath is required"))
	})

	It("returns error when targets is empty", func() {
		spec := validSpec()
		spec.Targets = []v1beta1.SecretTarget{}
		obj := &v1beta1.InfisicalStaticSecret{Spec: spec}
		err := reconciler.Validate(obj)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("at least one target is required"))
	})

	It("returns error when a target is missing name", func() {
		spec := validSpec()
		spec.Targets[0].Name = ""
		obj := &v1beta1.InfisicalStaticSecret{Spec: spec}
		err := reconciler.Validate(obj)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("targets[0].name is required"))
	})

	It("returns error when a target is missing namespace", func() {
		spec := validSpec()
		spec.Targets[0].Namespace = ""
		obj := &v1beta1.InfisicalStaticSecret{Spec: spec}
		err := reconciler.Validate(obj)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("targets[0].namespace is required"))
	})

	It("returns error for duplicate targets", func() {
		spec := validSpec()
		spec.Targets = append(spec.Targets, spec.Targets[0])
		obj := &v1beta1.InfisicalStaticSecret{Spec: spec}
		err := reconciler.Validate(obj)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("duplicate target"))
	})

	It("allows same name in different namespaces", func() {
		spec := validSpec()
		spec.Targets = append(spec.Targets, v1beta1.SecretTarget{
			Name:           "my-secret",
			Namespace:      "other-ns",
			Kind:           v1beta1.SecretTargetKindSecret,
			CreationPolicy: v1beta1.CreationPolicyOrphan,
		})
		obj := &v1beta1.InfisicalStaticSecret{Spec: spec}
		Expect(reconciler.Validate(obj)).To(Succeed())
	})

	It("returns error when secretType is set on a ConfigMap target", func() {
		spec := validSpec()
		spec.Targets[0].Kind = v1beta1.SecretTargetKindConfigMap
		spec.Targets[0].SecretType = corev1.SecretTypeOpaque
		obj := &v1beta1.InfisicalStaticSecret{Spec: spec}
		err := reconciler.Validate(obj)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("secretType is only valid for Secret targets"))
	})

	It("allows secretType on a Secret target", func() {
		spec := validSpec()
		spec.Targets[0].SecretType = corev1.SecretTypeOpaque
		obj := &v1beta1.InfisicalStaticSecret{Spec: spec}
		Expect(reconciler.Validate(obj)).To(Succeed())
	})
})

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

			data, err := reconciler.RenderTargetOutput(svc.RenderContext{
				MergedSecrets: secrets,
				RawSecrets:    secrets,
			}, target)
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

			data, err := reconciler.RenderTargetOutput(svc.RenderContext{}, target)
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
					Data: v1beta1.SecretTemplateData{
						Map: map[string]string{
							"dsn": "postgresql://user:pass@{{ .DB_HOST.Value }}:{{ .DB_PORT.Value }}/{{ .DB_NAME.Value }}",
						},
					},
				},
			}

			data, err := reconciler.RenderTargetOutput(svc.RenderContext{
				MergedSecrets: secrets,
				RawSecrets:    secrets,
			}, target)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(HaveLen(1))
			Expect(data).To(HaveKeyWithValue("dsn", []byte("postgresql://user:pass@localhost:5432/mydb")))
		})

		It("renders template data using .SecretPath accessor", func() {
			target := v1beta1.SecretTarget{
				Name:      "test",
				Namespace: "default",
				Kind:      v1beta1.SecretTargetKindSecret,
				Template: &v1beta1.SecretTemplate{
					Data: v1beta1.SecretTemplateData{
						Map: map[string]string{
							"info": "{{ .DB_HOST.Value }} from {{ .DB_HOST.SecretPath }}",
						},
					},
				},
			}

			data, err := reconciler.RenderTargetOutput(svc.RenderContext{
				MergedSecrets: secrets,
				RawSecrets:    secrets,
			}, target)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(HaveKeyWithValue("info", []byte("localhost from /db")))
		})

		It("renders multiple template keys", func() {
			target := v1beta1.SecretTarget{
				Name:      "test",
				Namespace: "default",
				Kind:      v1beta1.SecretTargetKindConfigMap,
				Template: &v1beta1.SecretTemplate{
					Data: v1beta1.SecretTemplateData{
						Map: map[string]string{
							"host": "{{ .DB_HOST.Value }}",
							"port": "{{ .DB_PORT.Value }}",
						},
					},
				},
			}

			data, err := reconciler.RenderTargetOutput(svc.RenderContext{
				MergedSecrets: secrets,
				RawSecrets:    secrets,
			}, target)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(HaveLen(2))
			Expect(data).To(HaveKeyWithValue("host", []byte("localhost")))
			Expect(data).To(HaveKeyWithValue("port", []byte("5432")))
		})

		It("returns error for invalid template syntax", func() {
			target := v1beta1.SecretTarget{
				Name:      "bad",
				Namespace: "default",
				Kind:      v1beta1.SecretTargetKindSecret,
				Template: &v1beta1.SecretTemplate{
					Data: v1beta1.SecretTemplateData{
						Map: map[string]string{
							"bad": "{{ .DB_HOST",
						},
					},
				},
			}

			_, err := reconciler.RenderTargetOutput(svc.RenderContext{
				MergedSecrets: secrets,
				RawSecrets:    secrets,
			}, target)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to parse template"))
		})

		It("renders template using secretFrom to reference secrets in subfolders", func() {
			subfolderSecrets := []api.Secret{
				{SecretKey: "DB_HOST", SecretValue: "prod-db.example.com", SecretPath: "/folder/subfolder"},
				{SecretKey: "DB_PORT", SecretValue: "5432", SecretPath: "/folder/subfolder"},
				{SecretKey: "API_KEY", SecretValue: "sk-secret-123", SecretPath: "/folder/other"},
			}

			target := v1beta1.SecretTarget{
				Name:      "ref-test",
				Namespace: "default",
				Kind:      v1beta1.SecretTargetKindSecret,
				Template: &v1beta1.SecretTemplate{
					Data: v1beta1.SecretTemplateData{
						Map: map[string]string{
							"dsn":     `postgresql://user:pass@{{ secretFrom "/folder/subfolder" "DB_HOST" }}:{{ secretFrom "/folder/subfolder" "DB_PORT" }}/mydb`,
							"api_key": `{{ secretFrom "/folder/other" "API_KEY" }}`,
						},
					},
				},
			}

			data, err := reconciler.RenderTargetOutput(svc.RenderContext{
				MergedSecrets: subfolderSecrets,
				RawSecrets:    subfolderSecrets,
			}, target)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(HaveLen(2))
			Expect(data).To(HaveKeyWithValue("dsn", []byte("postgresql://user:pass@prod-db.example.com:5432/mydb")))
			Expect(data).To(HaveKeyWithValue("api_key", []byte("sk-secret-123")))
		})

		It("resolves imported secrets via secretFrom and raw secrets take priority", func() {
			rawSecrets := []api.Secret{
				{SecretKey: "APP_NAME", SecretValue: "my-app", SecretPath: "/app"},
			}
			importedSecrets := []api.Secret{
				{SecretKey: "REDIS_URL", SecretValue: "redis://cache:6379", SecretPath: "/shared"},
				{SecretKey: "APP_NAME", SecretValue: "imported-app", SecretPath: "/shared"},
			}
			mergedSecrets := []api.Secret{
				{SecretKey: "APP_NAME", SecretValue: "my-app", SecretPath: "/app"},
				{SecretKey: "REDIS_URL", SecretValue: "redis://cache:6379", SecretPath: "/shared"},
			}

			target := v1beta1.SecretTarget{
				Name:      "import-test",
				Namespace: "default",
				Kind:      v1beta1.SecretTargetKindSecret,
				Template: &v1beta1.SecretTemplate{
					Data: v1beta1.SecretTemplateData{
						Map: map[string]string{
							"redis":             `{{ secretFrom "/shared" "REDIS_URL" }}`,
							"app_name":          `{{ secretFrom "/app" "APP_NAME" }}`,
							"imported_app_name": `{{ secretFrom "/shared" "APP_NAME" }}`,
						},
					},
				},
			}

			data, err := reconciler.RenderTargetOutput(svc.RenderContext{
				RawSecrets:      rawSecrets,
				ImportedSecrets: importedSecrets,
				MergedSecrets:   mergedSecrets,
			}, target)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(HaveLen(3))
			Expect(data).To(HaveKeyWithValue("redis", []byte("redis://cache:6379")))
			Expect(data).To(HaveKeyWithValue("app_name", []byte("my-app")))
			Expect(data).To(HaveKeyWithValue("imported_app_name", []byte("imported-app")))
		})
	})

	Context("with a bulk (string) template", func() {
		It("expands a range over every secret into the resulting map", func() {
			target := v1beta1.SecretTarget{
				Name:      "all",
				Namespace: "default",
				Kind:      v1beta1.SecretTargetKindSecret,
				Template: &v1beta1.SecretTemplate{
					Data: v1beta1.SecretTemplateData{
						Raw: `{{- range $key, $secret := . }}
{{ $key }}: "{{ $secret.Value }}"
{{- end }}`,
					},
				},
			}

			data, err := reconciler.RenderTargetOutput(svc.RenderContext{
				MergedSecrets: secrets,
				RawSecrets:    secrets,
			}, target)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(HaveLen(3))
			Expect(data).To(HaveKeyWithValue("DB_HOST", []byte("localhost")))
			Expect(data).To(HaveKeyWithValue("DB_PORT", []byte("5432")))
			Expect(data).To(HaveKeyWithValue("DB_NAME", []byte("mydb")))
		})

		It("supports referencing individual secrets alongside a range", func() {
			target := v1beta1.SecretTarget{
				Name:      "mixed",
				Namespace: "default",
				Kind:      v1beta1.SecretTargetKindSecret,
				Template: &v1beta1.SecretTemplate{
					Data: v1beta1.SecretTemplateData{
						Raw: `DSN: "postgres://{{ .DB_HOST.Value }}:{{ .DB_PORT.Value }}/{{ .DB_NAME.Value }}"
COUNT: "{{ len . }}"`,
					},
				},
			}

			data, err := reconciler.RenderTargetOutput(svc.RenderContext{
				MergedSecrets: secrets,
				RawSecrets:    secrets,
			}, target)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(HaveKeyWithValue("DSN", []byte("postgres://localhost:5432/mydb")))
			Expect(data).To(HaveKeyWithValue("COUNT", []byte("3")))
		})

		It("returns an empty map for a template that renders only whitespace", func() {
			target := v1beta1.SecretTarget{
				Name:      "empty",
				Namespace: "default",
				Kind:      v1beta1.SecretTargetKindSecret,
				Template: &v1beta1.SecretTemplate{
					Data: v1beta1.SecretTemplateData{Raw: "   \n  \n"},
				},
			}

			data, err := reconciler.RenderTargetOutput(svc.RenderContext{
				MergedSecrets: secrets,
				RawSecrets:    secrets,
			}, target)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(BeEmpty())
		})

		It("returns error for invalid template syntax", func() {
			target := v1beta1.SecretTarget{
				Name:      "bad-tmpl",
				Namespace: "default",
				Kind:      v1beta1.SecretTargetKindSecret,
				Template: &v1beta1.SecretTemplate{
					Data: v1beta1.SecretTemplateData{Raw: "{{ .DB_HOST"},
				},
			}

			_, err := reconciler.RenderTargetOutput(svc.RenderContext{
				MergedSecrets: secrets,
				RawSecrets:    secrets,
			}, target)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to parse bulk template"))
		})

		It("returns error when rendered output is not a YAML map", func() {
			target := v1beta1.SecretTarget{
				Name:      "bad-yaml",
				Namespace: "default",
				Kind:      v1beta1.SecretTargetKindSecret,
				Template: &v1beta1.SecretTemplate{
					Data: v1beta1.SecretTemplateData{Raw: `- just
- a
- list`},
				},
			}

			_, err := reconciler.RenderTargetOutput(svc.RenderContext{
				MergedSecrets: secrets,
				RawSecrets:    secrets,
			}, target)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("bulk template output is not a valid YAML map"))
		})
	})
})

var _ = Describe("SecretTemplateData JSON round-trip", func() {
	It("decodes a JSON object into the map form", func() {
		var d v1beta1.SecretTemplateData
		Expect(d.UnmarshalJSON([]byte(`{"FOO": "bar"}`))).To(Succeed())
		Expect(d.IsMap()).To(BeTrue())
		Expect(d.IsRaw()).To(BeFalse())
		Expect(d.Map).To(HaveKeyWithValue("FOO", "bar"))
	})

	It("decodes a JSON string into the raw form", func() {
		var d v1beta1.SecretTemplateData
		Expect(d.UnmarshalJSON([]byte(`"{{ range . }}{{ end }}"`))).To(Succeed())
		Expect(d.IsRaw()).To(BeTrue())
		Expect(d.IsMap()).To(BeFalse())
		Expect(d.Raw).To(Equal("{{ range . }}{{ end }}"))
	})

	It("rejects unsupported JSON shapes", func() {
		var d v1beta1.SecretTemplateData
		Expect(d.UnmarshalJSON([]byte(`123`))).To(HaveOccurred())
		Expect(d.UnmarshalJSON([]byte(`["a"]`))).To(HaveOccurred())
	})

	It("re-emits the map form when marshalled", func() {
		d := v1beta1.SecretTemplateData{Map: map[string]string{"FOO": "bar"}}
		out, err := d.MarshalJSON()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(out)).To(Equal(`{"FOO":"bar"}`))
	})

	It("re-emits the raw form when marshalled", func() {
		d := v1beta1.SecretTemplateData{Raw: "tmpl"}
		out, err := d.MarshalJSON()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(out)).To(Equal(`"tmpl"`))
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

		changed, _, err := reconciler.SyncKubeSecret(ctx, owner, data, target)
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

		changed, _, err := reconciler.SyncKubeSecret(ctx, owner, data, ownerTarget)
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

		changed, _, err := reconciler.SyncKubeSecret(ctx, owner, data, target)
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

		changed, _, err := reconciler.SyncKubeSecret(ctx, owner, data, target)
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

		changed, _, err := reconciler.SyncKubeSecret(ctx, owner, data, target)
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

		changed, _, err := reconciler.SyncKubeConfigMap(ctx, owner, data, target)
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

		changed, _, err := reconciler.SyncKubeConfigMap(ctx, owner, data, ownerTarget)
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

		changed, _, err := reconciler.SyncKubeConfigMap(ctx, owner, data, target)
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

		changed, _, err := reconciler.SyncKubeConfigMap(ctx, owner, data, target)
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

		changed, _, err := reconciler.SyncKubeConfigMap(ctx, owner, data, target)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeFalse())
	})

	It("stores data as strings not bytes", func() {
		reconciler := newReconciler()
		owner := newStaticSecret()

		_, _, err := reconciler.SyncKubeConfigMap(ctx, owner, data, target)
		Expect(err).NotTo(HaveOccurred())

		created := &corev1.ConfigMap{}
		err = reconciler.Client.Get(ctx, types.NamespacedName{Name: "my-configmap", Namespace: "default"}, created)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Data["DB_HOST"]).To(Equal("localhost"))
	})
})

var _ = Describe("SyncKubeSecret metadata", func() {
	ctx := context.Background()

	data := map[string][]byte{
		"DB_HOST": []byte("localhost"),
	}

	baseTarget := v1beta1.SecretTarget{
		Name:           "meta-secret",
		Namespace:      "default",
		Kind:           v1beta1.SecretTargetKindSecret,
		SecretType:     corev1.SecretTypeOpaque,
		CreationPolicy: v1beta1.CreationPolicyOrphan,
	}

	Context("with explicit target.Metadata (direct injection)", func() {
		It("applies target metadata labels and annotations on create", func() {
			reconciler := newReconciler()
			owner := newStaticSecret()

			target := baseTarget
			target.Metadata = &v1beta1.SecretTargetMetadata{
				Labels:      map[string]string{"team": "platform"},
				Annotations: map[string]string{"note": "managed-by-test"},
			}

			changed, _, err := reconciler.SyncKubeSecret(ctx, owner, data, target)
			Expect(err).NotTo(HaveOccurred())
			Expect(changed).To(BeTrue())

			created := &corev1.Secret{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "meta-secret", Namespace: "default"}, created)).To(Succeed())
			Expect(created.Labels).To(HaveKeyWithValue("team", "platform"))
			Expect(created.Annotations).To(HaveKeyWithValue("note", "managed-by-test"))
			Expect(created.Annotations).To(HaveKey(constants.SECRET_VERSION_ANNOTATION))
			Expect(created.Annotations).NotTo(HaveKey(constants.MANAGED_LABELS_ANNOTATION))
		})

		It("replaces metadata on update, preserving system annotations", func() {
			existing := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "meta-secret",
					Namespace: "default",
					Labels:    map[string]string{"old-label": "gone"},
					Annotations: map[string]string{
						constants.SECRET_VERSION_ANNOTATION:  "old-etag",
						"kubectl.kubernetes.io/last-applied": "keep-me",
						"user-annotation":                    "should-be-removed",
					},
				},
				Data: map[string][]byte{"OLD": []byte("old")},
			}
			reconciler := newReconciler(existing)
			owner := newStaticSecret()

			target := baseTarget
			target.Metadata = &v1beta1.SecretTargetMetadata{
				Labels:      map[string]string{"team": "platform"},
				Annotations: map[string]string{"note": "injected"},
			}

			changed, _, err := reconciler.SyncKubeSecret(ctx, owner, data, target)
			Expect(err).NotTo(HaveOccurred())
			Expect(changed).To(BeTrue())

			updated := &corev1.Secret{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "meta-secret", Namespace: "default"}, updated)).To(Succeed())
			Expect(updated.Labels).To(Equal(map[string]string{"team": "platform"}))
			Expect(updated.Annotations).To(HaveKeyWithValue("note", "injected"))
			Expect(updated.Annotations).To(HaveKeyWithValue("kubectl.kubernetes.io/last-applied", "keep-me"))
			Expect(updated.Annotations).To(HaveKey(constants.SECRET_VERSION_ANNOTATION))
			Expect(updated.Annotations).NotTo(HaveKey("user-annotation"))
		})
	})

	Context("without target.Metadata (CRD merge)", func() {
		It("copies CRD labels and annotations on create", func() {
			reconciler := newReconciler()
			owner := newStaticSecret()
			owner.Labels = map[string]string{"env": "dev"}
			owner.Annotations = map[string]string{"description": "test secret"}

			changed, _, err := reconciler.SyncKubeSecret(ctx, owner, data, baseTarget)
			Expect(err).NotTo(HaveOccurred())
			Expect(changed).To(BeTrue())

			created := &corev1.Secret{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "meta-secret", Namespace: "default"}, created)).To(Succeed())
			Expect(created.Labels).To(HaveKeyWithValue("env", "dev"))
			Expect(created.Annotations).To(HaveKeyWithValue("description", "test secret"))
			Expect(created.Annotations).To(HaveKey(constants.MANAGED_LABELS_ANNOTATION))
			Expect(created.Annotations).To(HaveKey(constants.MANAGED_ANNOTATIONS_ANNOTATION))
		})

		It("filters system annotations from CRD", func() {
			reconciler := newReconciler()
			owner := newStaticSecret()
			owner.Annotations = map[string]string{
				"kubectl.kubernetes.io/last-applied": "system-value",
				"helm.sh/chart":                      "my-chart",
				"custom-annotation":                  "keep-me",
			}

			changed, _, err := reconciler.SyncKubeSecret(ctx, owner, data, baseTarget)
			Expect(err).NotTo(HaveOccurred())
			Expect(changed).To(BeTrue())

			created := &corev1.Secret{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "meta-secret", Namespace: "default"}, created)).To(Succeed())
			Expect(created.Annotations).To(HaveKeyWithValue("custom-annotation", "keep-me"))
			Expect(created.Annotations).NotTo(HaveKey("kubectl.kubernetes.io/last-applied"))
			Expect(created.Annotations).NotTo(HaveKey("helm.sh/chart"))
		})

		It("cleans up previously managed labels removed from CRD", func() {
			existing := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "meta-secret",
					Namespace: "default",
					Labels:    map[string]string{"env": "dev", "user-added": "keep"},
					Annotations: map[string]string{
						constants.SECRET_VERSION_ANNOTATION:      "old-etag",
						constants.MANAGED_LABELS_ANNOTATION:      "env",
						constants.MANAGED_ANNOTATIONS_ANNOTATION: "",
					},
				},
				Data: map[string][]byte{"OLD": []byte("old")},
			}
			reconciler := newReconciler(existing)

			owner := newStaticSecret()
			// CRD no longer has the "env" label

			changed, _, err := reconciler.SyncKubeSecret(ctx, owner, data, baseTarget)
			Expect(err).NotTo(HaveOccurred())
			Expect(changed).To(BeTrue())

			updated := &corev1.Secret{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "meta-secret", Namespace: "default"}, updated)).To(Succeed())
			Expect(updated.Labels).NotTo(HaveKey("env"))
			Expect(updated.Labels).To(HaveKeyWithValue("user-added", "keep"))
		})

		It("preserves non-managed annotations added directly to the resource", func() {
			etag := crypto.ComputeEtag([]byte(fmt.Sprintf("%v", data)))
			existing := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "meta-secret",
					Namespace: "default",
					Annotations: map[string]string{
						constants.SECRET_VERSION_ANNOTATION:      etag,
						constants.MANAGED_LABELS_ANNOTATION:      "",
						constants.MANAGED_ANNOTATIONS_ANNOTATION: "",
						"user-added-annotation":                  "preserve-me",
					},
				},
				Data: data,
			}
			reconciler := newReconciler(existing)
			owner := newStaticSecret()

			changed, _, err := reconciler.SyncKubeSecret(ctx, owner, data, baseTarget)
			Expect(err).NotTo(HaveOccurred())
			Expect(changed).To(BeFalse())

			fetched := &corev1.Secret{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "meta-secret", Namespace: "default"}, fetched)).To(Succeed())
			Expect(fetched.Annotations).To(HaveKeyWithValue("user-added-annotation", "preserve-me"))
		})
	})
})

var _ = Describe("SyncKubeConfigMap metadata", func() {
	ctx := context.Background()

	data := map[string][]byte{
		"DB_HOST": []byte("localhost"),
	}

	baseTarget := v1beta1.SecretTarget{
		Name:           "meta-configmap",
		Namespace:      "default",
		Kind:           v1beta1.SecretTargetKindConfigMap,
		CreationPolicy: v1beta1.CreationPolicyOrphan,
	}

	It("applies target metadata on create", func() {
		reconciler := newReconciler()
		owner := newStaticSecret()

		target := baseTarget
		target.Metadata = &v1beta1.SecretTargetMetadata{
			Labels:      map[string]string{"team": "platform"},
			Annotations: map[string]string{"note": "injected"},
		}

		changed, _, err := reconciler.SyncKubeConfigMap(ctx, owner, data, target)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())

		created := &corev1.ConfigMap{}
		Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "meta-configmap", Namespace: "default"}, created)).To(Succeed())
		Expect(created.Labels).To(HaveKeyWithValue("team", "platform"))
		Expect(created.Annotations).To(HaveKeyWithValue("note", "injected"))
		Expect(created.Annotations).NotTo(HaveKey(constants.MANAGED_LABELS_ANNOTATION))
	})

	It("merges CRD metadata on create when target.Metadata is nil", func() {
		reconciler := newReconciler()
		owner := newStaticSecret()
		owner.Labels = map[string]string{"env": "staging"}

		changed, _, err := reconciler.SyncKubeConfigMap(ctx, owner, data, baseTarget)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())

		created := &corev1.ConfigMap{}
		Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "meta-configmap", Namespace: "default"}, created)).To(Succeed())
		Expect(created.Labels).To(HaveKeyWithValue("env", "staging"))
		Expect(created.Annotations).To(HaveKey(constants.MANAGED_LABELS_ANNOTATION))
	})

	It("returns dataChanged=false for metadata-only update", func() {
		etag := crypto.ComputeEtag([]byte(fmt.Sprintf("%v", data)))
		existing := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "meta-configmap",
				Namespace: "default",
				Annotations: map[string]string{
					constants.SECRET_VERSION_ANNOTATION: etag,
				},
			},
			Data: map[string]string{"DB_HOST": "localhost"},
		}
		reconciler := newReconciler(existing)
		owner := newStaticSecret()
		owner.Labels = map[string]string{"new-label": "added"}

		changed, _, err := reconciler.SyncKubeConfigMap(ctx, owner, data, baseTarget)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeFalse())

		updated := &corev1.ConfigMap{}
		Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "meta-configmap", Namespace: "default"}, updated)).To(Succeed())
		Expect(updated.Labels).To(HaveKeyWithValue("new-label", "added"))
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

	type workloadConsumesKind string

	const (
		consumesSecret    workloadConsumesKind = "Secret"
		consumesConfigMap workloadConsumesKind = "ConfigMap"
		consumesNothing   workloadConsumesKind = ""
	)

	envFromForKind := func(kind workloadConsumesKind, resourceName string) []corev1.EnvFromSource {
		switch kind {
		case consumesSecret:
			return []corev1.EnvFromSource{
				{SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: resourceName},
				}},
			}
		case consumesConfigMap:
			return []corev1.EnvFromSource{
				{ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: resourceName},
				}},
			}
		default:
			return nil
		}
	}

	newDeployment := func(name string, autoReload bool, consumes workloadConsumesKind, resourceName string) *appsv1.Deployment {
		annotations := map[string]string{}
		if autoReload {
			annotations[svc.AutoReloadAnnotation] = "true"
		}
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Annotations: annotations},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "main", Image: "busybox", EnvFrom: envFromForKind(consumes, resourceName)},
						},
					},
				},
			},
		}
	}

	newDaemonSet := func(name string, autoReload bool, consumes workloadConsumesKind, resourceName string) *appsv1.DaemonSet {
		annotations := map[string]string{}
		if autoReload {
			annotations[svc.AutoReloadAnnotation] = "true"
		}
		return &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Annotations: annotations},
			Spec: appsv1.DaemonSetSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "main", Image: "busybox", EnvFrom: envFromForKind(consumes, resourceName)},
						},
					},
				},
			},
		}
	}

	newStatefulSet := func(name string, autoReload bool, consumes workloadConsumesKind, resourceName string) *appsv1.StatefulSet {
		annotations := map[string]string{}
		if autoReload {
			annotations[svc.AutoReloadAnnotation] = "true"
		}
		return &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Annotations: annotations},
			Spec: appsv1.StatefulSetSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "main", Image: "busybox", EnvFrom: envFromForKind(consumes, resourceName)},
						},
					},
				},
			},
		}
	}

	newDeploymentWithInitContainer := func(name string, autoReload bool, consumes workloadConsumesKind, resourceName string) *appsv1.Deployment {
		dep := newDeployment(name, autoReload, consumesNothing, "")
		dep.Spec.Template.Spec.InitContainers = []corev1.Container{
			{Name: "init", Image: "busybox", EnvFrom: envFromForKind(consumes, resourceName)},
		}
		return dep
	}

	newDaemonSetWithInitContainer := func(name string, autoReload bool, consumes workloadConsumesKind, resourceName string) *appsv1.DaemonSet {
		ds := newDaemonSet(name, autoReload, consumesNothing, "")
		ds.Spec.Template.Spec.InitContainers = []corev1.Container{
			{Name: "init", Image: "busybox", EnvFrom: envFromForKind(consumes, resourceName)},
		}
		return ds
	}

	newStatefulSetWithInitContainer := func(name string, autoReload bool, consumes workloadConsumesKind, resourceName string) *appsv1.StatefulSet {
		ss := newStatefulSet(name, autoReload, consumesNothing, "")
		ss.Spec.Template.Spec.InitContainers = []corev1.Container{
			{Name: "init", Image: "busybox", EnvFrom: envFromForKind(consumes, resourceName)},
		}
		return ss
	}

	Context("with a Secret target", func() {
		It("reconciles a deployment that consumes the secret via envFrom", func() {
			dep := newDeployment("web", true, consumesSecret, secretName)
			reconciler := newReconciler(newTargetSecret(), dep)

			affected, err := reconciler.PropagateSecretToWorkloads(ctx, target, etag)
			Expect(err).NotTo(HaveOccurred())
			Expect(affected).To(Equal(1))

			updated := &appsv1.Deployment{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "web", Namespace: namespace}, updated)).To(Succeed())
			Expect(updated.Annotations[annotationKey]).To(Equal(etag))
			Expect(updated.Spec.Template.Annotations[annotationKey]).To(Equal(etag))
		})

		It("reconciles a daemonset that consumes the secret", func() {
			ds := newDaemonSet("agent", true, consumesSecret, secretName)
			reconciler := newReconciler(newTargetSecret(), ds)

			affected, err := reconciler.PropagateSecretToWorkloads(ctx, target, etag)
			Expect(err).NotTo(HaveOccurred())
			Expect(affected).To(Equal(1))

			updated := &appsv1.DaemonSet{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "agent", Namespace: namespace}, updated)).To(Succeed())
			Expect(updated.Annotations[annotationKey]).To(Equal(etag))
			Expect(updated.Spec.Template.Annotations[annotationKey]).To(Equal(etag))
		})

		It("reconciles a statefulset that consumes the secret", func() {
			ss := newStatefulSet("db", true, consumesSecret, secretName)
			reconciler := newReconciler(newTargetSecret(), ss)

			affected, err := reconciler.PropagateSecretToWorkloads(ctx, target, etag)
			Expect(err).NotTo(HaveOccurred())
			Expect(affected).To(Equal(1))

			updated := &appsv1.StatefulSet{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "db", Namespace: namespace}, updated)).To(Succeed())
			Expect(updated.Annotations[annotationKey]).To(Equal(etag))
			Expect(updated.Spec.Template.Annotations[annotationKey]).To(Equal(etag))
		})

		It("skips workloads without auto-reload annotation", func() {
			dep := newDeployment("no-reload", false, consumesSecret, secretName)
			reconciler := newReconciler(newTargetSecret(), dep)

			affected, err := reconciler.PropagateSecretToWorkloads(ctx, target, etag)
			Expect(err).NotTo(HaveOccurred())
			Expect(affected).To(Equal(0))

			updated := &appsv1.Deployment{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "no-reload", Namespace: namespace}, updated)).To(Succeed())
			Expect(updated.Annotations).NotTo(HaveKey(annotationKey))
		})

		It("skips workloads that do not consume the secret", func() {
			dep := newDeployment("unrelated", true, consumesNothing, "")
			reconciler := newReconciler(newTargetSecret(), dep)

			affected, err := reconciler.PropagateSecretToWorkloads(ctx, target, etag)
			Expect(err).NotTo(HaveOccurred())
			Expect(affected).To(Equal(0))

			updated := &appsv1.Deployment{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "unrelated", Namespace: namespace}, updated)).To(Succeed())
			Expect(updated.Annotations).NotTo(HaveKey(annotationKey))
		})

		It("skips workloads already at the current etag", func() {
			dep := newDeployment("up-to-date", true, consumesSecret, secretName)
			dep.Annotations[annotationKey] = etag
			dep.Spec.Template.Annotations = map[string]string{annotationKey: etag}
			reconciler := newReconciler(newTargetSecret(), dep)

			affected, err := reconciler.PropagateSecretToWorkloads(ctx, target, etag)
			Expect(err).NotTo(HaveOccurred())
			Expect(affected).To(Equal(1))

			updated := &appsv1.Deployment{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "up-to-date", Namespace: namespace}, updated)).To(Succeed())
			Expect(updated.Annotations[annotationKey]).To(Equal(etag))
		})

		It("reconciles multiple workload types in the same namespace", func() {
			dep := newDeployment("web", true, consumesSecret, secretName)
			ds := newDaemonSet("agent", true, consumesSecret, secretName)
			ss := newStatefulSet("db", true, consumesSecret, secretName)
			reconciler := newReconciler(newTargetSecret(), dep, ds, ss)

			affected, err := reconciler.PropagateSecretToWorkloads(ctx, target, etag)
			Expect(err).NotTo(HaveOccurred())
			Expect(affected).To(Equal(3))

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

		It("reconciles a deployment consuming the secret via init container", func() {
			dep := newDeploymentWithInitContainer("web-init", true, consumesSecret, secretName)
			reconciler := newReconciler(newTargetSecret(), dep)

			affected, err := reconciler.PropagateSecretToWorkloads(ctx, target, etag)
			Expect(err).NotTo(HaveOccurred())
			Expect(affected).To(Equal(1))

			updated := &appsv1.Deployment{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "web-init", Namespace: namespace}, updated)).To(Succeed())
			Expect(updated.Annotations[annotationKey]).To(Equal(etag))
			Expect(updated.Spec.Template.Annotations[annotationKey]).To(Equal(etag))
		})

		It("reconciles a daemonset consuming the secret via init container", func() {
			ds := newDaemonSetWithInitContainer("agent-init", true, consumesSecret, secretName)
			reconciler := newReconciler(newTargetSecret(), ds)

			affected, err := reconciler.PropagateSecretToWorkloads(ctx, target, etag)
			Expect(err).NotTo(HaveOccurred())
			Expect(affected).To(Equal(1))

			updated := &appsv1.DaemonSet{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "agent-init", Namespace: namespace}, updated)).To(Succeed())
			Expect(updated.Annotations[annotationKey]).To(Equal(etag))
			Expect(updated.Spec.Template.Annotations[annotationKey]).To(Equal(etag))
		})

		It("reconciles a statefulset consuming the secret via init container", func() {
			ss := newStatefulSetWithInitContainer("db-init", true, consumesSecret, secretName)
			reconciler := newReconciler(newTargetSecret(), ss)

			affected, err := reconciler.PropagateSecretToWorkloads(ctx, target, etag)
			Expect(err).NotTo(HaveOccurred())
			Expect(affected).To(Equal(1))

			updated := &appsv1.StatefulSet{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "db-init", Namespace: namespace}, updated)).To(Succeed())
			Expect(updated.Annotations[annotationKey]).To(Equal(etag))
			Expect(updated.Spec.Template.Annotations[annotationKey]).To(Equal(etag))
		})

		It("does nothing when no workloads exist", func() {
			reconciler := newReconciler(newTargetSecret())

			affected, err := reconciler.PropagateSecretToWorkloads(ctx, target, etag)
			Expect(err).NotTo(HaveOccurred())
			Expect(affected).To(Equal(0))
		})
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
			dep := newDeployment("web", true, consumesConfigMap, configMapName)
			reconciler := newReconciler(newTargetConfigMap(), dep)

			affected, err := reconciler.PropagateSecretToWorkloads(ctx, configMapTarget, etag)
			Expect(err).NotTo(HaveOccurred())
			Expect(affected).To(Equal(1))

			updated := &appsv1.Deployment{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "web", Namespace: namespace}, updated)).To(Succeed())
			Expect(updated.Annotations[configMapAnnotationKey]).To(Equal(etag))
			Expect(updated.Spec.Template.Annotations[configMapAnnotationKey]).To(Equal(etag))
		})

		It("reconciles a statefulset consuming the configmap via envFrom", func() {
			ss := newStatefulSet("db", true, consumesConfigMap, configMapName)
			reconciler := newReconciler(newTargetConfigMap(), ss)

			affected, err := reconciler.PropagateSecretToWorkloads(ctx, configMapTarget, etag)
			Expect(err).NotTo(HaveOccurred())
			Expect(affected).To(Equal(1))

			updated := &appsv1.StatefulSet{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "db", Namespace: namespace}, updated)).To(Succeed())
			Expect(updated.Annotations[configMapAnnotationKey]).To(Equal(etag))
			Expect(updated.Spec.Template.Annotations[configMapAnnotationKey]).To(Equal(etag))
		})

		It("reconciles a deployment consuming the configmap via init container", func() {
			dep := newDeploymentWithInitContainer("web-init", true, consumesConfigMap, configMapName)
			reconciler := newReconciler(newTargetConfigMap(), dep)

			affected, err := reconciler.PropagateSecretToWorkloads(ctx, configMapTarget, etag)
			Expect(err).NotTo(HaveOccurred())
			Expect(affected).To(Equal(1))

			updated := &appsv1.Deployment{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "web-init", Namespace: namespace}, updated)).To(Succeed())
			Expect(updated.Annotations[configMapAnnotationKey]).To(Equal(etag))
			Expect(updated.Spec.Template.Annotations[configMapAnnotationKey]).To(Equal(etag))
		})

		It("reconciles a daemonset consuming the configmap via init container", func() {
			ds := newDaemonSetWithInitContainer("agent-init", true, consumesConfigMap, configMapName)
			reconciler := newReconciler(newTargetConfigMap(), ds)

			affected, err := reconciler.PropagateSecretToWorkloads(ctx, configMapTarget, etag)
			Expect(err).NotTo(HaveOccurred())
			Expect(affected).To(Equal(1))

			updated := &appsv1.DaemonSet{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "agent-init", Namespace: namespace}, updated)).To(Succeed())
			Expect(updated.Annotations[configMapAnnotationKey]).To(Equal(etag))
			Expect(updated.Spec.Template.Annotations[configMapAnnotationKey]).To(Equal(etag))
		})

		It("reconciles a statefulset consuming the configmap via init container", func() {
			ss := newStatefulSetWithInitContainer("db-init", true, consumesConfigMap, configMapName)
			reconciler := newReconciler(newTargetConfigMap(), ss)

			affected, err := reconciler.PropagateSecretToWorkloads(ctx, configMapTarget, etag)
			Expect(err).NotTo(HaveOccurred())
			Expect(affected).To(Equal(1))

			updated := &appsv1.StatefulSet{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "db-init", Namespace: namespace}, updated)).To(Succeed())
			Expect(updated.Annotations[configMapAnnotationKey]).To(Equal(etag))
			Expect(updated.Spec.Template.Annotations[configMapAnnotationKey]).To(Equal(etag))
		})

		It("skips a deployment consuming a secret but not the configmap", func() {
			dep := newDeployment("secret-only", true, consumesSecret, configMapName)
			reconciler := newReconciler(newTargetConfigMap(), dep)

			affected, err := reconciler.PropagateSecretToWorkloads(ctx, configMapTarget, etag)
			Expect(err).NotTo(HaveOccurred())
			Expect(affected).To(Equal(0))

			updated := &appsv1.Deployment{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: "secret-only", Namespace: namespace}, updated)).To(Succeed())
			Expect(updated.Annotations).NotTo(HaveKey(configMapAnnotationKey))
		})

	})
})

// newStaleCacheReconciler builds a reconciler where Get returns NotFound for
// a specific namespaced name, simulating the informer cache lag that occurs
// in a real cluster when a resource is created via the API server but the
// watch event has not yet reached the local cache.
func newStaleCacheReconciler(staleNN types.NamespacedName, objs ...client.Object) *svc.InfisicalStaticSecretReconciler {
	s := newScheme()
	underlying := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()

	wrapped := interceptor.NewClient(underlying, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if key == staleNN {
				return k8Errors.NewNotFound(schema.GroupResource{Resource: "secrets"}, key.Name)
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})

	return svc.NewReconcilerForTest(wrapped, s)
}

var _ = Describe("Cache lag bug: SyncKubeSecret then PropagateSecretToWorkloads", func() {
	ctx := context.Background()

	const namespace = "default"
	const secretName = "new-target"

	data := map[string][]byte{
		"KEY": []byte("value"),
	}

	target := v1beta1.SecretTarget{
		Name:           secretName,
		Namespace:      namespace,
		Kind:           v1beta1.SecretTargetKindSecret,
		SecretType:     corev1.SecretTypeOpaque,
		CreationPolicy: v1beta1.CreationPolicyOrphan,
	}

	It("propagates to workloads even when the informer cache has not caught up", func() {
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
									{SecretRef: &corev1.SecretEnvSource{
										LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
									}},
								},
							},
						},
					},
				},
			},
		}

		staleNN := types.NamespacedName{Name: secretName, Namespace: namespace}
		reconciler := newStaleCacheReconciler(staleNN, dep)
		owner := newStaticSecret()

		// Step 1: SyncKubeSecret creates the target Secret. The Create call
		// bypasses the interceptor (only Get is intercepted), so this succeeds.
		changed, newEtag, err := reconciler.SyncKubeSecret(ctx, owner, data, target)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())

		// Step 2: PropagateSecretToWorkloads should succeed even though the
		// informer cache has not caught up with the newly created Secret.
		affected, err := reconciler.PropagateSecretToWorkloads(ctx, target, newEtag)
		Expect(err).NotTo(HaveOccurred())
		Expect(affected).To(Equal(1))
	})
})
