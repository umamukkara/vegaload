package openapi

import (
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// maxSchemaDepth stops a schema that refers to itself.
const maxSchemaDepth = 6

// fillDetails reads the parameters, request body, success status and
// security of one operation into ep.
func fillDetails(ep *Endpoint, doc *rawDocument, op *rawOp, pathParams []rawParam) {
	// Parameters: the operation's override the path item's of the same
	// name and place.
	seen := map[string]int{}
	add := func(rp rawParam) {
		if rp.Ref != "" {
			name := rp.Ref[strings.LastIndex(rp.Ref, "/")+1:]
			if c, ok := doc.Components.Parameters[name]; ok {
				rp = c
			}
		}
		if rp.Name == "" || (rp.In != "path" && rp.In != "query" && rp.In != "header") {
			return
		}
		ex, isNum := paramExample(rp, doc)
		p := Param{Name: rp.Name, In: rp.In, Required: rp.Required || rp.In == "path", Example: ex, Integer: isNum}
		key := rp.In + ":" + rp.Name
		if i, ok := seen[key]; ok {
			ep.Params[i] = p
			return
		}
		seen[key] = len(ep.Params)
		ep.Params = append(ep.Params, p)
	}
	for _, rp := range pathParams {
		add(rp)
	}
	for _, rp := range op.Parameters {
		add(rp)
	}

	// Request body.
	if rb := op.RequestBody; rb != nil {
		content := rb.Content
		if rb.Ref != "" && content == nil {
			name := rb.Ref[strings.LastIndex(rb.Ref, "/")+1:]
			if raw, ok := doc.Components.RequestBodies[name]; ok {
				var resolved struct {
					Content map[string]struct {
						Schema   any `json:"schema"`
						Example  any `json:"example"`
						Examples map[string]struct {
							Value any `json:"value"`
						} `json:"examples"`
					} `json:"content"`
				}
				if json.Unmarshal(raw, &resolved) == nil {
					for ct, c := range resolved.Content {
						if content == nil {
							content = map[string]struct {
								Schema   any `json:"schema"`
								Example  any `json:"example"`
								Examples map[string]struct {
									Value any `json:"value"`
								} `json:"examples"`
							}{}
						}
						content[ct] = struct {
							Schema   any `json:"schema"`
							Example  any `json:"example"`
							Examples map[string]struct {
								Value any `json:"value"`
							} `json:"examples"`
						}(c)
					}
				}
			}
		}
		cts := make([]string, 0, len(content))
		for ct := range content {
			cts = append(cts, ct)
		}
		sort.Strings(cts)
		for _, ct := range cts {
			lower := strings.ToLower(ct)
			if lower == "application/json" || strings.HasSuffix(lower, "+json") {
				c := content[ct]
				switch {
				case c.Example != nil:
					ep.Body = c.Example
				default:
					found := false
					names := make([]string, 0, len(c.Examples))
					for n := range c.Examples {
						names = append(names, n)
					}
					sort.Strings(names)
					for _, n := range names {
						if v := c.Examples[n].Value; v != nil {
							ep.Body, found = v, true
							break
						}
					}
					if !found {
						ep.Body = exampleOf(c.Schema, doc, 0, nil)
					}
				}
				ep.HasBody = true
				break
			}
		}
		if !ep.HasBody && len(cts) > 0 {
			ep.OtherBody = cts[0]
		}
	}

	// The first 2xx response the spec declares.
	codes := make([]string, 0, len(op.Responses))
	for c := range op.Responses {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	for _, c := range codes {
		if n, err := strconv.Atoi(c); err == nil && n >= 200 && n < 300 {
			ep.Success = n
			break
		}
	}

	// Security: the operation's own list wins, even when empty.
	reqs := doc.Security
	if op.Security != nil {
		reqs = *op.Security
	}
	ep.Auth = authOf(reqs, doc)
}

// authOf picks how to authenticate from a list of security requirements. The
// first scheme of the first requirement is used.
func authOf(reqs []map[string][]string, doc *rawDocument) Auth {
	for _, req := range reqs {
		names := make([]string, 0, len(req))
		for n := range req {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			sch, ok := doc.Components.SecuritySchemes[n]
			if !ok {
				continue
			}
			switch strings.ToLower(sch.Type) {
			case "http":
				if strings.EqualFold(sch.Scheme, "basic") {
					return Auth{Kind: AuthBasic}
				}
				return Auth{Kind: AuthBearer}
			case "apikey":
				if strings.EqualFold(sch.In, "header") && sch.Name != "" {
					return Auth{Kind: AuthAPIKey, Header: sch.Name}
				}
			case "oauth2", "openidconnect":
				return Auth{Kind: AuthBearer}
			}
		}
	}
	return Auth{}
}

// paramExample returns a sample value for a parameter, and whether its type
// is numeric.
func paramExample(rp rawParam, doc *rawDocument) (string, bool) {
	typ := schemaType(rp.Schema, doc)
	num := typ == "integer" || typ == "number"
	if rp.Example != nil {
		return scalarText(rp.Example), num
	}
	v := exampleOf(rp.Schema, doc, 0, nil)
	if s := scalarText(v); s != "" {
		return s, num
	}
	if num {
		return "1", true
	}
	return "example", false
}

// scalarText writes a scalar example as text. A list or object gives "".
func scalarText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	case []any:
		if len(x) > 0 {
			return scalarText(x[0])
		}
	}
	return ""
}

