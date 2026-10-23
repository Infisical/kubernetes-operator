package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/testutil/infra"
)

var _ = Describe("InfisicalStaticSecret metrics", Ordered, ContinueOnFailure, func() {
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

		project = api.CreateProject(GinkgoT(), "static-secret-metrics")
		DeferCleanup(func() { api.DeleteProject(GinkgoT(), project.ID) })

		identity := api.CreateIdentity(GinkgoT(), "metrics-identity")
		DeferCleanup(func() { api.DeleteIdentity(GinkgoT(), identity.ID) })

		api.AddIdentityToProject(GinkgoT(), project.ID, identity.ID, infra.Role("admin"))
		creds := api.SetupUniversalAuth(GinkgoT(), identity.ID)

		credentialSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e2e-metrics-credentials",
				Namespace: testNamespace,
			},
			StringData: map[string]string{
				"clientId":     creds.ClientID,
				"clientSecret": creds.ClientSecret,
			},
		}
		Expect(k.Create(ctx, credentialSecret)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, credentialSecret)) })

		connection := &secretsv1beta1.InfisicalConnection{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e2e-metrics-connection",
				Namespace: testNamespace,
			},
			Spec: secretsv1beta1.InfisicalConnectionSpec{
				Address: testManager.InClusterAPIURL(),
			},
		}
		Expect(k.Create(ctx, connection)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, connection)) })

		auth := &secretsv1beta1.InfisicalAuth{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e2e-metrics-auth",
				Namespace: testNamespace,
			},
			Spec: secretsv1beta1.InfisicalAuthSpec{
				InfisicalConnectionRef: secretsv1beta1.NamespacedName{
					Name:      connection.Name,
					Namespace: connection.Namespace,
				},
				Method: secretsv1beta1.UniversalAuth,
				Universal: &secretsv1beta1.UniversalAuthConfig{
					ClientIdRef: secretsv1beta1.SecretReference{
						Name:      credentialSecret.Name,
						Namespace: credentialSecret.Namespace,
						Key:       "clientId",
					},
					ClientSecretRef: secretsv1beta1.SecretReference{
						Name:      credentialSecret.Name,
						Namespace: credentialSecret.Namespace,
						Key:       "clientSecret",
					},
				},
			},
		}
		Expect(k.Create(ctx, auth)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, auth)) })

		authRef = secretsv1beta1.NamespacedName{
			Name:      auth.Name,
			Namespace: auth.Namespace,
		}

		By("waiting for InfisicalConnection to become ready")
		Eventually(func(g Gomega) {
			var conn secretsv1beta1.InfisicalConnection
			g.Expect(k.Get(ctx, types.NamespacedName{Name: connection.Name, Namespace: connection.Namespace}, &conn)).To(Succeed())
			cond := meta.FindStatusCondition(conn.Status.Conditions, "secrets.infisical.com/IsReady")
			g.Expect(cond).NotTo(BeNil(), "InfisicalConnection has no IsReady condition yet")
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue),
				"InfisicalConnection not ready: %s", cond.Message)
		}).WithTimeout(60 * time.Second).WithPolling(time.Second).Should(Succeed())

		By("waiting for InfisicalAuth to become ready")
		Eventually(func(g Gomega) {
			var a secretsv1beta1.InfisicalAuth
			g.Expect(k.Get(ctx, types.NamespacedName{Name: auth.Name, Namespace: auth.Namespace}, &a)).To(Succeed())
			cond := meta.FindStatusCondition(a.Status.Conditions, "secrets.infisical.com/IsReady")
			g.Expect(cond).NotTo(BeNil(), "InfisicalAuth has no IsReady condition yet")
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue),
				"InfisicalAuth not ready: %s", cond.Message)
		}).WithTimeout(60 * time.Second).WithPolling(time.Second).Should(Succeed())
	})

	createStaticSecret := func(name string, spec secretsv1beta1.InfisicalStaticSecretSpec) *secretsv1beta1.InfisicalStaticSecret {
		GinkgoHelper()
		ss := &secretsv1beta1.InfisicalStaticSecret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNamespace,
			},
			Spec: spec,
		}
		Expect(k.Create(ctx, ss)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, ss)) })
		return ss
	}

	It("should expose Prometheus metrics for static secrets", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "metrics")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/metrics", "METRICS_KEY", "metrics-value", nil)

		// A workload opted into auto-reload that consumes the healthy secret's target.
		replicas := int32(1)
		consumer := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "e2e-metrics-consumer",
				Namespace:   testNamespace,
				Annotations: map[string]string{"secrets.infisical.com/auto-reload": "true"},
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "e2e-metrics-consumer"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "e2e-metrics-consumer"}},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{
							Name:  "main",
							Image: "busybox",
							EnvFrom: []corev1.EnvFromSource{{
								SecretRef: &corev1.SecretEnvSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: "e2e-metrics-synced"},
								},
							}},
						}},
					},
				},
			},
		}
		Expect(k.Create(ctx, consumer)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, consumer)) })

		// A short refresh interval so that a change in Infisical is synced within the waits below.
		healthy := createStaticSecret("e2e-metrics-healthy", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "5s"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: project.EnvSlug,
				SecretPath:      "/metrics",
			}},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           "e2e-metrics-synced",
				Namespace:      testNamespace,
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				SecretType:     corev1.SecretTypeOpaque,
				CreationPolicy: secretsv1beta1.CreationPolicyOwner,
			}},
		})
		broken := createStaticSecret("e2e-metrics-broken", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: "does-not-exist",
				SecretPath:      "/",
			}},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           "e2e-metrics-broken-target",
				Namespace:      testNamespace,
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				SecretType:     corev1.SecretTypeOpaque,
				CreationPolicy: secretsv1beta1.CreationPolicyOwner,
			}},
		})

		Eventually(func(g Gomega) {
			var synced corev1.Secret
			g.Expect(k.Get(ctx, types.NamespacedName{Name: "e2e-metrics-synced", Namespace: testNamespace}, &synced)).To(Succeed())
			g.Expect(synced.Data).To(HaveKeyWithValue("METRICS_KEY", []byte("metrics-value")))
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			var ss secretsv1beta1.InfisicalStaticSecret
			g.Expect(k.Get(ctx, types.NamespacedName{Name: broken.Name, Namespace: testNamespace}, &ss)).To(Succeed())
			cond := meta.FindStatusCondition(ss.Status.Conditions, "secrets.infisical.com/LastReconcileStatus")
			g.Expect(cond).NotTo(BeNil(), "InfisicalStaticSecret has no LastReconcileStatus condition yet")
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())

		clientset, err := kubernetes.NewForConfig(ctrl.GetConfigOrDie())
		Expect(err).NotTo(HaveOccurred())

		// scrapeMetrics returns the operator's infisical_* metric lines. It reads /metrics through
		// the API server's service proxy, which needs no port-forward and accepts the endpoint's
		// self-signed certificate. Labels are rendered in alphabetical order.
		scrapeMetrics := func(g Gomega) []string {
			var services corev1.ServiceList
			g.Expect(k.List(ctx, &services, client.InNamespace(operatorNamespace))).To(Succeed())
			metricsService := ""
			for _, service := range services.Items {
				if strings.HasSuffix(service.Name, "-controller-manager-metrics-service") {
					metricsService = service.Name
				}
			}
			g.Expect(metricsService).NotTo(BeEmpty(), "operator metrics service not found")

			raw, err := clientset.CoreV1().RESTClient().Get().
				Namespace(operatorNamespace).
				Resource("services").
				Name("https:" + metricsService + ":https").
				SubResource("proxy").
				Suffix("metrics").
				DoRaw(ctx)
			g.Expect(err).NotTo(HaveOccurred())

			var lines []string
			for _, line := range strings.Split(string(raw), "\n") {
				if strings.HasPrefix(line, "infisical_") {
					lines = append(lines, line)
				}
			}
			return lines
		}

		// seriesValue returns the value of a series in scraped lines, failing g when it is missing.
		seriesValue := func(g Gomega, lines []string, series string) float64 {
			for _, line := range lines {
				if value, found := strings.CutPrefix(line, series+" "); found {
					var parsed float64
					_, err := fmt.Sscan(value, &parsed)
					g.Expect(err).NotTo(HaveOccurred())
					return parsed
				}
			}
			g.Expect(lines).To(ContainElement(HavePrefix(series + " ")))
			return 0
		}

		healthyLabels := fmt.Sprintf(`name=%q,namespace=%q`, healthy.Name, testNamespace)
		brokenLabels := fmt.Sprintf(`name=%q,namespace=%q`, broken.Name, testNamespace)
		healthyRedeploys := "infisical_static_secret_workload_redeploys_total{" + healthyLabels + "}"
		var redeploysBefore float64

		By("scraping the operator's /metrics endpoint")
		Eventually(func(g Gomega) {
			// Compare with the API server's view rather than fixed numbers, since resources
			// from earlier specs may still be going away.
			var list secretsv1beta1.InfisicalStaticSecretList
			g.Expect(k.List(ctx, &list, client.InNamespace(testNamespace))).To(Succeed())
			failed := 0
			for _, ss := range list.Items {
				cond := meta.FindStatusCondition(ss.Status.Conditions, "secrets.infisical.com/LastReconcileStatus")
				if cond != nil && cond.Status == metav1.ConditionFalse {
					failed++
				}
			}

			lines := scrapeMetrics(g)
			resources, failedResources := 0, 0
			for _, line := range lines {
				if strings.HasPrefix(line, "infisical_static_secrets{") && strings.Contains(line, fmt.Sprintf(`namespace=%q`, testNamespace)) {
					resources++
					if strings.Contains(line, `status="failed"`) {
						failedResources++
					}
				}
			}
			g.Expect(resources).To(Equal(len(list.Items)), "expected one infisical_static_secrets series per resource")
			g.Expect(failedResources).To(Equal(failed))

			g.Expect(lines).To(ContainElements(
				fmt.Sprintf(`infisical_static_secrets{instant_updates="false",%s,status="synced"} 1`, healthyLabels),
				fmt.Sprintf(`infisical_static_secrets{instant_updates="false",%s,status="failed"} 1`, brokenLabels),
				fmt.Sprintf("infisical_static_secret_refresh_interval_seconds{%s} 5", healthyLabels),
				// The operator's cache may not list the consumer yet at the first sync; a later sync counts it.
				fmt.Sprintf("infisical_static_secret_auto_reload_workloads{%s} 1", healthyLabels),
			))
			// The auto-reload count is recorded only after a complete sync, which the broken secret never had.
			g.Expect(lines).NotTo(ContainElement(HavePrefix("infisical_static_secret_auto_reload_workloads{" + brokenLabels + "}")))
			// The first sync may or may not have redeployed the consumer, depending on the cache.
			redeploysBefore = seriesValue(g, lines, healthyRedeploys)
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())

		By("changing the secret in Infisical and watching the redeploy counter go up")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/metrics", "ROTATED_KEY", "rotated-value", nil)
		Eventually(func(g Gomega) {
			g.Expect(seriesValue(g, scrapeMetrics(g), healthyRedeploys)).To(Equal(redeploysBefore + 1))
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())

		// setAutoReload turns auto-reload on or off for the consumer. It retries because the
		// Deployment controller may update the Deployment between the get and the update.
		setAutoReload := func(enabled bool) {
			Eventually(func(g Gomega) {
				var deployment appsv1.Deployment
				g.Expect(k.Get(ctx, client.ObjectKeyFromObject(consumer), &deployment)).To(Succeed())
				if enabled {
					if deployment.Annotations == nil {
						deployment.Annotations = map[string]string{}
					}
					deployment.Annotations["secrets.infisical.com/auto-reload"] = "true"
				} else {
					delete(deployment.Annotations, "secrets.infisical.com/auto-reload")
				}
				g.Expect(k.Update(ctx, &deployment)).To(Succeed())
			}).WithTimeout(10 * time.Second).WithPolling(time.Second).Should(Succeed())
		}
		healthyAutoReload := "infisical_static_secret_auto_reload_workloads{" + healthyLabels + "}"

		By("turning auto-reload off for the consumer and watching the gauge drop on a later sync")
		setAutoReload(false)
		Eventually(func(g Gomega) {
			g.Expect(seriesValue(g, scrapeMetrics(g), healthyAutoReload)).To(Equal(0.0))
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())

		By("turning auto-reload back on and watching the gauge come back")
		setAutoReload(true)
		Eventually(func(g Gomega) {
			g.Expect(seriesValue(g, scrapeMetrics(g), healthyAutoReload)).To(Equal(1.0))
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())

		By("fixing the broken secret and watching it leave the failed state")
		Expect(k.Get(ctx, types.NamespacedName{Name: broken.Name, Namespace: testNamespace}, broken)).To(Succeed())
		broken.Spec.Sources[0].EnvironmentSlug = project.EnvSlug
		broken.Spec.Sources[0].SecretPath = "/metrics"
		Expect(k.Update(ctx, broken)).To(Succeed())
		Eventually(func(g Gomega) {
			lines := scrapeMetrics(g)
			g.Expect(lines).To(ContainElements(
				fmt.Sprintf(`infisical_static_secrets{instant_updates="false",%s,status="synced"} 1`, brokenLabels),
				fmt.Sprintf("infisical_static_secret_auto_reload_workloads{%s} 0", brokenLabels),
			))
			g.Expect(lines).NotTo(ContainElement(ContainSubstring(brokenLabels + `,status="failed"`)))
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())

		By("deleting a secret and watching its series disappear")
		Expect(k.Delete(ctx, healthy)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(scrapeMetrics(g)).NotTo(ContainElement(ContainSubstring(healthyLabels)))
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())
	})
})
