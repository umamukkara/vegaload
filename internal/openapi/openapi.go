// Package openapi implements FR-MCP-06's `vegaload new --from-openapi`
// and the generate_from_spec MCP tool: turning an OpenAPI document into
// a ready-to-run starting point for load testing its endpoints.
//
// It parses JSON OpenAPI specs only — no YAML parser, deliberately, to
// avoid adding a new go.mod dependency (see go.mod's pinning history).
// Most tooling (Swagger UI, most API gateways) can export a spec as
// JSON even when the canonical source is YAML, so this is a real
// limitation but rarely a blocking one.
//
// It writes two things. RenderScenario is a runnable scenario that calls every
// operation as a named step. RenderRunbook is a Markdown runbook with one
// `vegaload run -target ...` command per endpoint.
//
// An OpenAPI spec describes each endpoint in isolation. It says nothing about
// how they relate, so the scenario's order and data flow come from simple
// rules, not from the spec: creates first, then reads, updates, and deletes
// last, and the id a create returns is used by the calls under its path. The
// generated file says this, and marks every value it had to guess, so the user
// edits it into the real flow.
package openapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Endpoint is one operation an OpenAPI document declares: an HTTP
// method on a path, with its summary (when the spec provides one) for
// a human-readable runbook entry. The other fields are what a generated
// scenario needs (see RenderScenario).
type Endpoint struct {
	Method  string
	Path    string
	Summary string

	// OperationID is the spec's operationId, or empty.
	OperationID string
	// Params are the path, query and header parameters, from the path item
	// and the operation.
	Params []Param
	// Body is an example request body, from the spec's example or built from
	// its schema. It is nil when the operation has no JSON body.
	Body any
	// HasBody is true when the operation has a JSON body.
	HasBody bool
	// OtherBody is the content type of a body that is not JSON, which a
	// scenario cannot generate, or empty.
	OtherBody string
	// Success is the first 2xx status the spec declares, or 0.
	Success int
	// Auth is how the operation is secured.
	Auth Auth
}

// Param is one parameter of an operation.
type Param struct {
	Name     string
	In       string // path, query or header
	Required bool
	// Example is a sample value, as text. It is never empty.
	Example string
	// Integer is true when the schema type is integer or number.
	Integer bool
}

// AuthKind is how an operation is secured.
type AuthKind int

const (
	AuthNone AuthKind = iota
	// AuthBearer is a bearer token in the Authorization header.
	AuthBearer
	// AuthBasic is HTTP basic authentication.
	AuthBasic
	// AuthAPIKey is an API key in a header. AuthHeader names it.
	AuthAPIKey
)

// Auth describes an operation's security.
type Auth struct {
	Kind   AuthKind
	Header string // for AuthAPIKey
}

// Spec is the minimal slice of an OpenAPI document this package needs:
// a title for the generated runbook's heading, a base URL to combine
// with each endpoint's path, and the endpoints themselves.
type Spec struct {
	Title     string
	BaseURL   string
	Endpoints []Endpoint
}

// httpMethods lists the operation keys OpenAPI defines under a path
// item, in the fixed order a runbook should present them — not
// alphabetical, but the order a human thinks about a resource's verbs
// (read before write, write before delete).
var httpMethods = []string{"get", "post", "put", "patch", "delete", "head", "options"}

// rawParam is a parameter, or a $ref to one in components.
type rawParam struct {
	Ref      string `json:"$ref"`
	Name     string `json:"name"`
	In       string `json:"in"`
	Required bool   `json:"required"`
	Schema   any    `json:"schema"`
	Example  any    `json:"example"`
}

// rawOp is one operation. Security is a pointer-like: nil means the key was
// absent (use the document's), and an empty list means "no security".
type rawOp struct {
	Summary     string     `json:"summary"`
	OperationID string     `json:"operationId"`
	Parameters  []rawParam `json:"parameters"`
	RequestBody *struct {
		Ref     string `json:"$ref"`
		Content map[string]struct {
			Schema   any `json:"schema"`
			Example  any `json:"example"`
			Examples map[string]struct {
				Value any `json:"value"`
			} `json:"examples"`
		} `json:"content"`
	} `json:"requestBody"`
	Responses map[string]json.RawMessage `json:"responses"`
	Security  *[]map[string][]string     `json:"security"`
}

