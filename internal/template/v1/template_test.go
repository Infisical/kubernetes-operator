package v1_test

import (
	"sort"

	"github.com/Infisical/infisical/k8-operator/internal/api"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	v1 "github.com/Infisical/infisical/k8-operator/internal/template/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var subfolderCtx = v1.NewTemplateContext(v1.RenderContext{
	RawSecrets: []api.Secret{
		{SecretKey: "DB_HOST", SecretValue: "prod-db.example.com", SecretPath: "/folder/subfolder"},
		{SecretKey: "DB_PORT", SecretValue: "5432", SecretPath: "/folder/subfolder"},
		{SecretKey: "API_KEY", SecretValue: "subfolder-secret", SecretPath: "/folder/subfolder"},
		{SecretKey: "API_KEY", SecretValue: "other-secret", SecretPath: "/folder/other"},
	},
	MergedSecrets: []api.Secret{
		{SecretKey: "DB_HOST", SecretValue: "prod-db.example.com", SecretPath: "/folder/subfolder"},
		{SecretKey: "DB_PORT", SecretValue: "5432", SecretPath: "/folder/subfolder"},
		{SecretKey: "API_KEY", SecretValue: "other-secret", SecretPath: "/folder/other"},
	},
})

var _ = Describe("BuildSecretTree", func() {

	It("places secrets in nested subfolders", func() {
		ctx := v1.NewTemplateContext(v1.RenderContext{RawSecrets: []api.Secret{
			{SecretKey: "SECRET_1", SecretValue: "val1", SecretPath: "/folder/subfolder"},
			{SecretKey: "SECRET_2", SecretValue: "val2", SecretPath: "/folder/subfolder"},
			{SecretKey: "SECRET_3", SecretValue: "val3", SecretPath: "/folder/another"},
		}})

		tree := v1.BuildSecretTree(ctx)

		folder := tree.Children["folder"]
		Expect(folder).NotTo(BeNil())

		subfolder := folder.Children["subfolder"]
		Expect(subfolder).NotTo(BeNil())
		Expect(subfolder.Children["SECRET_1"].Secret).To(HaveValue(Equal(model.V1TemplateOptions{Value: "val1", SecretPath: "/folder/subfolder"})))
		Expect(subfolder.Children["SECRET_2"].Secret).To(HaveValue(Equal(model.V1TemplateOptions{Value: "val2", SecretPath: "/folder/subfolder"})))

		another := folder.Children["another"]
		Expect(another).NotTo(BeNil())
		Expect(another.Children["SECRET_3"].Secret).To(HaveValue(Equal(model.V1TemplateOptions{Value: "val3", SecretPath: "/folder/another"})))
	})

	It("places secrets at the root path", func() {
		ctx := v1.NewTemplateContext(v1.RenderContext{RawSecrets: []api.Secret{
			{SecretKey: "ROOT_SECRET", SecretValue: "root-val", SecretPath: "/"},
		}})

		tree := v1.BuildSecretTree(ctx)
		Expect(tree.Children["ROOT_SECRET"].Secret).To(HaveValue(Equal(model.V1TemplateOptions{Value: "root-val", SecretPath: "/"})))
	})

	It("returns an empty tree for empty context", func() {
		ctx := v1.NewTemplateContext(v1.RenderContext{})
		tree := v1.BuildSecretTree(ctx)
		Expect(tree.Children).To(BeNil())
	})

	It("handles deeply nested paths", func() {
		ctx := v1.NewTemplateContext(v1.RenderContext{RawSecrets: []api.Secret{
			{SecretKey: "DEEP", SecretValue: "deep-val", SecretPath: "/a/b/c/d"},
		}})

		tree := v1.BuildSecretTree(ctx)

		a := tree.Children["a"]
		Expect(a).NotTo(BeNil())
		b := a.Children["b"]
		Expect(b).NotTo(BeNil())
		c := b.Children["c"]
		Expect(c).NotTo(BeNil())
		d := c.Children["d"]
		Expect(d).NotTo(BeNil())
		Expect(d.Children["DEEP"].Secret).To(HaveValue(Equal(model.V1TemplateOptions{Value: "deep-val", SecretPath: "/a/b/c/d"})))
	})

	It("keeps first occurrence when duplicate keys exist at the same path", func() {
		ctx := v1.NewTemplateContext(v1.RenderContext{RawSecrets: []api.Secret{
			{SecretKey: "DB_HOST", SecretValue: "project-a-host", SecretPath: "/shared"},
			{SecretKey: "DB_PORT", SecretValue: "5432", SecretPath: "/shared"},
			{SecretKey: "DB_HOST", SecretValue: "project-b-host", SecretPath: "/shared"},
			{SecretKey: "API_KEY", SecretValue: "key-from-b", SecretPath: "/shared"},
			{SecretKey: "DB_PORT", SecretValue: "1234", SecretPath: "/db"},
		}})

		tree := v1.BuildSecretTree(ctx)

		shared := tree.Children["shared"]
		Expect(shared).NotTo(BeNil())
		Expect(shared.Children["DB_HOST"].Secret).To(HaveValue(Equal(model.V1TemplateOptions{Value: "project-a-host", SecretPath: "/shared"})))
		Expect(shared.Children["DB_PORT"].Secret).To(HaveValue(Equal(model.V1TemplateOptions{Value: "5432", SecretPath: "/shared"})))
		Expect(shared.Children["API_KEY"].Secret).To(HaveValue(Equal(model.V1TemplateOptions{Value: "key-from-b", SecretPath: "/shared"})))

		db := tree.Children["db"]
		Expect(db).NotTo(BeNil())
		Expect(db.Children["DB_PORT"].Secret).To(HaveValue(Equal(model.V1TemplateOptions{Value: "1234", SecretPath: "/db"})))
	})

	It("handles mixed root and nested secrets", func() {
		ctx := v1.NewTemplateContext(v1.RenderContext{RawSecrets: []api.Secret{
			{SecretKey: "AT_ROOT", SecretValue: "r", SecretPath: "/"},
			{SecretKey: "IN_FOLDER", SecretValue: "f", SecretPath: "/app"},
		}})

		tree := v1.BuildSecretTree(ctx)

		Expect(tree.Children["AT_ROOT"].Secret).To(HaveValue(Equal(model.V1TemplateOptions{Value: "r", SecretPath: "/"})))
		app := tree.Children["app"]
		Expect(app).NotTo(BeNil())
		Expect(app.Children["IN_FOLDER"].Secret).To(HaveValue(Equal(model.V1TemplateOptions{Value: "f", SecretPath: "/app"})))
	})

	It("includes imported secrets not present in rawSecrets", func() {
		ctx := v1.NewTemplateContext(v1.RenderContext{
			RawSecrets: []api.Secret{
				{SecretKey: "RAW_ONLY", SecretValue: "raw-val", SecretPath: "/app"},
			},
			ImportedSecrets: []api.Secret{
				{SecretKey: "IMPORTED_ONLY", SecretValue: "imported-val", SecretPath: "/app"},
			},
		})

		tree := v1.BuildSecretTree(ctx)

		app := tree.Children["app"]
		Expect(app).NotTo(BeNil())
		Expect(app.Children["RAW_ONLY"].Secret).To(HaveValue(Equal(model.V1TemplateOptions{Value: "raw-val", SecretPath: "/app"})))
		Expect(app.Children["IMPORTED_ONLY"].Secret).To(HaveValue(Equal(model.V1TemplateOptions{Value: "imported-val", SecretPath: "/app"})))
	})

	It("prefers raw secret over imported secret with the same key and path", func() {
		ctx := v1.NewTemplateContext(v1.RenderContext{
			RawSecrets: []api.Secret{
				{SecretKey: "DB_HOST", SecretValue: "raw-host", SecretPath: "/shared"},
			},
			ImportedSecrets: []api.Secret{
				{SecretKey: "DB_HOST", SecretValue: "imported-host", SecretPath: "/shared"},
			},
		})

		tree := v1.BuildSecretTree(ctx)

		shared := tree.Children["shared"]
		Expect(shared).NotTo(BeNil())
		Expect(shared.Children["DB_HOST"].Secret).To(HaveValue(Equal(model.V1TemplateOptions{Value: "raw-host", SecretPath: "/shared"})))
	})

	It("allows a secret key and folder segment with the same name", func() {
		ctx := v1.NewTemplateContext(v1.RenderContext{RawSecrets: []api.Secret{
			{SecretKey: "db", SecretValue: "some-value", SecretPath: "/"},
			{SecretKey: "PASSWORD", SecretValue: "secret", SecretPath: "/db"},
		}})

		tree := v1.BuildSecretTree(ctx)

		dbNode := tree.Children["db"]
		Expect(dbNode).NotTo(BeNil())
		Expect(dbNode.Secret).To(HaveValue(Equal(model.V1TemplateOptions{Value: "some-value", SecretPath: "/"})))
		Expect(dbNode.Children["PASSWORD"].Secret).To(HaveValue(Equal(model.V1TemplateOptions{Value: "secret", SecretPath: "/db"})))
	})
})

