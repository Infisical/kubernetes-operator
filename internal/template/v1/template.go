package v1

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	tpl "text/template"

	"github.com/Infisical/infisical/k8-operator/internal/api"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/template"
	"gopkg.in/yaml.v3"
)

type TemplateContext struct {
	rawSecrets      []api.Secret
	importedSecrets []api.Secret
	mergedSecrets   map[string]model.V1TemplateOptions
}

type RenderContext struct {
	MergedSecrets   []api.Secret
	RawSecrets      []api.Secret
	ImportedSecrets []api.Secret
}

func NewTemplateContext(renderCtx RenderContext) TemplateContext {
	ctx := TemplateContext{
		rawSecrets:      renderCtx.RawSecrets,
		importedSecrets: renderCtx.ImportedSecrets,
		mergedSecrets:   make(map[string]model.V1TemplateOptions, 0),
	}

	for _, s := range renderCtx.MergedSecrets {
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

type SecretTreeNode struct {
	Secret   *model.V1TemplateOptions
	Children map[string]*SecretTreeNode
}

func (n *SecretTreeNode) getOrCreateChild(key string) *SecretTreeNode {
	if n.Children == nil {
		n.Children = make(map[string]*SecretTreeNode)
	}
	child, exists := n.Children[key]
	if !exists {
		child = &SecretTreeNode{}
		n.Children[key] = child
	}
	return child
}

func BuildSecretTree(ctx TemplateContext) *SecretTreeNode {
	root := &SecretTreeNode{}

	for _, secrets := range [][]api.Secret{ctx.rawSecrets, ctx.importedSecrets} {
		for _, s := range secrets {
			segments := strings.Split(strings.Trim(s.SecretPath, "/"), "/")

			node := root
			for _, seg := range segments {
				if seg == "" {
					continue
				}
				node = node.getOrCreateChild(seg)
			}

			leaf := node.getOrCreateChild(s.SecretKey)
			if leaf.Secret == nil {
				leaf.Secret = &model.V1TemplateOptions{
					Value:      s.SecretValue,
					SecretPath: s.SecretPath,
				}
			}
		}
	}

	return root
}

func newTemplate(name, templateString string, ctx TemplateContext) (*tpl.Template, error) {
	funcs := template.GetTemplateFunctions()
	tree := BuildSecretTree(ctx)
	funcs["secretFrom"] = func(secretPath, secretName string) (model.V1TemplateOptions, error) {
		current := tree
		for _, seg := range strings.Split(strings.Trim(secretPath, "/"), "/") {
			if seg == "" {
				continue
			}
			if current.Children == nil {
				return model.V1TemplateOptions{}, fmt.Errorf("secretFrom: folder path %q not found", seg)
			}
			child, exists := current.Children[seg]
			if !exists {
				return model.V1TemplateOptions{}, fmt.Errorf("secretFrom: folder path %q not found", seg)
			}
			current = child
		}

		if current.Children == nil {
			return model.V1TemplateOptions{}, fmt.Errorf("secretFrom: secret %q not found", secretName)
		}
		child, exists := current.Children[secretName]
		if !exists {
			return model.V1TemplateOptions{}, fmt.Errorf("secretFrom: secret %q not found", secretName)
		}
		if child.Secret == nil {
			return model.V1TemplateOptions{}, fmt.Errorf("secretFrom: %q does not resolve to a secret", secretName)
		}
		return *child.Secret, nil
	}
	funcs["foldersIn"] = func(dir string) []model.V1Folder {
		current := tree
		for _, seg := range strings.Split(strings.Trim(dir, "/"), "/") {
			if seg == "" {
				continue
			}
			if current.Children == nil {
				return []model.V1Folder{}
			}
			child, exists := current.Children[seg]
			if !exists {
				return []model.V1Folder{}
			}
			current = child
		}

		basePath := "/" + strings.Trim(dir, "/")

		result := make([]model.V1Folder, 0)
		for childName, child := range current.Children {
			if len(child.Children) == 0 {
				// A pure leaf is a secret, not a subdirectory. A node may carry
				// both a Secret and Children when a secret key name collides with
				// a folder segment; such a node is still a valid subdirectory.
				continue
			}
			result = append(result, model.V1Folder{
				Name: childName,
				Path: strings.TrimRight(basePath, "/") + "/" + childName,
			})
		}

		sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
		return result
	}
	return tpl.New(name).Funcs(funcs).Parse(templateString)
}