// deref follows a local $ref ("#/components/schemas/Name").
func deref(node map[string]any, doc *rawDocument) map[string]any {
	for i := 0; i < maxSchemaDepth; i++ {
		ref, ok := node["$ref"].(string)
		if !ok {
			return node
		}
		name := ref[strings.LastIndex(ref, "/")+1:]
		next, ok := doc.Components.Schemas[name].(map[string]any)
		if !ok {
			return map[string]any{}
		}
		node = next
	}
	return node
}

func schemaType(schema any, doc *rawDocument) string {
	m, ok := schema.(map[string]any)
	if !ok {
		return ""
	}
	m = deref(m, doc)
	if t, ok := m["type"].(string); ok {
		return t
	}
	if _, ok := m["properties"]; ok {
		return "object"
	}
	return ""
}

// exampleOf builds a sample value for a schema: the schema's own example,
// default or first enum value, or else one made from its type.
//
// refs holds the schemas being expanded, so a schema that contains itself is
// left out where it repeats, instead of nested to the depth limit.
func exampleOf(schema any, doc *rawDocument, depth int, refs map[string]bool) any {
	m, ok := schema.(map[string]any)
	if !ok || depth > maxSchemaDepth {
		return nil
	}
	if ref, ok := m["$ref"].(string); ok {
		name := ref[strings.LastIndex(ref, "/")+1:]
		if refs[name] {
			return nil
		}
		next := map[string]bool{name: true}
		for k := range refs {
			next[k] = true
		}
		refs = next
	}
	m = deref(m, doc)
	if v, ok := m["example"]; ok {
		return v
	}
	if v, ok := m["default"]; ok {
		return v
	}
	if enum, ok := m["enum"].([]any); ok && len(enum) > 0 {
		return enum[0]
	}
	for _, k := range []string{"allOf"} {
		if parts, ok := m[k].([]any); ok {
			merged := map[string]any{}
			for _, part := range parts {
				if pm, ok := exampleOf(part, doc, depth+1, refs).(map[string]any); ok {
					for pk, pv := range pm {
						merged[pk] = pv
					}
				}
			}
			return merged
		}
	}
	for _, k := range []string{"oneOf", "anyOf"} {
		if parts, ok := m[k].([]any); ok && len(parts) > 0 {
			return exampleOf(parts[0], doc, depth+1, refs)
		}
	}
	switch schemaType(m, doc) {
	case "object":
		out := map[string]any{}
		props, _ := m["properties"].(map[string]any)
		for name, prop := range props {
			if pm, ok := prop.(map[string]any); ok {
				if ro, _ := deref(pm, doc)["readOnly"].(bool); ro {
					continue
				}
			}
			if v := exampleOf(prop, doc, depth+1, refs); v != nil {
				out[name] = v
			}
		}
		return out
	case "array":
		item := exampleOf(m["items"], doc, depth+1, refs)
		if item == nil {
			return []any{}
		}
		return []any{item}
	case "integer":
		if min, ok := m["minimum"].(json.Number); ok {
			return min
		}
		return json.Number("1")
	case "number":
		if min, ok := m["minimum"].(json.Number); ok {
			return min
		}
		return json.Number("1.5")
	case "boolean":
		return true
	case "string":
		switch m["format"] {
		case "date-time":
			return "2026-01-01T00:00:00Z"
		case "date":
			return "2026-01-01"
		case "uuid":
			return "123e4567-e89b-12d3-a456-426614174000"
		case "email":
			return "user@example.com"
		case "uri", "url":
			return "https://example.com"
		case "byte":
			return "AAAA"
		}
		return "string"
	}
	return nil
}

// queryString joins the required query parameters of ep.
func queryString(ep Endpoint) string {
	var parts []string
	for _, p := range ep.Params {
		if p.In == "query" && p.Required {
			parts = append(parts, url.QueryEscape(p.Name)+"="+url.QueryEscape(p.Example))
		}
	}
	return strings.Join(parts, "&")
}
