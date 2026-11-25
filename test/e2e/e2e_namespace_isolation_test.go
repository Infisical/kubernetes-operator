package e2e

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/testutil/infra"
)

// The suite installs the operator with namespace isolation off. These specs show a resource
// using another namespace's credentials first, then turn isolation on over the existing
// resources, and turn it off again at the end.
var _ = Describe("Namespace isolation", Ordered, ContinueOnFailure, func() {
	const (
		teamA          = "isolation-team-a"
		teamB          = "isolation-team-b"
		credentials    = "e2e-ua-credentials"
		connectionName = "e2e-connection"
		authName       = "e2e-auth"
	)

	var (
		ctx     context.Context
		api     *infra.NodeJSService
		k       client.Client
		project *infra.ProjectSeed
		creds   *infra.UniversalAuthCredentials
	)

	ref := func(namespace, name string) secretsv1beta1.NamespacedName {
		return secretsv1beta1.NamespacedName{Name: name, Namespace: namespace}
	}

	universalAuth := func(namespace, name, credentialsNamespace string, connectionRef secretsv1beta1.NamespacedName) *secretsv1beta1.InfisicalAuth {
		return &secretsv1beta1.InfisicalAuth{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: secretsv1beta1.InfisicalAuthSpec{
				InfisicalConnectionRef: connectionRef,
				Method:                 secretsv1beta1.UniversalAuth,
				Universal: &secretsv1beta1.UniversalAuthConfig{
					ClientIdRef:     secretsv1beta1.SecretReference{Name: credentials, Namespace: credentialsNamespace, Key: "clientId"},
					ClientSecretRef: secretsv1beta1.SecretReference{Name: credentials, Namespace: credentialsNamespace, Key: "clientSecret"},
				},
			},
		}
	}

	staticSecret := func(namespace, name string, authRef secretsv1beta1.NamespacedName, targetNamespaces ...string) *secretsv1beta1.InfisicalStaticSecret {
		ss := &secretsv1beta1.InfisicalStaticSecret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: secretsv1beta1.InfisicalStaticSecretSpec{
				InfisicalAuthRef: authRef,
				SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
				Sources:          []secretsv1beta1.SecretSource{{ProjectId: project.ID, EnvironmentSlug: project.EnvSlug, SecretPath: "/"}},
			},
		}
		for _, targetNamespace := range targetNamespaces {
			ss.Spec.Targets = append(ss.Spec.Targets, secretsv1beta1.SecretTarget{
				Name:           name,
				Namespace:      targetNamespace,
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				CreationPolicy: secretsv1beta1.CreationPolicyOrphan,
			})
		}
		return ss
	}

	credentialsSecret := func(namespace string) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: credentials, Namespace: namespace},
			StringData: map[string]string{"clientId": creds.ClientID, "clientSecret": creds.ClientSecret},
		}
	}

	create := func(obj client.Object) {
		GinkgoHelper()
		Expect(k.Create(ctx, obj)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, obj)) })
	}

	// expectCondition waits until the resource has the condition with the status, and a
	// message containing the given text.
	expectCondition := func(obj client.Object, conditions func() []metav1.Condition, condType string, status metav1.ConditionStatus, message string) {
		GinkgoHelper()
		Eventually(func(g Gomega) {
			g.Expect(k.Get(ctx, client.ObjectKeyFromObject(obj), obj)).To(Succeed())
			cond := meta.FindStatusCondition(conditions(), condType)
			g.Expect(cond).NotTo(BeNil(), "%s has no %s condition yet", obj.GetName(), condType)
			g.Expect(cond.Status).To(Equal(status), "%s: %s", obj.GetName(), cond.Message)
			g.Expect(cond.Message).To(ContainSubstring(message))
		}).WithTimeout(60 * time.Second).WithPolling(time.Second).Should(Succeed())
	}

	expectAuthReady := func(auth *secretsv1beta1.InfisicalAuth) {
		GinkgoHelper()
		expectCondition(auth, func() []metav1.Condition { return auth.Status.Conditions }, "secrets.infisical.com/IsReady", metav1.ConditionTrue, "")
	}

	expectAuthRejected := func(auth *secretsv1beta1.InfisicalAuth, message string) {
		GinkgoHelper()
		expectCondition(auth, func() []metav1.Condition { return auth.Status.Conditions }, "secrets.infisical.com/IsReady", metav1.ConditionFalse, message)
	}

	expectStaticSecretRejected := func(ss *secretsv1beta1.InfisicalStaticSecret, message string) {
		GinkgoHelper()
		expectCondition(ss, func() []metav1.Condition { return ss.Status.Conditions }, "secrets.infisical.com/LastReconcileStatus", metav1.ConditionFalse, message)
	}

	secretData := func(namespace, name string) (map[string][]byte, error) {
		var secret corev1.Secret
		err := k.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &secret)
		return secret.Data, err
	}

	expectSynced := func(namespace, name, key string) {
		GinkgoHelper()
		Eventually(func(g Gomega) {
			data, err := secretData(namespace, name)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(data).To(HaveKey(key))
		}).WithTimeout(60 * time.Second).WithPolling(time.Second).Should(Succeed())
	}

	expectNoSecret := func(namespace, name string) {
		GinkgoHelper()
		_, err := secretData(namespace, name)
		Expect(k8sErrors.IsNotFound(err)).To(BeTrue(), "Secret %s/%s exists, or getting it failed: %v", namespace, name, err)
	}

	authInTeamA := &secretsv1beta1.InfisicalAuth{}
	authUsingTeamBCredentials := &secretsv1beta1.InfisicalAuth{}
	syncUsingTeamBAuth := &secretsv1beta1.InfisicalStaticSecret{}

	BeforeAll(func() {
		ctx = context.Background()
		api = testInfra.NodeJS()
		k = testManager.Client()

		for _, namespace := range []string{teamA, teamB} {
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			Expect(k.Create(ctx, ns)).To(Succeed())
			// Waits for the deletion, so the operator is still installed to finalize what's in it.
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k.Delete(ctx, ns))).To(Succeed())
				Eventually(func() bool {
					return k8sErrors.IsNotFound(k.Get(ctx, client.ObjectKeyFromObject(ns), &corev1.Namespace{}))
				}).WithTimeout(2*time.Minute).WithPolling(time.Second).Should(BeTrue(), "namespace %s wasn't deleted", namespace)
			})
		}
		// Registered after the namespaces, so it runs before they're deleted.
		DeferCleanup(func() { Expect(testManager.SetNamespaceIsolation(false)).To(Succeed()) })

		project = api.CreateProject(GinkgoT(), "namespace-isolation")
		DeferCleanup(func() { api.DeleteProject(GinkgoT(), project.ID) })
		identity := api.CreateIdentity(GinkgoT(), "namespace-isolation-identity")
		DeferCleanup(func() { api.DeleteIdentity(GinkgoT(), identity.ID) })
		api.AddIdentityToProject(GinkgoT(), project.ID, identity.ID, infra.Role("admin"))
		creds = api.SetupUniversalAuth(GinkgoT(), identity.ID)
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/", "API_KEY", "team-secret", nil)

		for _, namespace := range []string{teamA, teamB} {
			create(credentialsSecret(namespace))
			create(&secretsv1beta1.InfisicalConnection{
				ObjectMeta: metav1.ObjectMeta{Name: connectionName, Namespace: namespace},
				Spec:       secretsv1beta1.InfisicalConnectionSpec{Address: testManager.InClusterAPIURL()},
			})
			create(universalAuth(namespace, authName, namespace, ref(namespace, connectionName)))
		}
		create(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "team-b-private", Namespace: teamB},
			StringData: map[string]string{"TEAM_B_PRIVATE": "private-value"},
		})

		*authInTeamA = *universalAuth(teamA, authName, teamA, ref(teamA, connectionName))
		expectAuthReady(authInTeamA)
	})

	It("lets team-a use team-b's credentials while isolation is off", func() {
		// Not created with create: the later specs use them, and deleting team-a cleans them up.
		*authUsingTeamBCredentials = *universalAuth(teamA, "e2e-auth-team-b-credentials", teamB, ref(teamA, connectionName))
		Expect(k.Create(ctx, authUsingTeamBCredentials)).To(Succeed())
		expectAuthReady(authUsingTeamBCredentials)

		*syncUsingTeamBAuth = *staticSecret(teamA, "e2e-sync-team-b-auth", ref(teamB, authName), teamA)
		Expect(k.Create(ctx, syncUsingTeamBAuth)).To(Succeed())
		expectSynced(teamA, syncUsingTeamBAuth.Name, "API_KEY")
	})

	It("restarts the operator with isolation on", func() {
		// The resources above don't resync for an hour, so ADDED_LATER only reaches their
		// targets if the restarted operator still syncs them.
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/", "ADDED_LATER", "new-value", nil)

		Expect(testManager.SetNamespaceIsolation(true)).To(Succeed())
	})

	It("keeps syncing resources that only reference their own namespace", func() {
		expectAuthReady(authInTeamA)

		create(staticSecret(teamA, "e2e-sync-same-namespace", ref(teamA, authName), teamA))
		expectSynced(teamA, "e2e-sync-same-namespace", "ADDED_LATER")
	})

	It("marks an InfisicalAuth that reads team-b's credentials as not ready, although it was ready before", func() {
		expectAuthRejected(authUsingTeamBCredentials, `the Secret "e2e-ua-credentials" must be in this resource's namespace "isolation-team-a"`)
	})

	It("stops syncing with team-b's InfisicalAuth, although it synced before", func() {
		expectStaticSecretRejected(syncUsingTeamBAuth, ".spec.infisicalAuthRef must be in this resource's namespace")

		data, err := secretData(teamA, syncUsingTeamBAuth.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKey("API_KEY"))
		Expect(data).NotTo(HaveKey("ADDED_LATER"))
	})

	It("rejects an InfisicalAuth that uses team-b's ServiceAccount", func() {
		auth := &secretsv1beta1.InfisicalAuth{
			ObjectMeta: metav1.ObjectMeta{Name: "e2e-auth-team-b-service-account", Namespace: teamA},
			Spec: secretsv1beta1.InfisicalAuthSpec{
				InfisicalConnectionRef: ref(teamA, connectionName),
				Method:                 secretsv1beta1.KubernetesAuth,
				Kubernetes: &secretsv1beta1.KubernetesAuthConfig{
					IdentityIDRef:     secretsv1beta1.SecretReference{Name: credentials, Namespace: teamA, Key: "clientId"},
					ServiceAccountRef: ref(teamB, "default"),
				},
			},
		}
		create(auth)
		expectAuthRejected(auth, ".spec.kubernetes.serviceAccountRef must be in this resource's namespace")
	})

	It("still lets resources in the operator namespace use the default InfisicalConnection", func() {
		create(credentialsSecret(operatorNamespace))
		auth := universalAuth(operatorNamespace, "e2e-auth-default-connection", operatorNamespace, secretsv1beta1.NamespacedName{})
		create(auth)
		expectAuthReady(auth)

		create(staticSecret(operatorNamespace, "e2e-sync-operator-namespace", ref(operatorNamespace, auth.Name), operatorNamespace))
		expectSynced(operatorNamespace, "e2e-sync-operator-namespace", "API_KEY")
	})

	It("writes no target when one of them is in team-b", func() {
		ss := staticSecret(teamA, "e2e-sync-write-team-b", ref(teamA, authName), teamA, teamB)
		create(ss)
		expectStaticSecretRejected(ss, "targets[1].namespace must be in this resource's namespace")
		expectNoSecret(teamA, ss.Name)
		expectNoSecret(teamB, ss.Name)
	})

	It("doesn't push a Secret of team-b", func() {
		push := &secretsv1beta1.InfisicalStaticSecretPush{
			ObjectMeta: metav1.ObjectMeta{Name: "e2e-push-team-b-secret", Namespace: teamA},
			Spec: secretsv1beta1.InfisicalStaticSecretPushSpec{
				InfisicalAuthRef: ref(teamA, authName),
				Sources:          secretsv1beta1.PushSources{Secrets: []secretsv1beta1.NamespacedName{ref(teamB, "team-b-private")}},
				Targets:          []secretsv1beta1.PushTarget{{ProjectId: project.ID, EnvironmentSlug: project.EnvSlug, SecretPath: "/pushed"}},
			},
		}
		create(push)
		expectCondition(push, func() []metav1.Condition { return push.Status.Conditions },
			"secrets.infisical.com/LastReconcileStatus", metav1.ConditionFalse, "sources.secrets[0].namespace must be in this resource's namespace")
		Expect(api.ListSecrets(GinkgoT(), project.ID, project.EnvSlug, "/pushed")).To(BeEmpty())
	})

	It("restores the previous behavior when isolation is turned off again", func() {
		Expect(testManager.SetNamespaceIsolation(false)).To(Succeed())

		expectAuthReady(authUsingTeamBCredentials)
		expectSynced(teamA, syncUsingTeamBAuth.Name, "ADDED_LATER")
	})
})
