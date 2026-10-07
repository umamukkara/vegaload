package openapi

import (
	"strings"
	"testing"
)

const shopSpec = `{
  "info": {"title": "Shop"},
  "servers": [{"url": "http://shop.test/v1"}],
  "security": [{"key": []}],
  "components": {
    "securitySchemes": {"key": {"type": "apiKey", "in": "header", "name": "X-Api-Key"}},
    "schemas": {
      "Item": {"type": "object", "properties": {
        "id": {"type": "integer", "readOnly": true},
        "name": {"type": "string"},
        "when": {"type": "string", "format": "date-time"},
        "self": {"$ref": "#/components/schemas/Item"}
      }}
    }
  },
  "paths": {
    "/items/{itemId}": {
      "parameters": [{"name": "itemId", "in": "path", "required": true, "schema": {"type": "integer"}}],
      "delete": {"operationId": "deleteItem", "responses": {"204": {}}},
      "get": {"operationId": "getItem", "responses": {"200": {}}},
      "put": {"operationId": "putItem", "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/Item"}}}}}
    },
    "/items": {
      "get": {"operationId": "listItems", "parameters": [
        {"name": "limit", "in": "query", "required": true, "schema": {"type": "integer", "default": 25}},
        {"name": "q", "in": "query", "schema": {"type": "string"}}], "security": []},
      "post": {"operationId": "createItem", "responses": {"201": {}},
        "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/Item"}}}}}
    }
  }
}`

