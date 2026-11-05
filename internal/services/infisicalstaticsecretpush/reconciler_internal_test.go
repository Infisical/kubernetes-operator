package infisicalstaticsecretpush

import (
	"strings"

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
	})

	It("leaves keys it doesn't own untouched with the None policy", func() {
		plan := planTarget(desired, existing, managed, v1beta1.PushUpdatePolicyNone)

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

		failed, err := reconciler.deleteSecrets(restClient, map[string]string{firstID: "FIRST", secondID: "SECOND"}, listed, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(failed).To(BeEmpty())
		Expect(server.Secrets(projectID, environment, "/app")).To(BeEmpty())
	})

	It("looks up secrets that weren't listed and deletes them where they are now", func() {
		id := server.CreateSecret(projectID, environment, "/elsewhere", "KEY", "value")

		failed, err := reconciler.deleteSecrets(restClient, map[string]string{id: "KEY"}, nil, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(failed).To(BeEmpty())
		Expect(server.Secrets(projectID, environment, "/elsewhere")).To(BeEmpty())
		Expect(server.CountRequests("GET /api/v4/secrets/id/" + id)).To(Equal(1))
	})

	It("skips secrets that no longer exist", func() {
		failed, err := reconciler.deleteSecrets(restClient, map[string]string{"gone": "KEY"}, nil, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(failed).To(BeEmpty())
		Expect(server.CountRequests("DELETE /api/v4/secrets/batch")).To(Equal(0))
	})

	It("keeps the secrets it couldn't delete", func() {
		id := server.CreateSecret(projectID, environment, "/app", "KEY", "value")
		server.RequireApproval(projectID, environment, "/app")

		failed, err := reconciler.deleteSecrets(restClient, map[string]string{id: "KEY"}, nil, nil)

		Expect(err).To(MatchError(ContainSubstring("require approval")))
		Expect(failed).To(Equal(map[string]string{id: "KEY"}))
	})

	It("skips a folder and key the resource now owns under another ID", func() {
		id := server.CreateSecret(projectID, environment, "/app", "KEY", "value")
		keep := map[secretRef]struct{}{{location: location, key: "KEY"}: {}}

		failed, err := reconciler.deleteSecrets(restClient, map[string]string{"stale-id": "KEY"}, map[string]secretRef{"stale-id": {location: location, key: "KEY"}}, keep)

		Expect(err).NotTo(HaveOccurred())
		Expect(failed).To(BeEmpty())
		Expect(server.Secrets(projectID, environment, "/app")["KEY"].ID).To(Equal(id))
	})
})
