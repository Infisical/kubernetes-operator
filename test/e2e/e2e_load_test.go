package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/services/infisicalstaticsecret"
	"github.com/Infisical/infisical/k8-operator/internal/testutil/infra"
	"github.com/Infisical/infisical/k8-operator/internal/util"
)

// loadStaticSecrets is how many InfisicalStaticSecrets the load test creates at once.
// It is well above staticSecretWorkers so the workqueue stays saturated.
const loadStaticSecrets = 20

var _ = Describe("InfisicalStaticSecret load", Ordered, func() {
	var (
		ctx     context.Context
		api     *infra.NodeJSService
		k       client.Client
		project *infra.ProjectSeed
		authRef secretsv1beta1.NamespacedName
	)

	BeforeAll(func() {
		ctx = context.Background()
		api = testInfra.NodeJS()
		k = testManager.Client()

		project = api.CreateProject(GinkgoT(), "load")
		DeferCleanup(func() { api.DeleteProject(GinkgoT(), project.ID) })

		identity := api.CreateIdentity(GinkgoT(), "load-identity")
		DeferCleanup(func() { api.DeleteIdentity(GinkgoT(), identity.ID) })

		api.AddIdentityToProject(GinkgoT(), project.ID, identity.ID, infra.Role("admin"))
		creds := api.SetupUniversalAuth(GinkgoT(), identity.ID)

		credentialSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "e2e-load-credentials", Namespace: testNamespace},
			StringData: map[string]string{"clientId": creds.ClientID, "clientSecret": creds.ClientSecret},
		}
		Expect(k.Create(ctx, credentialSecret)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, credentialSecret)) })

		// All static secrets share this auth (on the Helm-installed default connection),
		// so the first reconciles race to log in with it.
		auth := &secretsv1beta1.InfisicalAuth{
			ObjectMeta: metav1.ObjectMeta{Name: "e2e-load-auth", Namespace: testNamespace},
			Spec: secretsv1beta1.InfisicalAuthSpec{
				Method: secretsv1beta1.UniversalAuth,
				Universal: &secretsv1beta1.UniversalAuthConfig{
					ClientIdRef:     secretsv1beta1.SecretReference{Name: credentialSecret.Name, Namespace: testNamespace, Key: "clientId"},
					ClientSecretRef: secretsv1beta1.SecretReference{Name: credentialSecret.Name, Namespace: testNamespace, Key: "clientSecret"},
				},
			},
		}
		Expect(k.Create(ctx, auth)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, auth)) })
		authRef = secretsv1beta1.NamespacedName{Name: auth.Name, Namespace: auth.Namespace}

		By("waiting for InfisicalAuth to become ready")
		Eventually(func(g Gomega) {
			var a secretsv1beta1.InfisicalAuth
			g.Expect(k.Get(ctx, types.NamespacedName{Name: auth.Name, Namespace: auth.Namespace}, &a)).To(Succeed())
			cond := meta.FindStatusCondition(a.Status.Conditions, "secrets.infisical.com/IsReady")
			g.Expect(cond).NotTo(BeNil(), "InfisicalAuth has no IsReady condition yet")
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue), "InfisicalAuth not ready: %s", cond.Message)
		}).WithTimeout(60 * time.Second).WithPolling(time.Second).Should(Succeed())
	})

	It("syncs many static secrets feeding the same auto-reload Deployment", func() {
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/", "LOAD_KEY", "load-value", nil)

		targetName := func(i int) string { return fmt.Sprintf("e2e-load-target-%d", i) }

		By("creating a Deployment that consumes every target secret")
		// Each static secret propagates its target to this Deployment, so concurrent reconciles
		// update the same object and conflict. Zero replicas: only the annotations matter.
		envFrom := make([]corev1.EnvFromSource, loadStaticSecrets)
		for i := range loadStaticSecrets {
			envFrom[i] = corev1.EnvFromSource{SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: targetName(i)},
				Optional:             ptr.To(true),
			}}
		}
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "e2e-load-workload",
				Namespace:   testNamespace,
				Annotations: map[string]string{infisicalstaticsecret.AutoReloadAnnotation: "true"},
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: ptr.To[int32](0),
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "e2e-load-workload"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "e2e-load-workload"}},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "main", Image: "busybox", EnvFrom: envFrom}},
					},
				},
			},
		}
		Expect(k.Create(ctx, deployment)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, deployment)) })

		By(fmt.Sprintf("creating %d static secrets at once", loadStaticSecrets))
		for i := range loadStaticSecrets {
			ss := &secretsv1beta1.InfisicalStaticSecret{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("e2e-load-%d", i), Namespace: testNamespace},
				Spec: secretsv1beta1.InfisicalStaticSecretSpec{
					InfisicalAuthRef: authRef,
					SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
					Sources: []secretsv1beta1.SecretSource{{
						ProjectId:       project.ID,
						EnvironmentSlug: project.EnvSlug,
						SecretPath:      "/",
					}},
					Targets: []secretsv1beta1.SecretTarget{{
						Name:           targetName(i),
						Namespace:      testNamespace,
						Kind:           secretsv1beta1.SecretTargetKindSecret,
						SecretType:     corev1.SecretTypeOpaque,
						CreationPolicy: secretsv1beta1.CreationPolicyOwner,
					}},
				},
			}
			Expect(k.Create(ctx, ss)).To(Succeed())
			DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, ss)) })
		}

		By("waiting for every target secret to sync")
		Eventually(func(g Gomega) {
			for i := range loadStaticSecrets {
				var secret corev1.Secret
				g.Expect(k.Get(ctx, types.NamespacedName{Name: targetName(i), Namespace: testNamespace}, &secret)).To(Succeed())
				g.Expect(secret.Data).To(HaveKeyWithValue("LOAD_KEY", []byte("load-value")), "target %s", targetName(i))
			}
		}).WithTimeout(2 * time.Minute).WithPolling(2 * time.Second).Should(Succeed())

		By("checking every static secret reconciled successfully")
		Eventually(func(g Gomega) {
			for i := range loadStaticSecrets {
				var ss secretsv1beta1.InfisicalStaticSecret
				g.Expect(k.Get(ctx, types.NamespacedName{Name: fmt.Sprintf("e2e-load-%d", i), Namespace: testNamespace}, &ss)).To(Succeed())
				cond := meta.FindStatusCondition(ss.Status.Conditions, "secrets.infisical.com/LastReconcileStatus")
				g.Expect(cond).NotTo(BeNil(), "static secret %d has no LastReconcileStatus yet", i)
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue), "static secret %d failed: %s", i, cond.Message)
			}
		}).WithTimeout(time.Minute).WithPolling(2 * time.Second).Should(Succeed())

		By("checking the Deployment received every target's reload annotation despite concurrent updates")
		Eventually(func(g Gomega) {
			var d appsv1.Deployment
			g.Expect(k.Get(ctx, client.ObjectKeyFromObject(deployment), &d)).To(Succeed())
			for i := range loadStaticSecrets {
				key := util.ComputeManagedSecretAnnotation(targetName(i))
				g.Expect(d.Annotations).To(HaveKey(key), "missing annotation for %s", targetName(i))
				g.Expect(d.Spec.Template.Annotations).To(HaveKey(key), "missing template annotation for %s", targetName(i))
			}
		}).WithTimeout(time.Minute).WithPolling(2 * time.Second).Should(Succeed())
	})
})
