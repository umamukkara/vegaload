package openapi

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Language of a generated scenario.
const (
	JavaScript = "js"
	Python     = "python"
)

// defaultBase is used when the spec names no absolute server.
const defaultBase = "http://127.0.0.1:8080"

// RenderScenario writes a runnable scenario that calls every operation of
// the spec once per iteration, each as a named step (so the summary shows
// each endpoint's latency and errors, and a threshold can target one).
//
// A spec describes each endpoint on its own, so the order and the data flow
// are chosen by simple rules, and the generated file says so. Creates run
// first, then reads, then updates, and deletes run last. When a create
// (POST) returns an object with an id, the calls under it (the paths that
// extend its own, such as /widgets/{id}) use that id. A path parameter with
// no create to take it from gets a placeholder, which the file marks.
//
// lang is JavaScript or Python.
func (s *Spec) RenderScenario(name, lang string) string {
	p := s.plan()
	if lang == Python {
		return p.python(s, name)
	}
	return p.javascript(s, name)
}

// seg is one piece of a request path: literal text, or a path parameter.
type seg struct {
	Lit      string
	Param    string // the parameter's name, when this is a parameter
	Key      string // the path template before the parameter, where its id is captured
	Fallback string
}

type step struct {
	Name     string
	Ep       Endpoint
	Segs     []seg
	Query    string
	Headers  [][2]string
	Capture  string   // the key to store a created id under, or ""
	Fields   []string // the response fields that may hold the id
	Notes    []string
	Expected int
}

type scenarioPlan struct {
	Steps []step
	Auth  Auth // the first secured operation's scheme
}

// plan decides the order and the wiring of the steps.
func (s *Spec) plan() scenarioPlan {
	eps := append([]Endpoint(nil), s.Endpoints...)
	type ranked struct {
		ep    Endpoint
		class int
		depth int
		idx   int
	}
	rs := make([]ranked, len(eps))
	for i, ep := range eps {
		depth := strings.Count(ep.Path, "{")
		class := 3
		switch ep.Method {
		case "POST":
			class = 0
		case "GET":
			class = 1
		case "PUT", "PATCH":
			class = 2
		case "DELETE":
			class, depth = 4, -depth
		}
		rs[i] = ranked{ep, class, depth, i}
	}
	sort.SliceStable(rs, func(a, b int) bool {
		if rs[a].class != rs[b].class {
			return rs[a].class < rs[b].class
		}
		if rs[a].depth != rs[b].depth {
			return rs[a].depth < rs[b].depth
		}
		return rs[a].idx < rs[b].idx
	})

	// The collections a create can supply ids for.
	creates := map[string]bool{}
	for _, ep := range eps {
		if ep.Method == "POST" {
			creates[trimSlash(ep.Path)] = true
		}
	}

	var plan scenarioPlan
	used := map[string]int{}
	for _, r := range rs {
		ep := r.ep
		st := step{Ep: ep, Expected: ep.Success, Query: queryString(ep)}
		st.Name = stepName(ep, used)
		st.Segs = pathSegs(ep, creates, &st.Notes)
		if ep.Auth.Kind != AuthNone && plan.Auth.Kind == AuthNone {
			plan.Auth = ep.Auth
		}
		for _, p := range ep.Params {
			if p.In == "header" && p.Required && !strings.EqualFold(p.Name, "authorization") &&
				!strings.EqualFold(p.Name, "content-type") && !strings.EqualFold(p.Name, "accept") {
				st.Headers = append(st.Headers, [2]string{p.Name, p.Example})
			}
		}
		if !ep.HasBody && ep.OtherBody == "" && (ep.Method == "POST" || ep.Method == "PUT" || ep.Method == "PATCH") {
			st.Notes = append(st.Notes, "the spec declares no request body for this operation. If it needs one, add it")
		}
		if ep.OtherBody != "" {
			st.Notes = append(st.Notes, fmt.Sprintf("this operation takes %s, which is not generated. Add the body", ep.OtherBody))
		}
		if ep.Method == "POST" {
			key := trimSlash(ep.Path)
			fields := captureFields(key, eps)
			if len(fields) > 0 {
				st.Capture, st.Fields = key, fields
			}
		}
		plan.Steps = append(plan.Steps, st)
	}
	return plan
}

