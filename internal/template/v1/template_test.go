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
		Expect(subfolder["SECRET_1"]).To(Equal(model.V1TemplateOptions{Value: "val1", SecretPath: "/folder/subfolder"}))
		Expect(subfolder["SECRET_2"]).To(Equal(model.V1TemplateOptions{Value: "val2", SecretPath: "/folder/subfolder"}))

		another, ok := folder["another"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(another["SECRET_3"]).To(Equal(model.V1TemplateOptions{Value: "val3", SecretPath: "/folder/another"}))
	})

	It("places secrets at the root path", func() {
		ctx := v1.NewTemplateContext([]api.Secret{
			{SecretKey: "ROOT_SECRET", SecretValue: "root-val", SecretPath: "/"},
		}, nil)

		tree := v1.BuildSecretTree(ctx)
		Expect(tree["ROOT_SECRET"]).To(Equal(model.V1TemplateOptions{Value: "root-val", SecretPath: "/"}))
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
		Expect(d["DEEP"]).To(Equal(model.V1TemplateOptions{Value: "deep-val", SecretPath: "/a/b/c/d"}))
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
		Expect(shared["DB_HOST"]).To(Equal(model.V1TemplateOptions{Value: "project-a-host", SecretPath: "/shared"}))
		Expect(shared["DB_PORT"]).To(Equal(model.V1TemplateOptions{Value: "5432", SecretPath: "/shared"}))
		Expect(shared["API_KEY"]).To(Equal(model.V1TemplateOptions{Value: "key-from-b", SecretPath: "/shared"}))

		db, ok := tree["db"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(db["DB_PORT"]).To(Equal(model.V1TemplateOptions{Value: "1234", SecretPath: "/db"}))
	})

	It("handles mixed root and nested secrets", func() {
		ctx := v1.NewTemplateContext([]api.Secret{
			{SecretKey: "AT_ROOT", SecretValue: "r", SecretPath: "/"},
			{SecretKey: "IN_FOLDER", SecretValue: "f", SecretPath: "/app"},
		}, nil)

		tree := v1.BuildSecretTree(ctx)

		Expect(tree["AT_ROOT"]).To(Equal(model.V1TemplateOptions{Value: "r", SecretPath: "/"}))
		app, ok := tree["app"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(app["IN_FOLDER"]).To(Equal(model.V1TemplateOptions{Value: "f", SecretPath: "/app"}))
	})
})

