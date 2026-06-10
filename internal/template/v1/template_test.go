package v1_test

import (
	"sort"

	"github.com/Infisical/infisical/k8-operator/internal/api"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	v1 "github.com/Infisical/infisical/k8-operator/internal/template/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var subfolderCtx = v1.NewTemplateContext(
	[]api.Secret{
		{SecretKey: "DB_HOST", SecretValue: "prod-db.example.com", SecretPath: "/folder/subfolder"},
		{SecretKey: "DB_PORT", SecretValue: "5432", SecretPath: "/folder/subfolder"},
		{SecretKey: "API_KEY", SecretValue: "subfolder-secret", SecretPath: "/folder/subfolder"},
		{SecretKey: "API_KEY", SecretValue: "other-secret", SecretPath: "/folder/other"},
	},
	[]api.Secret{
		{SecretKey: "DB_HOST", SecretValue: "prod-db.example.com", SecretPath: "/folder/subfolder"},
		{SecretKey: "DB_PORT", SecretValue: "5432", SecretPath: "/folder/subfolder"},
		{SecretKey: "API_KEY", SecretValue: "other-secret", SecretPath: "/folder/other"},
	},
)

var _ = Describe("BuildSecretTree", func() {

	It("places secrets in nested subfolders", func() {
		ctx := v1.NewTemplateContext([]api.Secret{
			{SecretKey: "SECRET_1", SecretValue: "val1", SecretPath: "/folder/subfolder"},
			{SecretKey: "SECRET_2", SecretValue: "val2", SecretPath: "/folder/subfolder"},
			{SecretKey: "SECRET_3", SecretValue: "val3", SecretPath: "/folder/another"},
		}, nil)

		tree := v1.BuildSecretTree(ctx)

		folder, ok := tree["folder"].(map[string]any)
		Expect(ok).To(BeTrue())

		subfolder, ok := folder["subfolder"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(subfolder["SECRET_1"]).To(Equal(model.SecretTemplateOptions{Value: "val1", SecretPath: "/folder/subfolder"}))
		Expect(subfolder["SECRET_2"]).To(Equal(model.SecretTemplateOptions{Value: "val2", SecretPath: "/folder/subfolder"}))

		another, ok := folder["another"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(another["SECRET_3"]).To(Equal(model.SecretTemplateOptions{Value: "val3", SecretPath: "/folder/another"}))
	})

	It("places secrets at the root path", func() {
		ctx := v1.NewTemplateContext([]api.Secret{
			{SecretKey: "ROOT_SECRET", SecretValue: "root-val", SecretPath: "/"},
		}, nil)

		tree := v1.BuildSecretTree(ctx)
		Expect(tree["ROOT_SECRET"]).To(Equal(model.SecretTemplateOptions{Value: "root-val", SecretPath: "/"}))
	})

	It("returns an empty tree for empty context", func() {
		ctx := v1.NewTemplateContext(nil, nil)
		tree := v1.BuildSecretTree(ctx)
		Expect(tree).To(BeEmpty())
	})

	It("handles deeply nested paths", func() {
		ctx := v1.NewTemplateContext([]api.Secret{
			{SecretKey: "DEEP", SecretValue: "deep-val", SecretPath: "/a/b/c/d"},
		}, nil)

		tree := v1.BuildSecretTree(ctx)

		a, ok := tree["a"].(map[string]any)
		Expect(ok).To(BeTrue())
		b, ok := a["b"].(map[string]any)
		Expect(ok).To(BeTrue())
		c, ok := b["c"].(map[string]any)
		Expect(ok).To(BeTrue())
		d, ok := c["d"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(d["DEEP"]).To(Equal(model.SecretTemplateOptions{Value: "deep-val", SecretPath: "/a/b/c/d"}))
	})

	It("keeps first occurrence when duplicate keys exist at the same path", func() {
		ctx := v1.NewTemplateContext([]api.Secret{
			{SecretKey: "DB_HOST", SecretValue: "project-a-host", SecretPath: "/shared"},
			{SecretKey: "DB_PORT", SecretValue: "5432", SecretPath: "/shared"},
			{SecretKey: "DB_HOST", SecretValue: "project-b-host", SecretPath: "/shared"},
			{SecretKey: "API_KEY", SecretValue: "key-from-b", SecretPath: "/shared"},
			{SecretKey: "DB_PORT", SecretValue: "1234", SecretPath: "/db"},
		}, nil)

		tree := v1.BuildSecretTree(ctx)

		shared, ok := tree["shared"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(shared["DB_HOST"]).To(Equal(model.SecretTemplateOptions{Value: "project-a-host", SecretPath: "/shared"}))
		Expect(shared["DB_PORT"]).To(Equal(model.SecretTemplateOptions{Value: "5432", SecretPath: "/shared"}))
		Expect(shared["API_KEY"]).To(Equal(model.SecretTemplateOptions{Value: "key-from-b", SecretPath: "/shared"}))

		db, ok := tree["db"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(db["DB_PORT"]).To(Equal(model.SecretTemplateOptions{Value: "1234", SecretPath: "/db"}))
	})

	It("handles mixed root and nested secrets", func() {
		ctx := v1.NewTemplateContext([]api.Secret{
			{SecretKey: "AT_ROOT", SecretValue: "r", SecretPath: "/"},
			{SecretKey: "IN_FOLDER", SecretValue: "f", SecretPath: "/app"},
		}, nil)

		tree := v1.BuildSecretTree(ctx)

		Expect(tree["AT_ROOT"]).To(Equal(model.SecretTemplateOptions{Value: "r", SecretPath: "/"}))
		app, ok := tree["app"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(app["IN_FOLDER"]).To(Equal(model.SecretTemplateOptions{Value: "f", SecretPath: "/app"}))
	})
})

var _ = Describe("RenderPerKeyTemplates without resolveSecretFromPath", func() {

	rootCtx := v1.NewTemplateContext(nil, []api.Secret{
		{SecretKey: "DB_HOST", SecretValue: "localhost", SecretPath: "/"},
		{SecretKey: "DB_PORT", SecretValue: "5432", SecretPath: "/"},
		{SecretKey: "DB_NAME", SecretValue: "mydb", SecretPath: "/"},
	})

	It("resolves .Value accessor directly", func() {
		tmpls := map[string]string{
			"host": `{{ .DB_HOST.Value }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, rootCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("host", []byte("localhost")))
	})

	It("resolves .SecretPath accessor directly", func() {
		tmpls := map[string]string{
			"path": `{{ .DB_HOST.SecretPath }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, rootCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("path", []byte("/")))
	})

	It("combines multiple secrets in a single template key", func() {
		tmpls := map[string]string{
			"dsn": `postgresql://user:pass@{{ .DB_HOST.Value }}:{{ .DB_PORT.Value }}/{{ .DB_NAME.Value }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, rootCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("dsn", []byte("postgresql://user:pass@localhost:5432/mydb")))
	})
})

var _ = Describe("RenderBulkTemplate without resolveSecretFromPath", func() {

	rootCtx := v1.NewTemplateContext(nil, []api.Secret{
		{SecretKey: "DB_HOST", SecretValue: "localhost", SecretPath: "/"},
		{SecretKey: "DB_PORT", SecretValue: "5432", SecretPath: "/"},
	})

	It("resolves .Value accessor directly", func() {
		tmpl := `host: "{{ .DB_HOST.Value }}"`

		data, err := v1.RenderBulkTemplate(tmpl, rootCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("host", []byte("localhost")))
	})

	It("renders a range over all secrets", func() {
		tmpl := `{{- range $key, $secret := . }}
{{ $key }}: "{{ $secret.Value }}"
{{- end }}`

		data, err := v1.RenderBulkTemplate(tmpl, rootCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveLen(2))
		Expect(data).To(HaveKeyWithValue("DB_HOST", []byte("localhost")))
		Expect(data).To(HaveKeyWithValue("DB_PORT", []byte("5432")))
	})
})

var _ = Describe("RenderPerKeyTemplates with resolveSecretFromPath", func() {

	It("resolves value via resolveSecretFromPath with nested path", func() {
		tmpls := map[string]string{
			"host": `{{ resolveSecretFromPath "folder.subfolder.DB_HOST.value" }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("host", []byte("prod-db.example.com")))
	})

	It("resolves secretPath via resolveSecretFromPath", func() {
		tmpls := map[string]string{
			"path": `{{ resolveSecretFromPath "folder.subfolder.DB_HOST.secretPath" }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("path", []byte("/folder/subfolder")))
	})

	It("resolves secrets from different subfolders in the same template", func() {
		tmpls := map[string]string{
			"combined": `{{ resolveSecretFromPath "folder.subfolder.DB_HOST.value" }}:{{ resolveSecretFromPath "folder.subfolder.DB_PORT.value" }} key={{ resolveSecretFromPath "folder.other.API_KEY.value" }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("combined", []byte("prod-db.example.com:5432 key=other-secret")))
	})

	It("returns error for unknown accessor", func() {
		tmpls := map[string]string{
			"bad": `{{ resolveSecretFromPath "folder.subfolder.DB_HOST.unknown" }}`,
		}

		_, err := v1.RenderPerKeyTemplates(tmpls, subfolderCtx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unknown accessor"))
	})

	It("returns error for non-existent path segment", func() {
		tmpls := map[string]string{
			"bad": `{{ resolveSecretFromPath "missing.DB_HOST.value" }}`,
		}

		_, err := v1.RenderPerKeyTemplates(tmpls, subfolderCtx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not found"))
	})

	It("does not duplicate values when ranging over all secrets alongside resolveSecretFromPath", func() {
		tmpls := map[string]string{
			"ref_host": `{{ resolveSecretFromPath "folder.subfolder.DB_HOST.value" }}`,
			"ref_port": `{{ resolveSecretFromPath "folder.subfolder.DB_PORT.value" }}`,
			"ref_key":  `{{ resolveSecretFromPath "folder.other.API_KEY.value" }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveLen(3))

		values := make([]string, 0, len(data))
		for _, v := range data {
			values = append(values, string(v))
		}
		sort.Strings(values)
		unique := make([]string, 0, len(values))
		for i, v := range values {
			if i == 0 || v != values[i-1] {
				unique = append(unique, v)
			}
		}
		Expect(unique).To(HaveLen(3), "each resolveSecretFromPath should resolve to a distinct value")
	})

	It("resolves duplicate secret keys from different paths", func() {
		tmpls := map[string]string{
			"subfolder_key": `{{ resolveSecretFromPath "folder.subfolder.API_KEY.value" }}`,
			"other_key":     `{{ resolveSecretFromPath "folder.other.API_KEY.value" }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("subfolder_key", []byte("subfolder-secret")))
		Expect(data).To(HaveKeyWithValue("other_key", []byte("other-secret")))
	})

	It("resolves secretPath of duplicate secret keys from different paths", func() {
		tmpls := map[string]string{
			"subfolder_path": `{{ resolveSecretFromPath "folder.subfolder.API_KEY.secretPath" }}`,
			"other_path":     `{{ resolveSecretFromPath "folder.other.API_KEY.secretPath" }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("subfolder_path", []byte("/folder/subfolder")))
		Expect(data).To(HaveKeyWithValue("other_path", []byte("/folder/other")))
	})
})

var _ = Describe("RenderBulkTemplate with resolveSecretFromPath", func() {

	It("returns error for non-existent path segment", func() {
		tmpl := `bad: "{{ resolveSecretFromPath "missing.DB_HOST.value" }}"`

		_, err := v1.RenderBulkTemplate(tmpl, subfolderCtx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not found"))
	})

	It("resolves value via resolveSecretFromPath", func() {
		tmpl := `host: "{{ resolveSecretFromPath "folder.subfolder.DB_HOST.value" }}"`

		data, err := v1.RenderBulkTemplate(tmpl, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("host", []byte("prod-db.example.com")))
	})

	It("resolves secretPath via resolveSecretFromPath", func() {
		tmpl := `path: "{{ resolveSecretFromPath "folder.other.API_KEY.secretPath" }}"`

		data, err := v1.RenderBulkTemplate(tmpl, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("path", []byte("/folder/other")))
	})

	It("resolves multiple secrets from different subfolders", func() {
		tmpl := `dsn: "{{ resolveSecretFromPath "folder.subfolder.DB_HOST.value" }}:{{ resolveSecretFromPath "folder.subfolder.DB_PORT.value" }}"
api_key: "{{ resolveSecretFromPath "folder.other.API_KEY.value" }}"`

		data, err := v1.RenderBulkTemplate(tmpl, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveLen(2))
		Expect(data).To(HaveKeyWithValue("dsn", []byte("prod-db.example.com:5432")))
		Expect(data).To(HaveKeyWithValue("api_key", []byte("other-secret")))
	})

	It("does not duplicate values when rendering all secrets via resolveSecretFromPath", func() {
		tmpl := `host: "{{ resolveSecretFromPath "folder.subfolder.DB_HOST.value" }}"
port: "{{ resolveSecretFromPath "folder.subfolder.DB_PORT.value" }}"
key: "{{ resolveSecretFromPath "folder.other.API_KEY.value" }}"`

		data, err := v1.RenderBulkTemplate(tmpl, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveLen(3))

		values := make([]string, 0, len(data))
		for _, v := range data {
			values = append(values, string(v))
		}
		sort.Strings(values)
		unique := make([]string, 0, len(values))
		for i, v := range values {
			if i == 0 || v != values[i-1] {
				unique = append(unique, v)
			}
		}
		Expect(unique).To(HaveLen(3), "each resolveSecretFromPath should resolve to a distinct value")
	})

	It("resolves duplicate secret keys from different paths", func() {
		tmpl := `subfolder_key: "{{ resolveSecretFromPath "folder.subfolder.API_KEY.value" }}"
other_key: "{{ resolveSecretFromPath "folder.other.API_KEY.value" }}"`

		data, err := v1.RenderBulkTemplate(tmpl, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("subfolder_key", []byte("subfolder-secret")))
		Expect(data).To(HaveKeyWithValue("other_key", []byte("other-secret")))
	})

	It("resolves secretPath of duplicate secret keys from different paths", func() {
		tmpl := `subfolder_path: "{{ resolveSecretFromPath "folder.subfolder.API_KEY.secretPath" }}"
other_path: "{{ resolveSecretFromPath "folder.other.API_KEY.secretPath" }}"`

		data, err := v1.RenderBulkTemplate(tmpl, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("subfolder_path", []byte("/folder/subfolder")))
		Expect(data).To(HaveKeyWithValue("other_path", []byte("/folder/other")))
	})
})