func trimSlash(p string) string {
	if len(p) > 1 {
		return strings.TrimSuffix(p, "/")
	}
	return p
}

// captureFields lists the response fields that may hold the id of what a POST
// on key created: "id", and the name of the path parameter that follows key in
// another path.
func captureFields(key string, eps []Endpoint) []string {
	var out []string
	seen := map[string]bool{}
	for _, ep := range eps {
		rest, ok := strings.CutPrefix(ep.Path, key+"/{")
		if !ok {
			continue
		}
		end := strings.Index(rest, "}")
		if end <= 0 {
			continue
		}
		name := rest[:end]
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	if len(out) > 0 && !seen["id"] {
		out = append(out, "id")
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i] == "id" && out[j] != "id" })
	return out
}

// pathSegs splits a path into literals and parameters.
func pathSegs(ep Endpoint, creates map[string]bool, notes *[]string) []seg {
	var segs []seg
	rest := ep.Path
	consumed := 0
	for {
		i := strings.Index(rest, "{")
		if i < 0 {
			break
		}
		j := strings.Index(rest[i:], "}")
		if j < 0 {
			break
		}
		name := rest[i+1 : i+j]
		segs = append(segs, seg{Lit: rest[:i]})
		key := trimSlash(ep.Path[:consumed+i])
		fallback := "1"
		for _, p := range ep.Params {
			if p.In == "path" && p.Name == name {
				fallback = p.Example
			}
		}
		sg := seg{Param: name, Key: key, Fallback: fallback}
		if !creates[key] {
			*notes = append(*notes, fmt.Sprintf("{%s} has no create call to take it from, so %q is a placeholder. Set a real id", name, fallback))
		}
		segs = append(segs, sg)
		consumed += i + j + 1
		rest = rest[i+j+1:]
	}
	if rest != "" {
		segs = append(segs, seg{Lit: rest})
	}
	return segs
}

// stepName is the operationId, or "METHOD /path" with {x} written :x. It is
// unique in the file.
func stepName(ep Endpoint, used map[string]int) string {
	name := ep.OperationID
	if name == "" {
		p := ep.Path
		for {
			i := strings.Index(p, "{")
			j := strings.Index(p, "}")
			if i < 0 || j < i {
				break
			}
			p = p[:i] + ":" + p[i+1:j] + p[j+1:]
		}
		name = ep.Method + " " + p
	}
	name = strings.NewReplacer(`"`, "", "'", "", "\n", " ", "\\", "").Replace(name)
	used[name]++
	if n := used[name]; n > 1 {
		name += " (" + strconv.Itoa(n) + ")"
	}
	return name
}

func (s *Spec) base() string {
	switch {
	case strings.HasPrefix(s.BaseURL, "http://"), strings.HasPrefix(s.BaseURL, "https://"):
		return s.BaseURL
	case s.BaseURL == "":
		return defaultBase
	}
	return defaultBase + "/" + strings.TrimPrefix(s.BaseURL, "/")
}

func (s *Spec) baseNote() string {
	if strings.HasPrefix(s.BaseURL, "http://") || strings.HasPrefix(s.BaseURL, "https://") {
		return ""
	}
	return "The spec names no absolute server, so " + s.base() + " is a placeholder. Set the real one."
}

// q quotes text as a string literal that both JavaScript and Python read.
func q(s string) string { return strconv.Quote(s) }

func (st step) comment() string {
	c := st.Ep.Method + " " + st.Ep.Path
	if st.Ep.Summary != "" {
		c += ": " + st.Ep.Summary
	}
	return c
}

func (st step) expected() int {
	if st.Expected > 0 {
		return st.Expected
	}
	return 200
}

const generatedNote = "Generated by `vegaload new -from-openapi`. A spec describes each endpoint on its own, so the order and the data flow are a guess: creates first, then reads, then updates, then deletes. When a create returns an id, the calls under it use that id. Edit this file to fit the real flow."