var _ = Describe("RenderPerKeyTemplates without secretFrom", func() {

	rootCtx := v1.NewTemplateContext(v1.RenderContext{MergedSecrets: []api.Secret{
		{SecretKey: "DB_HOST", SecretValue: "localhost", SecretPath: "/"},
		{SecretKey: "DB_PORT", SecretValue: "5432", SecretPath: "/"},
		{SecretKey: "DB_NAME", SecretValue: "mydb", SecretPath: "/"},
	}})

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

	rootCtx := v1.NewTemplateContext(v1.RenderContext{MergedSecrets: []api.Secret{
		{SecretKey: "DB_HOST", SecretValue: "localhost", SecretPath: "/"},
		{SecretKey: "DB_PORT", SecretValue: "5432", SecretPath: "/"},
	}})

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

	It("resolves imported-only secret via secretFrom", func() {
		ctx := v1.NewTemplateContext(v1.RenderContext{
			RawSecrets: []api.Secret{
				{SecretKey: "RAW_SECRET", SecretValue: "raw-val", SecretPath: "/app"},
			},
			ImportedSecrets: []api.Secret{
				{SecretKey: "IMPORTED_SECRET", SecretValue: "imported-val", SecretPath: "/lib"},
			},
			MergedSecrets: []api.Secret{
				{SecretKey: "RAW_SECRET", SecretValue: "raw-val", SecretPath: "/app"},
			},
		})
		tmpls := map[string]string{
			"imported": `{{ secretFrom "/lib" "IMPORTED_SECRET" }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("imported", []byte("imported-val")))
	})

	It("resolves raw secret if conflict with imported secret via secretFrom", func() {
		ctx := v1.NewTemplateContext(v1.RenderContext{
			RawSecrets: []api.Secret{
				{SecretKey: "SECRET", SecretValue: "raw-val", SecretPath: "/app"},
			},
			ImportedSecrets: []api.Secret{
				{SecretKey: "SECRET", SecretValue: "imported-val", SecretPath: "/app"},
			},
			MergedSecrets: []api.Secret{
				{SecretKey: "SECRET", SecretValue: "raw-val", SecretPath: "/app"},
			},
		})
		tmpls := map[string]string{
			"secret": `{{ secretFrom "/app" "SECRET" }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("secret", []byte("raw-val")))
	})

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

	It("resolves value via secretFrom for root path secrets", func() {
		rootCtx := v1.NewTemplateContext(v1.RenderContext{
			RawSecrets: []api.Secret{
				{SecretKey: "MY_SECRET", SecretValue: "root-val", SecretPath: "/"},
			},
			MergedSecrets: []api.Secret{
				{SecretKey: "MY_SECRET", SecretValue: "root-val", SecretPath: "/"},
			},
		})
		tmpls := map[string]string{
			"secret": `{{ secretFrom "/" "MY_SECRET" }}`,
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

	It("errors if only one parameter is passed to secretPath", func() {
		tmpl := `subfolder_path: "{{ (secretFrom "API_KEY").Value }}"`

		_, err := v1.RenderBulkTemplate(tmpl, subfolderCtx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("wrong number of args"))
	})
})
