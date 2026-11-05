package infisicalstaticsecretpush

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/go-logr/logr"
	"github.com/go-resty/resty/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/api"
	"github.com/Infisical/infisical/k8-operator/internal/testutil/fakeinfisical"
)

var _ = Describe("planTarget", func() {
	existing := []api.Secret{
		{ID: "owned-same", SecretKey: "OWNED_SAME", SecretValue: "value"},
		{ID: "owned-changed", SecretKey: "OWNED_CHANGED", SecretValue: "old"},
		{ID: "foreign-same", SecretKey: "FOREIGN_SAME", SecretValue: "value"},
		{ID: "foreign-changed", SecretKey: "FOREIGN_CHANGED", SecretValue: "old"},
	}
	managed := map[string]string{"owned-same": "OWNED_SAME", "owned-changed": "OWNED_CHANGED"}
	desired := map[string]string{
		"NEW":             "value",
		"OWNED_SAME":      "value",
		"OWNED_CHANGED":   "new",
		"FOREIGN_SAME":    "value",
		"FOREIGN_CHANGED": "new",
	}

	upsertKeys := func(plan targetPlan) []string {
		var keys []string
		for _, upsert := range plan.upserts {
			keys = append(keys, upsert.SecretKey)
		}
		return keys
	}

	It("creates, updates and takes over keys with the Replace policy", func() {
		plan := planTarget(desired, existing, managed, v1beta1.PushUpdatePolicyReplace)

		Expect(upsertKeys(plan)).To(Equal([]string{"FOREIGN_CHANGED", "NEW", "OWNED_CHANGED"}))
		Expect(plan.owned).To(Equal(map[string]string{"owned-same": "OWNED_SAME", "foreign-same": "FOREIGN_SAME"}))
		Expect(plan.claims).To(Equal(map[string]string{
			"FOREIGN_CHANGED": "foreign-changed",
			"FOREIGN_SAME":    "foreign-same",
			"OWNED_CHANGED":   "owned-changed",
		}))
		Expect(plan.keptByPolicy).To(BeEmpty())
	})

	It("only creates missing keys with the None policy, keeping the ones it owns", func() {
		plan := planTarget(desired, existing, managed, v1beta1.PushUpdatePolicyNone)

		Expect(upsertKeys(plan)).To(Equal([]string{"NEW"}))
		Expect(plan.owned).To(Equal(map[string]string{"owned-same": "OWNED_SAME", "owned-changed": "OWNED_CHANGED"}))
		Expect(plan.claims).To(BeEmpty())
		Expect(plan.keptByPolicy).To(Equal([]string{"FOREIGN_CHANGED", "OWNED_CHANGED"}))
	})

	It("drops the overwrites and take-overs of secrets another resource owns", func() {
		plan := planTarget(desired, existing, managed, v1beta1.PushUpdatePolicyReplace)

		other := otherOwner{name: "default/other"}
		deleting := otherOwner{name: "default/deleting", deleting: true}
		conflicts := plan.dropClaimsOwnedByOthers(map[string]otherOwner{
			"foreign-changed": other,
			"foreign-same":    deleting,
			"unrelated":       other,
		})

		Expect(conflicts).To(Equal(map[string]otherOwner{"FOREIGN_CHANGED": other, "FOREIGN_SAME": deleting}))
		Expect(upsertKeys(plan)).To(Equal([]string{"NEW", "OWNED_CHANGED"}))
		Expect(plan.owned).To(Equal(map[string]string{"owned-same": "OWNED_SAME"}))
	})

	It("compares values the way Infisical stores them", func() {
		plan := planTarget(
			map[string]string{"PADDED": "  value  ", "NEWLINE": "value\n\n"},
			[]api.Secret{{ID: "1", SecretKey: "PADDED", SecretValue: "value"}, {ID: "2", SecretKey: "NEWLINE", SecretValue: "value\n"}},
			map[string]string{"1": "PADDED", "2": "NEWLINE"},
			v1beta1.PushUpdatePolicyReplace,
		)

		Expect(plan.upserts).To(BeEmpty())
		Expect(plan.owned).To(HaveLen(2))
	})
})