func parseShop(t *testing.T) *Spec {
	t.Helper()
	s, err := Parse([]byte(shopSpec))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParse_Details(t *testing.T) {
	s := parseShop(t)
	var create, list, get Endpoint
	for _, ep := range s.Endpoints {
		switch ep.OperationID {
		case "createItem":
			create = ep
		case "listItems":
			list = ep
		case "getItem":
			get = ep
		}
	}
	if create.Success != 201 || !create.HasBody || create.Auth.Kind != AuthAPIKey || create.Auth.Header != "X-Api-Key" {
		t.Errorf("create = %+v", create)
	}
	body, _ := create.Body.(map[string]any)
	if body["name"] != "string" || body["when"] != "2026-01-01T00:00:00Z" {
		t.Errorf("body = %v", create.Body)
	}
	if _, has := body["id"]; has {
		t.Error("a read-only property should not be in a request body")
	}
	if _, has := body["self"]; has {
		t.Error("a schema that contains itself should be cut where it repeats")
	}
	if list.Auth.Kind != AuthNone {
		t.Errorf("security: [] should switch auth off, got %+v", list.Auth)
	}
	if got := queryString(list); got != "limit=25" {
		t.Errorf("query = %q, want only the required parameter with its default", got)
	}
	if len(get.Params) != 1 || get.Params[0].Name != "itemId" || !get.Params[0].Integer {
		t.Errorf("path-level parameters should reach the operation: %+v", get.Params)
	}
}

func TestPlan_OrderAndWiring(t *testing.T) {
	p := parseShop(t).plan()
	var order []string
	for _, st := range p.Steps {
		order = append(order, st.Name)
	}
	want := "createItem,listItems,getItem,putItem,deleteItem"
	if got := strings.Join(order, ","); got != want {
		t.Errorf("order = %s, want %s", got, want)
	}
	create := p.Steps[0]
	if create.Capture != "/items" || len(create.Fields) != 2 || create.Fields[0] != "id" || create.Fields[1] != "itemId" {
		t.Errorf("capture = %q %v", create.Capture, create.Fields)
	}
	get := p.Steps[2]
	if len(get.Segs) != 2 || get.Segs[1].Param != "itemId" || get.Segs[1].Key != "/items" {
		t.Errorf("get path = %+v", get.Segs)
	}
	if len(get.Notes) != 0 {
		t.Errorf("an id from a create needs no placeholder note: %v", get.Notes)
	}
	if p.Auth.Kind != AuthAPIKey {
		t.Errorf("auth = %+v", p.Auth)
	}
}

func TestPlan_PlaceholderWhenNoCreate(t *testing.T) {
	s, err := Parse([]byte(`{"paths":{"/orders/{orderId}":{"get":{}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	p := s.plan()
	if len(p.Steps[0].Notes) != 1 || !strings.Contains(p.Steps[0].Notes[0], "placeholder") {
		t.Errorf("notes = %v", p.Steps[0].Notes)
	}
	if p.Steps[0].Name != "GET /orders/:orderId" {
		t.Errorf("name = %q", p.Steps[0].Name)
	}
}

func TestRenderScenario_JavaScript(t *testing.T) {
	out := parseShop(t).RenderScenario("shop.vl.js", JavaScript)
	for _, want := range []string{
		`const base = "http://shop.test/v1";`,
		`step("createItem"`,
		`http.post(base + "/items"`,
		`"X-Api-Key": token`,
		`base + "/items/" + pathId(ids, "/items", "1")`,
		`"?limit=25"`,
		`capture(ids, "/items", r, ["id", "itemId"])`,
		`"createItem: status 201"`,
		`env.API_KEY`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// A server in the spec means no placeholder note for the base.
	if strings.Contains(out, "names no absolute server") {
		t.Error("the spec has an absolute server")
	}
}

func TestRenderScenario_Python(t *testing.T) {
	out := parseShop(t).RenderScenario("shop.py", Python)
	for _, want := range []string{
		`BASE = "http://shop.test/v1"`,
		`with step("createItem"):`,
		`_path_id(ids, "/items", "1")`,
		`body=json.dumps(`,
		`_capture(ids, "/items", r, ["id", "itemId"])`,
		`env.get("API_KEY")`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRenderScenario_RelativeServerIsAPlaceholder(t *testing.T) {
	s, err := Parse([]byte(`{"servers":[{"url":"/api"}],"paths":{"/x":{"get":{}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	out := s.RenderScenario("x.vl.js", JavaScript)
	if !strings.Contains(out, `const base = "http://127.0.0.1:8080/api";`) || !strings.Contains(out, "TODO: The spec names no absolute server") {
		t.Errorf("relative server:\n%s", out)
	}
}

func TestStepName_Unique(t *testing.T) {
	used := map[string]int{}
	a := stepName(Endpoint{OperationID: "same"}, used)
	b := stepName(Endpoint{OperationID: "same"}, used)
	if a == b {
		t.Errorf("names must be unique: %q %q", a, b)
	}
	if n := stepName(Endpoint{OperationID: `has "quotes"`}, map[string]int{}); strings.Contains(n, `"`) {
		t.Errorf("quote marks cannot be in a step name (a threshold cannot target them): %q", n)
	}
}

func TestPyLiteral(t *testing.T) {
	got := pyLiteral(map[string]any{"a": true, "b": nil, "c": []any{}}, 0)
	for _, want := range []string{`"a": True`, `"b": None`, `"c": []`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
}

func TestPlan_NoteWhenAWriteDeclaresNoBody(t *testing.T) {
	s, err := Parse([]byte(`{"paths":{"/w":{"post":{"summary":"Create"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	notes := s.plan().Steps[0].Notes
	if len(notes) != 1 || !strings.Contains(notes[0], "declares no request body") {
		t.Errorf("notes = %v", notes)
	}
}

func TestRenderScenario_HeadAndOptionsUseRequest(t *testing.T) {
	s, err := Parse([]byte(`{"servers":[{"url":"http://x.test"}],"paths":{"/p":{"head":{},"options":{},"get":{}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	js := s.RenderScenario("p.vl.js", JavaScript)
	py := s.RenderScenario("p.py", Python)
	for _, want := range []string{`http.request("HEAD", base + "/p"`, `http.request("OPTIONS", base + "/p"`, `http.get(base + "/p"`} {
		if !strings.Contains(js, want) {
			t.Errorf("js: missing %q in:\n%s", want, js)
		}
	}
	for _, want := range []string{`http.request("HEAD", BASE + "/p"`, `http.request("OPTIONS", BASE + "/p"`, `http.get(BASE + "/p"`} {
		if !strings.Contains(py, want) {
			t.Errorf("py: missing %q in:\n%s", want, py)
		}
	}
	for _, bad := range []string{"http.head(", "http.options("} {
		if strings.Contains(js, bad) || strings.Contains(py, bad) {
			t.Errorf("the HTTP client has no %s", bad)
		}
	}
}

func TestPlan_APIKeyHeaderParameterDoesNotOverrideTheToken(t *testing.T) {
	s, err := Parse([]byte(`{
	  "components": {"securitySchemes": {"k": {"type": "apiKey", "in": "header", "name": "X-Api-Key"}}},
	  "security": [{"k": []}],
	  "paths": {"/p": {"get": {"parameters": [
	    {"name": "x-api-key", "in": "header", "required": true, "schema": {"type": "string"}},
	    {"name": "X-Region", "in": "header", "required": true, "schema": {"type": "string"}}]}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	st := s.plan().Steps[0]
	if len(st.Headers) != 1 || st.Headers[0][0] != "X-Region" {
		t.Errorf("headers = %v, want only X-Region", st.Headers)
	}
}

func TestParse_JSONMediaTypeWithParameters(t *testing.T) {
	s, err := Parse([]byte(`{"paths":{"/p":{"post":{"requestBody":{"content":{
	  "application/json; charset=utf-8": {"example": {"a": 1}}}}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	ep := s.Endpoints[0]
	if !ep.HasBody || ep.OtherBody != "" {
		t.Errorf("a JSON media type with a parameter is still JSON: %+v", ep)
	}
}

func TestRenderScenario_PythonQuotesTheId(t *testing.T) {
	out := parseShop(t).RenderScenario("shop.py", Python)
	if !strings.Contains(out, "from urllib.parse import quote") || !strings.Contains(out, `quote(str(ids[key]), safe="")`) {
		t.Errorf("python should escape the id like JavaScript does:\n%s", out)
	}
}
