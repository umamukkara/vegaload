package llm

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestConfig_Enabled(t *testing.T) {
	if (Config{}).Enabled() {
		t.Error("expected an empty Config to be disabled")
	}
	if !(Config{Provider: "openai"}).Enabled() {
		t.Error("expected a Config with a Provider to be enabled")
	}
}

func TestConfigFromEnv_Unset(t *testing.T) {
	for _, k := range []string{"VEGALOAD_LLM_PROVIDER", "VEGALOAD_LLM_API_KEY", "VEGALOAD_LLM_BASE_URL", "VEGALOAD_LLM_MODEL"} {
		os.Unsetenv(k)
	}
	cfg := ConfigFromEnv()
	if cfg.Enabled() {
		t.Error("expected ConfigFromEnv to be disabled when nothing is set")
	}
}

func TestConfigFromEnv_Set(t *testing.T) {
	t.Setenv("VEGALOAD_LLM_PROVIDER", "openai")
	t.Setenv("VEGALOAD_LLM_API_KEY", "sk-test")
	t.Setenv("VEGALOAD_LLM_BASE_URL", "http://example.test")
	t.Setenv("VEGALOAD_LLM_MODEL", "gpt-test")
	cfg := ConfigFromEnv()
	if !cfg.Enabled() || cfg.APIKey != "sk-test" || cfg.BaseURL != "http://example.test" || cfg.Model != "gpt-test" {
		t.Errorf("ConfigFromEnv = %+v, want fields from env", cfg)
	}
}

func TestNarrate_DisabledMakesNoCall(t *testing.T) {
	called := false
	orig := httpClient
	defer func() { httpClient = orig }()
	httpClient = newStubClient(t, func(r *http.Request) (*http.Response, error) {
		called = true
		t.Fatal("did not expect a network call for a disabled Config")
		return nil, nil
	})

	out, err := Narrate(context.Background(), Config{}, "anything")
	if err != nil {
		t.Fatalf("Narrate returned error: %v", err)
	}
	if out != "" {
		t.Errorf("Narrate = %q, want empty string when disabled", out)
	}
	if called {
		t.Error("expected no network call when disabled")
	}
}

func TestNarrate_OpenAICompatible(t *testing.T) {
	orig := httpClient
	defer func() { httpClient = orig }()
	httpClient = newStubClient(t, func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("path = %s, want suffix /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer sk-test")
		}
		body := `{"choices":[{"message":{"content":"the run looks healthy"}}]}`
		return jsonResponse(200, body), nil
	})

	cfg := Config{Provider: "openai", APIKey: "sk-test", BaseURL: "https://api.openai.test/v1", Model: "gpt-test"}
	out, err := Narrate(context.Background(), cfg, "summarize this run")
	if err != nil {
		t.Fatalf("Narrate returned error: %v", err)
	}
	if out != "the run looks healthy" {
		t.Errorf("Narrate = %q, want %q", out, "the run looks healthy")
	}
}

func TestNarrate_Anthropic(t *testing.T) {
	orig := httpClient
	defer func() { httpClient = orig }()
	httpClient = newStubClient(t, func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/v1/messages") {
			t.Errorf("path = %s, want suffix /v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "ak-test" {
			t.Errorf("x-api-key = %q, want %q", got, "ak-test")
		}
		body := `{"content":[{"text":"failures clustered early in the run"}]}`
		return jsonResponse(200, body), nil
	})

	cfg := Config{Provider: "anthropic", APIKey: "ak-test", BaseURL: "https://api.anthropic.test", Model: "claude-test"}
	out, err := Narrate(context.Background(), cfg, "summarize this run")
	if err != nil {
		t.Fatalf("Narrate returned error: %v", err)
	}
	if out != "failures clustered early in the run" {
		t.Errorf("Narrate = %q, want %q", out, "failures clustered early in the run")
	}
}

func TestNarrate_UnknownProvider(t *testing.T) {
	cfg := Config{Provider: "bogus"}
	if _, err := Narrate(context.Background(), cfg, "x"); err == nil {
		t.Error("expected an error for an unknown provider")
	}
}

func TestNarrate_NonOKStatus(t *testing.T) {
	orig := httpClient
	defer func() { httpClient = orig }()
	httpClient = newStubClient(t, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(500, `{"error":"boom"}`), nil
	})

	cfg := Config{Provider: "openai", BaseURL: "https://api.openai.test/v1", Model: "gpt-test"}
	if _, err := Narrate(context.Background(), cfg, "x"); err == nil {
		t.Error("expected an error for a non-200 response")
	}
}

// --- test helpers: a RoundTripper-backed client, no real network use ---

type stubRoundTripper struct {
	t  *testing.T
	fn func(*http.Request) (*http.Response, error)
}

func (s stubRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return s.fn(r)
}

func newStubClient(t *testing.T, fn func(*http.Request) (*http.Response, error)) *http.Client {
	t.Helper()
	return &http.Client{Transport: stubRoundTripper{t: t, fn: fn}}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}
