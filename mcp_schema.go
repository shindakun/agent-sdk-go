package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Tool arguments are checked against the tool's input schema before its
// handler runs, as the official SDK does with jsonschema; a failure is reported
// to the model as an "Input validation error: ..." result. This covers the
// JSON Schema keywords tool schemas use: type, properties, required,
// additionalProperties, items, enum, const, string length and pattern, numeric
// bounds, and array length. Messages follow python-jsonschema's wording.

// validationError carries python-jsonschema's wording, capitals included.
type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }

func validationErrorf(format string, a ...any) error {
	return &validationError{fmt.Sprintf(format, a...)}
}

// schemaError is an invalid schema (as opposed to invalid arguments).
type schemaError struct{ msg string }

func (e *schemaError) Error() string { return e.msg }

// validateToolArguments returns the first violation of schema by args, or nil.
// A schema that cannot be used returns a *schemaError.
func validateToolArguments(schema, args json.RawMessage) error {
	var s any
	if len(schema) == 0 {
		s = map[string]any{"type": "object"}
	} else if err := decodeNumbers(schema, &s); err != nil {
		return &schemaError{"invalid input schema: " + err.Error()}
	}
	var v any
	if len(args) == 0 {
		v = map[string]any{}
	} else if err := decodeNumbers(args, &v); err != nil {
		return fmt.Errorf("arguments are not valid JSON: %v", err)
	}
	return validateValue(s, v)
}

func decodeNumbers(b []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	return d.Decode(out)
}

var jsonSchemaTypes = map[string]bool{
	"string": true, "integer": true, "number": true, "boolean": true, "object": true, "array": true, "null": true,
}