var _ = Describe("normalizeSecretValue", func() {
	DescribeTable("matches how Infisical stores values",
		func(input, stored string) {
			Expect(normalizeSecretValue(input)).To(Equal(stored))
		},
		Entry("plain", "value", "value"),
		Entry("surrounding spaces", "  value \t", "value"),
		Entry("trailing newline", "value\n", "value\n"),
		Entry("several trailing newlines", " value\n\n", "value\n"),
		Entry("only a newline", "\n", "\n"),
		Entry("empty", "", ""),
	)
})

var _ = Describe("normalizeSecretPath", func() {
	DescribeTable("returns the path without a trailing slash",
		func(input, expected string) {
			Expect(normalizeSecretPath(input)).To(Equal(expected))
		},
		Entry("root", "/", "/"),
		Entry("trailing slash", "/app/", "/app"),
		Entry("double slashes", "/app//nested", "/app/nested"),
	)
})

var _ = Describe("chunkByJSONSize", func() {
	It("keeps every batch under the limit and preserves order", func() {
		var secrets []api.UpsertSecret
		for i := range 10 {
			secrets = append(secrets, api.UpsertSecret{SecretKey: strings.Repeat("K", i+1), SecretValue: strings.Repeat("v", 40)})
		}

		batches := chunkByJSONSize(secrets, 200)

		Expect(len(batches)).To(BeNumerically(">", 1))
		var flattened []api.UpsertSecret
		for _, batch := range batches {
			Expect(batch).NotTo(BeEmpty())
			flattened = append(flattened, batch...)
		}
		Expect(flattened).To(Equal(secrets))
	})

	It("puts an item larger than the limit in its own batch", func() {
		secrets := []api.UpsertSecret{
			{SecretKey: "SMALL", SecretValue: "v"},
			{SecretKey: "LARGE", SecretValue: strings.Repeat("v", 500)},
			{SecretKey: "SMALL2", SecretValue: "v"},
		}

		batches := chunkByJSONSize(secrets, 100)

		Expect(batches).To(HaveLen(3))
		Expect(batches[1]).To(Equal(secrets[1:2]))
	})

	It("counts escaped characters", func() {
		// Every newline is encoded as two bytes, so these values are larger in JSON than in Go.
		secrets := []api.UpsertSecret{
			{SecretKey: "A", SecretValue: strings.Repeat("\n", 60)},
			{SecretKey: "B", SecretValue: strings.Repeat("\n", 60)},
		}

		Expect(chunkByJSONSize(secrets, 200)).To(HaveLen(2))
	})

	It("returns no batches for no items", func() {
		Expect(chunkByJSONSize([]api.UpsertSecret{}, 100)).To(BeEmpty())
	})
})