// ---- JavaScript ----

func (p scenarioPlan) javascript(s *Spec, name string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// %s -- a VegaLoad scenario for \"%s\".\n//\n", name, s.Title)
	writeWrapped(&b, "// ", generatedNote)
	fmt.Fprintf(&b, "//\n// Run it:\n//   vegaload run -vus 5 -duration 30s %s\n", name)
	if p.Auth.Kind != AuthNone {
		b.WriteString("// Give it the credential with -secret-env, for example: -secret-env API_TOKEN\n")
	}
	b.WriteString("// Each operation is a named step(), so the summary shows each one's latency and\n// errors, and a threshold can target one:\n//   -threshold 'p95{step=\"...\"} < 300ms'\n")
	fmt.Fprintf(&b, "\nconst base = %s;\n", q(s.base()))
	if n := s.baseNote(); n != "" {
		fmt.Fprintf(&b, "// TODO: %s\n", n)
	}
	b.WriteString(`
// pathId is the id a create returned for the collection at key, or the
// placeholder when there is none.
function pathId(ids, key, fallback) {
  return ids[key] !== undefined ? encodeURIComponent(ids[key]) : fallback;
}

// capture remembers the id in a create's reply, under the collection's path.
function capture(ids, key, r, fields) {
  try {
    const body = r.json();
    if (body && typeof body === "object") {
      for (const f of fields) {
        if (body[f] !== undefined) { ids[key] = body[f]; return; }
      }
    }
  } catch (e) {
    // The reply was not JSON. The calls under it use their placeholders.
  }
}
`)
	switch p.Auth.Kind {
	case AuthBearer:
		b.WriteString("\nconst token = env.API_TOKEN;\nfunction auth() {\n  return token ? { Authorization: \"Bearer \" + token } : {};\n}\n")
	case AuthBasic:
		b.WriteString("\n// Basic authentication: give the base64 of user:password as API_BASIC.\nconst token = env.API_BASIC;\nfunction auth() {\n  return token ? { Authorization: \"Basic \" + token } : {};\n}\n")
	case AuthAPIKey:
		fmt.Fprintf(&b, "\nconst token = env.API_KEY;\nfunction auth() {\n  return token ? { %s: token } : {};\n}\n", q(p.Auth.Header))
	}
	b.WriteString("\nexport default function () {\n  const ids = {};\n")
	for _, st := range p.Steps {
		b.WriteString("\n")
		fmt.Fprintf(&b, "  // %s\n", st.comment())
		for _, n := range st.Notes {
			fmt.Fprintf(&b, "  // TODO: %s.\n", n)
		}
		fmt.Fprintf(&b, "  step(%s, () => {\n", q(st.Name))
		fmt.Fprintf(&b, "    const r = http.%s(%s", strings.ToLower(st.Ep.Method), st.jsURL())
		if opts := st.jsOptions(); opts != "" {
			fmt.Fprintf(&b, ", %s", opts)
		}
		b.WriteString(");\n")
		fmt.Fprintf(&b, "    check(r, { %s: (x) => x.status === %d });\n", q(fmt.Sprintf("%s: status %d", st.Name, st.expected())), st.expected())
		fmt.Fprintf(&b, "    if (r.status < 200 || r.status >= 300) {\n      throw new Error(%s + r.status);\n    }\n", q(st.Name+": status "))
		if st.Capture != "" {
			fmt.Fprintf(&b, "    capture(ids, %s, r, %s);\n", q(st.Capture), jsList(st.Fields))
		}
		b.WriteString("  });\n")
	}
	b.WriteString("}\n")
	return b.String()
}

func (st step) jsURL() string {
	parts := []string{"base"}
	for _, sg := range st.Segs {
		if sg.Param != "" {
			parts = append(parts, fmt.Sprintf("pathId(ids, %s, %s)", q(sg.Key), q(sg.Fallback)))
		} else if sg.Lit != "" {
			parts = append(parts, q(sg.Lit))
		}
	}
	if st.Query != "" {
		parts = append(parts, q("?"+st.Query))
	}
	return strings.Join(parts, " + ")
}

