package v1

import (
	"bytes"
	"fmt"
	"strings"
	tpl "text/template"

	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/template"
	"gopkg.in/yaml.v3"
)

func RenderPerKeyTemplates(tmpls map[string]string, ctx map[string]model.SecretTemplateOptions) (map[string][]byte, error) {
	data := make(map[string][]byte, len(tmpls))

	for key, tmplStr := range tmpls {
		tmpl, err := newTemplate(key, tmplStr, ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to parse template at key %q: %w", key, err)
		}

		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, ctx); err != nil {
			return nil, fmt.Errorf("failed to execute template at key %q: %w", key, err)
		}

		data[key] = buf.Bytes()
	}

	return data, nil
}

func RenderBulkTemplate(tmplStr string, ctx map[string]model.SecretTemplateOptions) (map[string][]byte, error) {
	tmpl, err := newTemplate("template.data", tmplStr, ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to parse bulk template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
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

func BuildSecretTree(ctx map[string]model.SecretTemplateOptions) map[string]any {
	root := make(map[string]any)

	for key, opts := range ctx {
		path := opts.SecretPath
		segments := strings.Split(strings.Trim(path, "/"), "/")

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

		node[key] = opts
	}

	return root
}

func newTemplate(name, templateString string, ctx map[string]model.SecretTemplateOptions) (*tpl.Template, error) {
	funcs := template.GetTemplateFunctions()
	tree := BuildSecretTree(ctx)
	funcs["resolveSecretFromPath"] = func(ref string) (string, error) {
		parts := strings.Split(ref, ".")
		if len(parts) < 2 {
			return "", fmt.Errorf("resolveSecretFromPath %q must have at least a secret name and accessor (value or secretPath)", ref)
		}

		accessor := parts[len(parts)-1]
		pathParts := parts[:len(parts)-1]

		var current any = tree
		for _, p := range pathParts {
			node, ok := current.(map[string]any)
			if !ok {
				return "", fmt.Errorf("resolveSecretFromPath %q: segment %q is not a folder", ref, p)
			}
			child, exists := node[p]
			if !exists {
				return "", fmt.Errorf("resolveSecretFromPath %q: segment %q not found", ref, p)
			}
			current = child
		}

		opts, ok := current.(model.SecretTemplateOptions)
		if !ok {
			return "", fmt.Errorf("resolveSecretFromPath %q: does not resolve to a secret", ref)
		}

		switch strings.ToLower(accessor) {
		case "value":
			return opts.Value, nil
		case "secretpath":
			return opts.SecretPath, nil
		default:
			return "", fmt.Errorf("resolveSecretFromPath %q: unknown accessor %q (use value or secretPath)", ref, accessor)
		}
	}
	return tpl.New(name).Funcs(funcs).Parse(templateString)
}