var _ = Describe("RenderPerKeyTemplates without secretFrom", func() {

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

	It("resolves value when .Value is not used", func() {
		tmpls := map[string]string{
			"host": `{{ .DB_HOST }}`,
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

var _ = Describe("RenderBulkTemplate without secretFrom", func() {

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

var _ = Describe("RenderPerKeyTemplates with secretFrom", func() {

	It("resolves value via secretFrom with .Value accessor", func() {
		tmpls := map[string]string{
			"host": `{{ (secretFrom "/folder/subfolder" "DB_HOST").Value }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("host", []byte("prod-db.example.com")))
	})

	It("resolves value via secretFrom without accessor", func() {
		tmpls := map[string]string{
			"host": `{{ secretFrom "/folder/subfolder" "DB_HOST" }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("host", []byte("prod-db.example.com")))
	})

	It("resolves value via secretFrom with single argument (name only)", func() {
		rootCtx := v1.NewTemplateContext(
			[]api.Secret{
				{SecretKey: "MY_SECRET", SecretValue: "root-val", SecretPath: "/"},
			},
			[]api.Secret{
				{SecretKey: "MY_SECRET", SecretValue: "root-val", SecretPath: "/"},
			},
		)
		tmpls := map[string]string{
			"secret": `{{ secretFrom "MY_SECRET" }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, rootCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("secret", []byte("root-val")))
	})

	It("resolves secretPath via secretFrom", func() {
		tmpls := map[string]string{
			"path": `{{ (secretFrom "/folder/subfolder" "DB_HOST").SecretPath }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("path", []byte("/folder/subfolder")))
	})

	It("resolves secrets from different subfolders in the same template", func() {
		tmpls := map[string]string{
			"combined": `{{ secretFrom "/folder/subfolder" "DB_HOST" }}:{{ secretFrom "/folder/subfolder" "DB_PORT" }} key={{ secretFrom "/folder/other" "API_KEY" }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("combined", []byte("prod-db.example.com:5432 key=other-secret")))
	})

	It("returns error for non-existent path segment", func() {
		tmpls := map[string]string{
			"bad": `{{ secretFrom "/missing" "DB_HOST" }}`,
		}

		_, err := v1.RenderPerKeyTemplates(tmpls, subfolderCtx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not found"))
	})

	It("returns error for non-existent secret name", func() {
		tmpls := map[string]string{
			"bad": `{{ secretFrom "/folder/subfolder" "NOPE" }}`,
		}

		_, err := v1.RenderPerKeyTemplates(tmpls, subfolderCtx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not found"))
	})

	It("does not duplicate values when ranging over all secrets alongside secretFrom", func() {
		tmpls := map[string]string{
			"ref_host": `{{ secretFrom "/folder/subfolder" "DB_HOST" }}`,
			"ref_port": `{{ secretFrom "/folder/subfolder" "DB_PORT" }}`,
			"ref_key":  `{{ secretFrom "/folder/other" "API_KEY" }}`,
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
		Expect(unique).To(HaveLen(3), "each secretFrom should resolve to a distinct value")
	})

	It("resolves duplicate secret keys from different paths", func() {
		tmpls := map[string]string{
			"subfolder_key": `{{ secretFrom "/folder/subfolder" "API_KEY" }}`,
			"other_key":     `{{ secretFrom "/folder/other" "API_KEY" }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("subfolder_key", []byte("subfolder-secret")))
		Expect(data).To(HaveKeyWithValue("other_key", []byte("other-secret")))
	})

	It("resolves secretPath of duplicate secret keys from different paths", func() {
		tmpls := map[string]string{
			"subfolder_path": `{{ (secretFrom "/folder/subfolder" "API_KEY").SecretPath }}`,
			"other_path":     `{{ (secretFrom "/folder/other" "API_KEY").SecretPath }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("subfolder_path", []byte("/folder/subfolder")))
		Expect(data).To(HaveKeyWithValue("other_path", []byte("/folder/other")))
	})
})

var _ = Describe("RenderBulkTemplate with secretFrom", func() {

	It("returns error for non-existent path segment", func() {
		tmpl := `bad: "{{ secretFrom "/missing" "DB_HOST" }}"`

		_, err := v1.RenderBulkTemplate(tmpl, subfolderCtx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not found"))
	})

	It("resolves value via secretFrom", func() {
		tmpl := `host: "{{ secretFrom "/folder/subfolder" "DB_HOST" }}"`

		data, err := v1.RenderBulkTemplate(tmpl, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("host", []byte("prod-db.example.com")))
	})

	It("resolves secretPath via secretFrom", func() {
		tmpl := `path: "{{ (secretFrom "/folder/other" "API_KEY").SecretPath }}"`

		data, err := v1.RenderBulkTemplate(tmpl, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("path", []byte("/folder/other")))
	})

	It("resolves multiple secrets from different subfolders", func() {
		tmpl := `dsn: "{{ secretFrom "/folder/subfolder" "DB_HOST" }}:{{ secretFrom "/folder/subfolder" "DB_PORT" }}"
api_key: "{{ secretFrom "/folder/other" "API_KEY" }}"`

		data, err := v1.RenderBulkTemplate(tmpl, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveLen(2))
		Expect(data).To(HaveKeyWithValue("dsn", []byte("prod-db.example.com:5432")))
		Expect(data).To(HaveKeyWithValue("api_key", []byte("other-secret")))
	})

	It("does not duplicate values when rendering all secrets via secretFrom", func() {
		tmpl := `host: "{{ secretFrom "/folder/subfolder" "DB_HOST" }}"
port: "{{ secretFrom "/folder/subfolder" "DB_PORT" }}"
key: "{{ secretFrom "/folder/other" "API_KEY" }}"`

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
		Expect(unique).To(HaveLen(3), "each secretFrom should resolve to a distinct value")
	})

	It("resolves duplicate secret keys from different paths", func() {
		tmpl := `subfolder_key: "{{ secretFrom "/folder/subfolder" "API_KEY" }}"
other_key: "{{ secretFrom "/folder/other" "API_KEY" }}"`

		data, err := v1.RenderBulkTemplate(tmpl, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("subfolder_key", []byte("subfolder-secret")))
		Expect(data).To(HaveKeyWithValue("other_key", []byte("other-secret")))
	})

	It("resolves secretPath of duplicate secret keys from different paths", func() {
		tmpl := `subfolder_path: "{{ (secretFrom "/folder/subfolder" "API_KEY").SecretPath }}"
other_path: "{{ (secretFrom "/folder/other" "API_KEY").SecretPath }}"`

		data, err := v1.RenderBulkTemplate(tmpl, subfolderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("subfolder_path", []byte("/folder/subfolder")))
		Expect(data).To(HaveKeyWithValue("other_path", []byte("/folder/other")))
	})
})