func (st step) jsOptions() string {
	var fields []string
	var hdr []string
	if st.Ep.HasBody {
		hdr = append(hdr, `"Content-Type": "application/json"`)
	}
	for _, h := range st.Headers {
		hdr = append(hdr, q(h[0])+": "+q(h[1]))
	}
	headers := ""
	if len(hdr) > 0 {
		headers = "{ " + strings.Join(hdr, ", ") + " }"
	}
	if st.Ep.Auth.Kind != AuthNone {
		if headers == "" {
			headers = "auth()"
		} else {
			headers = "Object.assign({}, auth(), " + headers + ")"
		}
	}
	if headers != "" {
		fields = append(fields, "headers: "+headers)
	}
	if st.Ep.HasBody {
		fields = append(fields, "body: JSON.stringify("+jsLiteral(st.Ep.Body, 3)+")")
	}
	switch len(fields) {
	case 0:
		return ""
	case 1:
		return "{ " + fields[0] + " }"
	}
	return "{\n      " + strings.Join(fields, ",\n      ") + ",\n    }"
}

func jsList(items []string) string {
	qs := make([]string, len(items))
	for i, it := range items {
		qs[i] = q(it)
	}
	return "[" + strings.Join(qs, ", ") + "]"
}

// jsLiteral writes v as JSON, indented to depth levels of two spaces.
func jsLiteral(v any, depth int) string {
	b, err := json.MarshalIndent(v, strings.Repeat("  ", depth), "  ")
	if err != nil {
		return "null"
	}
	return string(b)
}

// ---- Python ----

func (p scenarioPlan) python(s *Spec, name string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s -- a VegaLoad scenario for \"%s\".\n#\n", name, s.Title)
	writeWrapped(&b, "# ", generatedNote)
	fmt.Fprintf(&b, "#\n# Run it:\n#   vegaload run -vus 5 -duration 30s %s\n", name)
	if p.Auth.Kind != AuthNone {
		b.WriteString("# Give it the credential with -secret-env, for example: -secret-env API_TOKEN\n")
	}
	b.WriteString("# Each operation is a named step, so the summary shows each one's latency and\n# errors, and a threshold can target one:\n#   -threshold 'p95{step=\"...\"} < 300ms'\n")
	b.WriteString("import json\n")
	fmt.Fprintf(&b, "\nBASE = %s\n", q(s.base()))
	if n := s.baseNote(); n != "" {
		fmt.Fprintf(&b, "# TODO: %s\n", n)
	}
	b.WriteString(`

def _path_id(ids, key, fallback):
    """The id a create returned for the collection at key, or the placeholder."""
    if key in ids:
        return str(ids[key])
    return fallback


def _capture(ids, key, r, fields):
    """Remember the id in a create's reply, under the collection's path."""
    try:
        body = r.json()
    except Exception:
        return  # not JSON: the calls under it use their placeholders
    if isinstance(body, dict):
        for f in fields:
            if f in body:
                ids[key] = body[f]
                return
`)
	switch p.Auth.Kind {
	case AuthBearer:
		b.WriteString("\n\ndef _auth():\n    token = env.get(\"API_TOKEN\")\n    return {\"Authorization\": \"Bearer \" + token} if token else {}\n")
	case AuthBasic:
		b.WriteString("\n\n# Basic authentication: give the base64 of user:password as API_BASIC.\ndef _auth():\n    token = env.get(\"API_BASIC\")\n    return {\"Authorization\": \"Basic \" + token} if token else {}\n")
	case AuthAPIKey:
		fmt.Fprintf(&b, "\n\ndef _auth():\n    token = env.get(\"API_KEY\")\n    return {%s: token} if token else {}\n", q(p.Auth.Header))
	}
	b.WriteString("\n\ndef iteration():\n    ids = {}\n")
	for _, st := range p.Steps {
		b.WriteString("\n")
		fmt.Fprintf(&b, "    # %s\n", st.comment())
		for _, n := range st.Notes {
			fmt.Fprintf(&b, "    # TODO: %s.\n", n)
		}
		fmt.Fprintf(&b, "    with step(%s):\n", q(st.Name))
		if st.Ep.HasBody {
			fmt.Fprintf(&b, "        r = http.%s(\n            %s,%s)\n", strings.ToLower(st.Ep.Method), st.pyURL(), st.pyOptions())
		} else {
			fmt.Fprintf(&b, "        r = http.%s(%s", strings.ToLower(st.Ep.Method), st.pyURL())
			if opts := st.pyOptions(); opts != "" {
				fmt.Fprintf(&b, ", %s", opts)
			}
			b.WriteString(")\n")
		}
		fmt.Fprintf(&b, "        check(r, {%s: lambda x: x.status == %d})\n", q(fmt.Sprintf("%s: status %d", st.Name, st.expected())), st.expected())
		fmt.Fprintf(&b, "        if r.status < 200 or r.status >= 300:\n            raise ValueError(%s %% r.status)\n", q(st.Name+": status %d"))
		if st.Capture != "" {
			fmt.Fprintf(&b, "        _capture(ids, %s, r, %s)\n", q(st.Capture), jsList(st.Fields))
		}
	}
	return b.String()
}