func validateValue(schema, v any) error {
	s, ok := schema.(map[string]any)
	if !ok {
		if b, isBool := schema.(bool); isBool {
			if !b {
				return validationErrorf("False schema does not allow %s", pyRepr(v))
			}
			return nil
		}
		return &schemaError{fmt.Sprintf("%s is not of type 'object', 'boolean'", pyRepr(schema))}
	}

	if t, ok := s["type"]; ok {
		var types []string
		switch tt := t.(type) {
		case string:
			types = []string{tt}
		case []any:
			for _, x := range tt {
				name, _ := x.(string)
				types = append(types, name)
			}
		default:
			return &schemaError{fmt.Sprintf("%s is not valid under any of the given schemas", pyRepr(t))}
		}
		for _, name := range types {
			if !jsonSchemaTypes[name] {
				return &schemaError{fmt.Sprintf("%s is not valid under any of the given schemas", pyRepr(t))}
			}
		}
		matched := false
		for _, name := range types {
			if isJSONType(v, name) {
				matched = true
				break
			}
		}
		if !matched {
			quoted := make([]string, len(types))
			for i, name := range types {
				quoted[i] = "'" + name + "'"
			}
			return fmt.Errorf("%s is not of type %s", pyRepr(v), strings.Join(quoted, ", "))
		}
	}

	if enum, ok := s["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			if jsonEqual(e, v) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s is not one of %s", pyRepr(v), pyRepr(enum))
		}
	}
	if c, ok := s["const"]; ok && !jsonEqual(c, v) {
		return fmt.Errorf("%s was expected", pyRepr(c))
	}

	switch val := v.(type) {
	case string:
		n := utf8.RuneCountInString(val)
		if min, ok := schemaInt(s["minLength"]); ok && n < min {
			if min == 1 {
				return fmt.Errorf("%s should be non-empty", pyRepr(v))
			}
			return fmt.Errorf("%s is too short", pyRepr(v))
		}
		if max, ok := schemaInt(s["maxLength"]); ok && n > max {
			return fmt.Errorf("%s is too long", pyRepr(v))
		}
		if p, ok := s["pattern"].(string); ok {
			re, err := regexp.Compile(p)
			if err != nil {
				return &schemaError{fmt.Sprintf("%s is not a 'regex'", pyRepr(p))}
			}
			if !re.MatchString(val) {
				return fmt.Errorf("%s does not match %s", pyRepr(v), pyRepr(p))
			}
		}
	case json.Number:
		f, _ := val.Float64()
		if m, ok := schemaFloat(s["minimum"]); ok && f < m {
			return fmt.Errorf("%s is less than the minimum of %s", pyRepr(v), pyRepr(s["minimum"]))
		}
		if m, ok := schemaFloat(s["maximum"]); ok && f > m {
			return fmt.Errorf("%s is greater than the maximum of %s", pyRepr(v), pyRepr(s["maximum"]))
		}
		if m, ok := schemaFloat(s["exclusiveMinimum"]); ok && f <= m {
			return fmt.Errorf("%s is less than or equal to the minimum of %s", pyRepr(v), pyRepr(s["exclusiveMinimum"]))
		}
		if m, ok := schemaFloat(s["exclusiveMaximum"]); ok && f >= m {
			return fmt.Errorf("%s is greater than or equal to the maximum of %s", pyRepr(v), pyRepr(s["exclusiveMaximum"]))
		}
	case []any:
		if min, ok := schemaInt(s["minItems"]); ok && len(val) < min {
			if min == 1 {
				return fmt.Errorf("%s should be non-empty", pyRepr(v))
			}
			return fmt.Errorf("%s is too short", pyRepr(v))
		}
		if max, ok := schemaInt(s["maxItems"]); ok && len(val) > max {
			return fmt.Errorf("%s is too long", pyRepr(v))
		}
		if items, ok := s["items"]; ok {
			for _, item := range val {
				if err := validateValue(items, item); err != nil {
					return err
				}
			}
		}
	case map[string]any:
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				name, _ := r.(string)
				if _, present := val[name]; !present {
					return fmt.Errorf("%s is a required property", pyRepr(name))
				}
			}
		}
		props, _ := s["properties"].(map[string]any)
		for _, k := range sortedKeys(val) {
			if ps, ok := props[k]; ok {
				if err := validateValue(ps, val[k]); err != nil {
					return err
				}
			}
		}
		switch ap := s["additionalProperties"].(type) {
		case bool:
			if !ap {
				var extra []string
				for _, k := range sortedKeys(val) {
					if _, ok := props[k]; !ok {
						extra = append(extra, pyRepr(k))
					}
				}
				if len(extra) == 1 {
					return validationErrorf("Additional properties are not allowed (%s was unexpected)", extra[0])
				}
				if len(extra) > 1 {
					return validationErrorf("Additional properties are not allowed (%s were unexpected)", strings.Join(extra, ", "))
				}
			}
		case map[string]any:
			for _, k := range sortedKeys(val) {
				if _, ok := props[k]; !ok {
					if err := validateValue(ap, val[k]); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func isJSONType(v any, name string) bool {
	switch name {
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "number":
		_, ok := v.(json.Number)
		return ok
	case "integer":
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		f, err := n.Float64()
		return err == nil && f == math.Trunc(f) && !math.IsInf(f, 0)
	}
	return false
}

func jsonEqual(a, b any) bool {
	na, aNum := a.(json.Number)
	nb, bNum := b.(json.Number)
	if aNum && bNum {
		fa, _ := na.Float64()
		fb, _ := nb.Float64()
		return fa == fb
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return bytes.Equal(ja, jb)
}

func schemaInt(v any) (int, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	return int(i), err == nil
}

func schemaFloat(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// pyRepr renders a decoded JSON value the way Python's repr does, which is how
// python-jsonschema quotes values in its messages.
func pyRepr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case string:
		if strings.Contains(x, "'") && !strings.Contains(x, `"`) {
			return `"` + x + `"`
		}
		return "'" + strings.ReplaceAll(strings.ReplaceAll(x, `\`, `\\`), "'", `\'`) + "'"
	case json.Number:
		s := x.String()
		if strings.ContainsAny(s, ".eE") {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return strconv.FormatFloat(f, 'f', -1, 64)
			}
		}
		return s
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = pyRepr(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := sortedKeys(x)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = pyRepr(k) + ": " + pyRepr(x[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprint(v)
}