var _ = Describe("deleteSecrets", func() {
	const projectID, environment = "project-1", "dev"
	location := api.SecretLocation{ProjectID: projectID, Environment: environment, SecretPath: "/app"}

	var (
		server     *fakeinfisical.Server
		restClient *resty.Client
		reconciler *InfisicalStaticSecretPushReconciler
	)

	BeforeEach(func() {
		server = fakeinfisical.New([]string{projectID}, []string{environment})
		DeferCleanup(server.Close)
		restClient = resty.New().SetBaseURL(server.URL)
		reconciler = &InfisicalStaticSecretPushReconciler{logger: logr.Discard()}
	})

	It("retries with the secrets that still exist when one was deleted in the meantime", func() {
		firstID := server.CreateSecret(projectID, environment, "/app", "FIRST", "value")
		secondID := server.CreateSecret(projectID, environment, "/app", "SECOND", "value")
		listed := map[string]secretRef{
			firstID:  {location: location, key: "FIRST"},
			secondID: {location: location, key: "SECOND"},
		}
		server.DeleteSecret(secondID)

		failed, err := reconciler.deleteSecrets(restClient, map[string]string{firstID: "FIRST", secondID: "SECOND"}, listed, nil, noOtherOwners)

		Expect(err).NotTo(HaveOccurred())
		Expect(failed).To(BeEmpty())
		Expect(server.Secrets(projectID, environment, "/app")).To(BeEmpty())
	})

	It("looks up secrets that weren't listed and deletes them where they are now", func() {
		id := server.CreateSecret(projectID, environment, "/elsewhere", "KEY", "value")

		failed, err := reconciler.deleteSecrets(restClient, map[string]string{id: "KEY"}, nil, nil, noOtherOwners)

		Expect(err).NotTo(HaveOccurred())
		Expect(failed).To(BeEmpty())
		Expect(server.Secrets(projectID, environment, "/elsewhere")).To(BeEmpty())
		Expect(server.CountRequests("GET /api/v4/secrets/id/" + id)).To(Equal(1))
	})

	It("skips secrets that no longer exist", func() {
		failed, err := reconciler.deleteSecrets(restClient, map[string]string{"gone": "KEY"}, nil, nil, noOtherOwners)

		Expect(err).NotTo(HaveOccurred())
		Expect(failed).To(BeEmpty())
		Expect(server.CountRequests("DELETE /api/v4/secrets/batch")).To(Equal(0))
	})

	It("keeps the secrets it couldn't delete", func() {
		id := server.CreateSecret(projectID, environment, "/app", "KEY", "value")
		server.RequireApproval(projectID, environment, "/app")

		failed, err := reconciler.deleteSecrets(restClient, map[string]string{id: "KEY"}, nil, nil, noOtherOwners)

		Expect(err).To(MatchError(ContainSubstring("require approval")))
		Expect(failed).To(Equal(map[string]string{id: "KEY"}))
	})

	It("skips a folder and key the resource now owns under another ID", func() {
		id := server.CreateSecret(projectID, environment, "/app", "KEY", "value")
		keep := map[secretRef]struct{}{{location: location, key: "KEY"}: {}}

		failed, err := reconciler.deleteSecrets(restClient, map[string]string{"stale-id": "KEY"}, map[string]secretRef{"stale-id": {location: location, key: "KEY"}}, keep, noOtherOwners)

		Expect(err).NotTo(HaveOccurred())
		Expect(failed).To(BeEmpty())
		Expect(server.Secrets(projectID, environment, "/app")["KEY"].ID).To(Equal(id))
	})

	It("leaves a secret another resource owns to that resource", func() {
		id := server.CreateSecret(projectID, environment, "/app", "KEY", "value")
		owners := func() (map[string]otherOwner, error) {
			return map[string]otherOwner{id: {name: "default/other"}}, nil
		}

		failed, err := reconciler.deleteSecrets(restClient, map[string]string{id: "KEY"}, nil, nil, owners)

		Expect(err).NotTo(HaveOccurred())
		Expect(failed).To(BeEmpty())
		Expect(server.Secrets(projectID, environment, "/app")).To(HaveKey("KEY"))
		Expect(server.Requests()).To(BeEmpty())
	})
})

func noOtherOwners() (map[string]otherOwner, error) {
	return nil, nil
}

var _ = Describe("summarize", func() {
	It("names every item up to the limit", func() {
		Expect(summarize([]string{"A", "B"})).To(Equal("A, B"))
	})

	It("names the first items and counts the rest", func() {
		items := make([]string, 0, maxListedItems+2)
		for i := range maxListedItems + 2 {
			items = append(items, fmt.Sprintf("K%d", i))
		}

		Expect(summarize(items)).To(Equal("K0, K1, K2, K3, K4, K5, K6, K7, K8, K9 and 2 more"))
	})
})

var _ = Describe("setReconcileStatusCondition", func() {
	It("cuts a message the API server would reject to the bytes it accepts, keeping it valid UTF-8", func() {
		push := &v1beta1.InfisicalStaticSecretPush{}

		// "é" takes two bytes, so cutting at an exact byte count could split one.
		setReconcileStatusCondition(push, errors.New("x"+strings.Repeat("é", maxConditionMessageLength)))

		message := push.Status.Conditions[0].Message
		Expect(len(message)).To(BeNumerically("<=", maxConditionMessageLength))
		Expect(len(message)).To(BeNumerically(">", maxConditionMessageLength-4))
		Expect(utf8.ValidString(message)).To(BeTrue())
		Expect(message).To(HavePrefix("Reconciliation failed: xéé"))
		Expect(message).To(HaveSuffix("é... (truncated)"))
	})

	It("keeps a message that fits", func() {
		push := &v1beta1.InfisicalStaticSecretPush{}

		setReconcileStatusCondition(push, errors.New("boom"))

		Expect(push.Status.Conditions[0].Message).To(Equal("Reconciliation failed: boom"))
	})
})