func (st step) pyURL() string {
	parts := []string{"BASE"}
	for _, sg := range st.Segs {
		if sg.Param != "" {
			parts = append(parts, fmt.Sprintf("_path_id(ids, %s, %s)", q(sg.Key), q(sg.Fallback)))
		} else if sg.Lit != "" {
			parts = append(parts, q(sg.Lit))
		}
	}
	if st.Query != "" {
		parts = append(parts, q("?"+st.Query))
	}
	return strings.Join(parts, " + ")
}

func (st step) pyOptions() string {
	var fields []string
	var hdr []string
	if st.Ep.HasBody {
		hdr = append(hdr, `"Content-Type": "application/json"`)
	}
	for _, h := range st.Headers {
		hdr = append(hdr, q(h[0])+": "+q(h[1]))
	}
	headers := ""
	if len(hdr) > 0 {
		headers = "{" + strings.Join(hdr, ", ") + "}"
	}
	if st.Ep.Auth.Kind != AuthNone {
		if headers == "" {
			headers = "_auth()"
		} else {
			headers = "{**_auth(), " + strings.TrimPrefix(headers, "{")
		}
	}
	if headers != "" {
		fields = append(fields, "headers="+headers)
	}
	if st.Ep.HasBody {
		fields = append(fields, "body=json.dumps("+pyLiteral(st.Ep.Body, 3)+")")
	}
	if st.Ep.HasBody {
		// A body is long, so each argument gets its own line.
		return "\n            " + strings.Join(fields, ",\n            ") + ",\n        "
	}
	return strings.Join(fields, ", ")
}

// pyLiteral writes v as a Python literal, indented to depth levels of four
// spaces, so that true, false and null become True, False and None.
func pyLiteral(v any, depth int) string {
	pad := strings.Repeat("    ", depth)
	in := strings.Repeat("    ", depth+1)
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case string:
		return q(x)
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case []any:
		if len(x) == 0 {
			return "[]"
		}
		var items []string
		for _, it := range x {
			items = append(items, in+pyLiteral(it, depth+1))
		}
		return "[\n" + strings.Join(items, ",\n") + ",\n" + pad + "]"
	case map[string]any:
		if len(x) == 0 {
			return "{}"
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var items []string
		for _, k := range keys {
			items = append(items, in+q(k)+": "+pyLiteral(x[k], depth+1))
		}
		return "{\n" + strings.Join(items, ",\n") + ",\n" + pad + "}"
	}
	return "None"
}

// writeWrapped writes text as comment lines of up to about 76 characters.
func writeWrapped(b *strings.Builder, prefix, text string) {
	line := ""
	for _, w := range strings.Fields(text) {
		if line != "" && len(prefix)+len(line)+1+len(w) > 78 {
			b.WriteString(prefix + line + "\n")
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += w
	}
	if line != "" {
		b.WriteString(prefix + line + "\n")
	}
}
