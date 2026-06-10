package v1

import (
	"bytes"
	"fmt"
	"strings"
	tpl "text/template"

	"github.com/Infisical/infisical/k8-operator/internal/api"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/template"
	"gopkg.in/yaml.v3"
)

type TemplateContext struct {
	rawSecrets    []api.Secret
	mergedSecrets map[string]model.V1TemplateOptions
}

func NewTemplateContext(rawSecrets []api.Secret, mergedSecrets []api.Secret) TemplateContext {
	ctx := TemplateContext{
		rawSecrets:    rawSecrets,
		mergedSecrets: make(map[string]model.V1TemplateOptions, 0),
	}

	for _, s := range mergedSecrets {
		ctx.mergedSecrets[s.SecretKey] = model.V1TemplateOptions{
			Value:      s.SecretValue,
			SecretPath: s.SecretPath,
		}
	}

	return ctx
}

func RenderPerKeyTemplates(tmpls map[string]string, ctx TemplateContext) (map[string][]byte, error) {
	data := make(map[string][]byte, len(tmpls))

	for key, tmplStr := range tmpls {
		tmpl, err := newTemplate(key, tmplStr, ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to parse template at key %q: %w", key, err)
		}

		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, ctx.mergedSecrets); err != nil {
			return nil, fmt.Errorf("failed to execute template at key %q: %w", key, err)
		}

		data[key] = buf.Bytes()
	}

	return data, nil
}

func RenderBulkTemplate(tmplStr string, ctx TemplateContext) (map[string][]byte, error) {
	tmpl, err := newTemplate("template.data", tmplStr, ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to parse bulk template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx.mergedSecrets); err != nil {
		return nil, fmt.Errorf("failed to execute bulk template: %w", err)
	}

	rendered := bytes.TrimSpace(buf.Bytes())
	if len(rendered) == 0 {
		return map[string][]byte{}, nil
	}

	var parsed map[string]string
	if err := yaml.Unmarshal(rendered, &parsed); err != nil {
		return nil, fmt.Errorf("bulk template output is not a valid YAML map of key/value pairs: %w", err)
	}

	data := make(map[string][]byte, len(parsed))
	for k, v := range parsed {
		data[k] = []byte(v)
	}
	return data, nil
}

func BuildSecretTree(ctx TemplateContext) map[string]any {
	root := make(map[string]any)

	for _, s := range ctx.rawSecrets {
		segments := strings.Split(strings.Trim(s.SecretPath, "/"), "/")

		node := root
		for _, seg := range segments {
			if seg == "" {
				continue
			}
			child, exists := node[seg]
			if !exists {
				child = make(map[string]any)
				node[seg] = child
			}
			node = child.(map[string]any)
		}

		if _, exists := node[s.SecretKey]; !exists {
			node[s.SecretKey] = model.V1TemplateOptions{
				Value:      s.SecretValue,
				SecretPath: s.SecretPath,
			}
		}
	}

	return root
}

func newTemplate(name, templateString string, ctx TemplateContext) (*tpl.Template, error) {
	funcs := template.GetTemplateFunctions()
	tree := BuildSecretTree(ctx)
	funcs["getSecretByPath"] = func(ref string) (model.V1TemplateOptions, error) {
		parts := strings.Split(ref, "/")
		if len(parts) < 1 {
			return model.V1TemplateOptions{}, fmt.Errorf("getSecretByPath %q: path must not be empty", ref)
		}

		var current any = tree
		for _, p := range parts {
			node, ok := current.(map[string]any)
			if !ok {
				return model.V1TemplateOptions{}, fmt.Errorf("getSecretByPath %q: segment %q is not a folder", ref, p)
			}
			child, exists := node[p]
			if !exists {
				return model.V1TemplateOptions{}, fmt.Errorf("getSecretByPath %q: segment %q not found", ref, p)
			}
			current = child
		}

		opts, ok := current.(model.V1TemplateOptions)
		if !ok {
			return model.V1TemplateOptions{}, fmt.Errorf("getSecretByPath %q: does not resolve to a secret", ref)
		}

		return opts, nil
	}
	return tpl.New(name).Funcs(funcs).Parse(templateString)
}
