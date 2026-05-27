/*
MIT License

Copyright (c) 2024 Infisical

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package v1beta1

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// SecretTemplateData accepts either:
//   - a map of per-key Go templates ({"FOO": "{{ .FOO.Value }}"}), or
//   - a single Go template string that, when rendered, yields a YAML map of
//     key/value pairs.
//
// Exactly one of Map / Raw will be populated after JSON unmarshalling. The
// per-key map form is the original v1beta1 behaviour and is preserved for
// backwards compatibility; the string form lets you template all keys at once
// (e.g. ranging over every fetched secret).
type SecretTemplateData struct {
	// Map holds per-key Go templates. Each entry becomes one key in the
	// resulting Secret/ConfigMap.
	Map map[string]string `json:"-"`

	// Raw holds a single Go template whose rendered output is YAML-decoded
	// into a map of key/value pairs.
	Raw string `json:"-"`
}

// IsMap reports whether the per-key map form was provided.
func (d *SecretTemplateData) IsMap() bool {
	return d != nil && d.Map != nil
}

// IsRaw reports whether the single-template string form was provided.
func (d *SecretTemplateData) IsRaw() bool {
	return d != nil && d.Raw != ""
}

// IsZero reports whether neither form was provided.
func (d *SecretTemplateData) IsZero() bool {
	return !d.IsMap() && !d.IsRaw()
}

// UnmarshalJSON accepts both a JSON object (per-key map) and a JSON string
// (single bulk template).
func (d *SecretTemplateData) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		d.Map = nil
		d.Raw = ""
		return nil
	}

	switch data[0] {
	case '"':
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("template.data: invalid string value: %w", err)
		}
		d.Map = nil
		d.Raw = s
		return nil
	case '{':
		var m map[string]string
		if err := json.Unmarshal(data, &m); err != nil {
			return fmt.Errorf("template.data: invalid map value: %w", err)
		}
		d.Map = m
		d.Raw = ""
		return nil
	default:
		return fmt.Errorf("template.data: expected string or object, got %q", string(data))
	}
}

// MarshalJSON emits whichever form is populated, defaulting to a JSON null
// when neither is set.
func (d SecretTemplateData) MarshalJSON() ([]byte, error) {
	if d.Raw != "" {
		return json.Marshal(d.Raw)
	}
	if d.Map != nil {
		return json.Marshal(d.Map)
	}
	return []byte("null"), nil
}
