package v1_test

import (
	"sort"

	"github.com/Infisical/infisical/k8-operator/internal/model"
	v1 "github.com/Infisical/infisical/k8-operator/internal/template/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var subfolderSecrets = map[string]model.SecretTemplateOptions{
	"DB_HOST": {Value: "prod-db.example.com", SecretPath: "/folder/subfolder"},
	"DB_PORT": {Value: "5432", SecretPath: "/folder/subfolder"},
	"API_KEY": {Value: "sk-secret-123", SecretPath: "/folder/other"},
}

var _ = Describe("BuildSecretTree", func() {

	It("places secrets in nested subfolders", func() {
		ctx := map[string]model.SecretTemplateOptions{
			"SECRET_1": {Value: "val1", SecretPath: "/folder/subfolder"},
			"SECRET_2": {Value: "val2", SecretPath: "/folder/subfolder"},
			"SECRET_3": {Value: "val3", SecretPath: "/folder/another"},
		}

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
		ctx := map[string]model.SecretTemplateOptions{
			"ROOT_SECRET": {Value: "root-val", SecretPath: "/"},
		}

		tree := v1.BuildSecretTree(ctx)
		Expect(tree["ROOT_SECRET"]).To(Equal(model.SecretTemplateOptions{Value: "root-val", SecretPath: "/"}))
	})

	It("returns an empty tree for empty context", func() {
		tree := v1.BuildSecretTree(map[string]model.SecretTemplateOptions{})
		Expect(tree).To(BeEmpty())
	})

	It("handles deeply nested paths", func() {
		ctx := map[string]model.SecretTemplateOptions{
			"DEEP": {Value: "deep-val", SecretPath: "/a/b/c/d"},
		}

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

	It("handles mixed root and nested secrets", func() {
		ctx := map[string]model.SecretTemplateOptions{
			"AT_ROOT":   {Value: "r", SecretPath: "/"},
			"IN_FOLDER": {Value: "f", SecretPath: "/app"},
		}

		tree := v1.BuildSecretTree(ctx)

		Expect(tree["AT_ROOT"]).To(Equal(model.SecretTemplateOptions{Value: "r", SecretPath: "/"}))
		app, ok := tree["app"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(app["IN_FOLDER"]).To(Equal(model.SecretTemplateOptions{Value: "f", SecretPath: "/app"}))
	})
})

var _ = Describe("RenderPerKeyTemplates without secretRef", func() {

	rootSecrets := map[string]model.SecretTemplateOptions{
		"DB_HOST": {Value: "localhost", SecretPath: "/"},
		"DB_PORT": {Value: "5432", SecretPath: "/"},
		"DB_NAME": {Value: "mydb", SecretPath: "/"},
	}

	It("resolves .Value accessor directly", func() {
		tmpls := map[string]string{
			"host": `{{ .DB_HOST.Value }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, rootSecrets)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("host", []byte("localhost")))
	})

	It("resolves .SecretPath accessor directly", func() {
		tmpls := map[string]string{
			"path": `{{ .DB_HOST.SecretPath }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, rootSecrets)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("path", []byte("/")))
	})

	It("combines multiple secrets in a single template key", func() {
		tmpls := map[string]string{
			"dsn": `postgresql://user:pass@{{ .DB_HOST.Value }}:{{ .DB_PORT.Value }}/{{ .DB_NAME.Value }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, rootSecrets)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("dsn", []byte("postgresql://user:pass@localhost:5432/mydb")))
	})
})

var _ = Describe("RenderBulkTemplate without secretRef", func() {

	rootSecrets := map[string]model.SecretTemplateOptions{
		"DB_HOST": {Value: "localhost", SecretPath: "/"},
		"DB_PORT": {Value: "5432", SecretPath: "/"},
	}

	It("resolves .Value accessor directly", func() {
		tmpl := `host: "{{ .DB_HOST.Value }}"`

		data, err := v1.RenderBulkTemplate(tmpl, rootSecrets)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("host", []byte("localhost")))
	})

	It("renders a range over all secrets", func() {
		tmpl := `{{- range $key, $secret := . }}
{{ $key }}: "{{ $secret.Value }}"
{{- end }}`

		data, err := v1.RenderBulkTemplate(tmpl, rootSecrets)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveLen(2))
		Expect(data).To(HaveKeyWithValue("DB_HOST", []byte("localhost")))
		Expect(data).To(HaveKeyWithValue("DB_PORT", []byte("5432")))
	})
})

var _ = Describe("RenderPerKeyTemplates with secretRef", func() {

	It("resolves value via secretRef with nested path", func() {
		tmpls := map[string]string{
			"host": `{{ secretRef "folder.subfolder.DB_HOST.value" }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, subfolderSecrets)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("host", []byte("prod-db.example.com")))
	})

	It("resolves secretPath via secretRef", func() {
		tmpls := map[string]string{
			"path": `{{ secretRef "folder.subfolder.DB_HOST.secretPath" }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, subfolderSecrets)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("path", []byte("/folder/subfolder")))
	})

	It("resolves secrets from different subfolders in the same template", func() {
		tmpls := map[string]string{
			"combined": `{{ secretRef "folder.subfolder.DB_HOST.value" }}:{{ secretRef "folder.subfolder.DB_PORT.value" }} key={{ secretRef "folder.other.API_KEY.value" }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, subfolderSecrets)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("combined", []byte("prod-db.example.com:5432 key=sk-secret-123")))
	})

	It("returns error for unknown accessor", func() {
		tmpls := map[string]string{
			"bad": `{{ secretRef "folder.subfolder.DB_HOST.unknown" }}`,
		}

		_, err := v1.RenderPerKeyTemplates(tmpls, subfolderSecrets)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unknown accessor"))
	})

	It("returns error for non-existent path segment", func() {
		tmpls := map[string]string{
			"bad": `{{ secretRef "missing.DB_HOST.value" }}`,
		}

		_, err := v1.RenderPerKeyTemplates(tmpls, subfolderSecrets)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not found"))
	})

	It("does not duplicate values when ranging over all secrets alongside secretRef", func() {
		tmpls := map[string]string{
			"ref_host": `{{ secretRef "folder.subfolder.DB_HOST.value" }}`,
			"ref_port": `{{ secretRef "folder.subfolder.DB_PORT.value" }}`,
			"ref_key":  `{{ secretRef "folder.other.API_KEY.value" }}`,
		}

		data, err := v1.RenderPerKeyTemplates(tmpls, subfolderSecrets)
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
		Expect(unique).To(HaveLen(3), "each secretRef should resolve to a distinct value")
	})
})

var _ = Describe("RenderBulkTemplate with secretRef", func() {

	It("returns error for non-existent path segment", func() {
		tmpl := `bad: "{{ secretRef "missing.DB_HOST.value" }}"`

		_, err := v1.RenderBulkTemplate(tmpl, subfolderSecrets)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not found"))
	})

	It("resolves value via secretRef", func() {
		tmpl := `host: "{{ secretRef "folder.subfolder.DB_HOST.value" }}"`

		data, err := v1.RenderBulkTemplate(tmpl, subfolderSecrets)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("host", []byte("prod-db.example.com")))
	})

	It("resolves secretPath via secretRef", func() {
		tmpl := `path: "{{ secretRef "folder.other.API_KEY.secretPath" }}"`

		data, err := v1.RenderBulkTemplate(tmpl, subfolderSecrets)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("path", []byte("/folder/other")))
	})

	It("resolves multiple secrets from different subfolders", func() {
		tmpl := `dsn: "{{ secretRef "folder.subfolder.DB_HOST.value" }}:{{ secretRef "folder.subfolder.DB_PORT.value" }}"
api_key: "{{ secretRef "folder.other.API_KEY.value" }}"`

		data, err := v1.RenderBulkTemplate(tmpl, subfolderSecrets)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveLen(2))
		Expect(data).To(HaveKeyWithValue("dsn", []byte("prod-db.example.com:5432")))
		Expect(data).To(HaveKeyWithValue("api_key", []byte("sk-secret-123")))
	})

	It("does not duplicate values when rendering all secrets via secretRef", func() {
		tmpl := `host: "{{ secretRef "folder.subfolder.DB_HOST.value" }}"
port: "{{ secretRef "folder.subfolder.DB_PORT.value" }}"
key: "{{ secretRef "folder.other.API_KEY.value" }}"`

		data, err := v1.RenderBulkTemplate(tmpl, subfolderSecrets)
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
		Expect(unique).To(HaveLen(3), "each secretRef should resolve to a distinct value")
	})
})
