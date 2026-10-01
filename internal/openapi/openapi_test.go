package openapi

import (
	"strings"
	"testing"
)

const sampleSpec = `{
  "openapi": "3.0.0",
  "info": {"title": "Widgets API"},
  "servers": [{"url": "https://api.example.com/v1/"}],
  "paths": {
    "/widgets": {
      "get": {"summary": "List widgets"},
      "post": {"summary": "Create a widget"}
    },
    "/widgets/{id}": {
      "get": {"summary": "Get a widget"},
      "delete": {"summary": "Delete a widget"}
    }
  }
}`

func TestParse_Basic(t *testing.T) {
	spec, err := Parse([]byte(sampleSpec))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if spec.Title != "Widgets API" {
		t.Errorf("Title = %q, want %q", spec.Title, "Widgets API")
	}
	if spec.BaseURL != "https://api.example.com/v1" {
		t.Errorf("BaseURL = %q, want trailing slash trimmed", spec.BaseURL)
	}
	if len(spec.Endpoints) != 4 {
		t.Fatalf("expected 4 endpoints, got %d: %+v", len(spec.Endpoints), spec.Endpoints)
	}
}

func TestParse_EndpointOrderIsPathThenVerbOrder(t *testing.T) {
	spec, err := Parse([]byte(sampleSpec))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	want := []struct{ Method, Path string }{
		{"GET", "/widgets"},
		{"POST", "/widgets"},
		{"GET", "/widgets/{id}"},
		{"DELETE", "/widgets/{id}"},
	}
	for i, w := range want {
		if spec.Endpoints[i].Method != w.Method || spec.Endpoints[i].Path != w.Path {
			t.Errorf("Endpoints[%d] = %+v, want %+v", i, spec.Endpoints[i], w)
		}
	}
}

func TestParse_InvalidJSON(t *testing.T) {
	if _, err := Parse([]byte("not json")); err == nil {
		t.Error("expected an error for invalid JSON")
	}
}

func TestParse_NoPaths(t *testing.T) {
	if _, err := Parse([]byte(`{"openapi":"3.0.0","info":{"title":"Empty"}}`)); err == nil {
		t.Error("expected an error when the spec has no paths")
	}
}

func TestParse_NoRecognizedOperations(t *testing.T) {
	spec := `{"info":{"title":"X"},"paths":{"/x":{"trace":{"summary":"unsupported verb"}}}}`
	if _, err := Parse([]byte(spec)); err == nil {
		t.Error("expected an error when no path has a recognized HTTP method")
	}
}

func TestParse_DefaultsTitleAndBaseURL(t *testing.T) {
	spec, err := Parse([]byte(`{"paths":{"/x":{"get":{}}}}`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if spec.Title != "API" {
		t.Errorf("Title = %q, want default %q", spec.Title, "API")
	}
	if spec.BaseURL != "" {
		t.Errorf("BaseURL = %q, want empty when no servers are declared", spec.BaseURL)
	}
}

func TestRenderRunbook_IncludesRunCommands(t *testing.T) {
	spec, err := Parse([]byte(sampleSpec))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	doc := spec.RenderRunbook("widgets")
	if !strings.Contains(doc, "Widgets API") {
		t.Error("expected the spec's title in the runbook")
	}
	if !strings.Contains(doc, `vegaload run -target "https://api.example.com/v1/widgets" -protocol http1 -method GET`) {
		t.Errorf("expected a GET /widgets run command, got:\n%s", doc)
	}
	if !strings.Contains(doc, `-method DELETE`) {
		t.Error("expected a DELETE run command for /widgets/{id}")
	}
	if !strings.Contains(doc, "List widgets") {
		t.Error("expected the endpoint's summary in the runbook")
	}
}

func TestRenderRunbook_NoSummaryStillRendersHeading(t *testing.T) {
	spec, err := Parse([]byte(`{"paths":{"/x":{"get":{}}}}`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	doc := spec.RenderRunbook("x")
	if !strings.Contains(doc, "## GET /x") {
		t.Errorf("expected a heading for the endpoint even without a summary, got:\n%s", doc)
	}
}