// rawDocument mirrors just the fields of an OpenAPI 3.x JSON document
// that Parse needs; everything else in a real spec is ignored.
type rawDocument struct {
	Info struct {
		Title string `json:"title"`
	} `json:"info"`
	Servers []struct {
		URL string `json:"url"`
	} `json:"servers"`
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Security   []map[string][]string                 `json:"security"`
	Components struct {
		Schemas         map[string]any             `json:"schemas"`
		Parameters      map[string]rawParam        `json:"parameters"`
		RequestBodies   map[string]json.RawMessage `json:"requestBodies"`
		SecuritySchemes map[string]struct {
			Type   string `json:"type"`
			Scheme string `json:"scheme"`
			In     string `json:"in"`
			Name   string `json:"name"`
		} `json:"securitySchemes"`
	} `json:"components"`
}

// Parse reads an OpenAPI 3.x document from data (JSON only — see this
// package's doc comment) and returns the Spec RenderRunbook and
// RenderScenario need.
func Parse(data []byte) (*Spec, error) {
	var doc rawDocument
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("openapi: not valid JSON (only JSON OpenAPI specs are supported, not YAML): %w", err)
	}
	if len(doc.Paths) == 0 {
		return nil, fmt.Errorf("openapi: no paths found in the spec")
	}

	spec := &Spec{Title: doc.Info.Title}
	if len(doc.Servers) > 0 {
		spec.BaseURL = strings.TrimSuffix(doc.Servers[0].URL, "/")
	}
	if spec.Title == "" {
		spec.Title = "API"
	}

	paths := make([]string, 0, len(doc.Paths))
	for p := range doc.Paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		ops := doc.Paths[p]
		var pathParams []rawParam
		if raw, ok := ops["parameters"]; ok {
			_ = json.Unmarshal(raw, &pathParams)
		}
		for _, method := range httpMethods {
			rawOpData, ok := ops[method]
			if !ok {
				continue
			}
			var op rawOp
			// A malformed operation still counts as an operation. The
			// runbook only needs its method and path.
			odec := json.NewDecoder(bytes.NewReader(rawOpData))
			odec.UseNumber()
			_ = odec.Decode(&op)
			ep := Endpoint{
				Method:      strings.ToUpper(method),
				Path:        p,
				Summary:     op.Summary,
				OperationID: op.OperationID,
			}
			fillDetails(&ep, &doc, &op, pathParams)
			spec.Endpoints = append(spec.Endpoints, ep)
		}
	}

	if len(spec.Endpoints) == 0 {
		return nil, fmt.Errorf("openapi: no operations found under any path (only get/post/put/patch/delete/head/options are recognized)")
	}
	return spec, nil
}

// RenderRunbook renders a Markdown document: one `vegaload run` command
// per endpoint, ready to copy and run (or for an MCP-driven agent to
// pass straight to the run_test tool's target/protocol/executor
// arguments). name is used only in the heading, to connect the runbook
// back to the scaffold command that produced it.
func (s *Spec) RenderRunbook(name string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — load test runbook\n\n", s.Title)
	fmt.Fprintf(&b, "Generated by `vegaload new %s -from-openapi <spec>`.\n\n", name)
	b.WriteString("Each entry below is the exact protocol-direct command for one endpoint this spec " +
		"declares, for loading one endpoint at a time. Copy the one you want and adjust " +
		"-vus/-duration/-executor. The scenario file written next to this runbook calls all of " +
		"them in one flow instead. An OpenAPI spec describes each endpoint on its own, so the " +
		"order of that flow is a guess: edit it to fit the real one.\n\n")

	for _, ep := range s.Endpoints {
		target := s.BaseURL + ep.Path
		if ep.Summary != "" {
			fmt.Fprintf(&b, "## %s %s — %s\n\n", ep.Method, ep.Path, ep.Summary)
		} else {
			fmt.Fprintf(&b, "## %s %s\n\n", ep.Method, ep.Path)
		}
		fmt.Fprintf(&b, "```\nvegaload run -target %q -protocol http1 -method %s -vus 10 -duration 30s\n```\n\n",
			target, ep.Method)
	}
	return b.String()
}
