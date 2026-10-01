// Package llm implements FR-MCP-05's optional BYO-LLM narrative layer:
// turning a diagnose.Findings into a short plain-English explanation by
// calling a user-configured LLM endpoint. It is entirely stdlib
// (net/http + encoding/json) — no SDK, no new go.mod dependency — and
// entirely opt-in: per NFR-05 ("nothing leaves the local machine unless
// explicitly configured"), ConfigFromEnv returns a Config with an empty
// Provider when nothing is set, and Narrate treats that as "do not make
// a network call" rather than an error.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Config holds everything Narrate needs to call an LLM endpoint. Every
// field comes from an environment variable (see ConfigFromEnv) so that
// neither the CLI nor the MCP layer has to grow its own provider flags —
// both just call Narrate and let the user's shell environment decide
// whether it does anything at all.
type Config struct {
	Provider string // "openai", "ollama", "anthropic", or "" (disabled)
	APIKey   string
	BaseURL  string
	Model    string
}

// Enabled reports whether cfg has enough set to attempt a call. An
// empty Provider is the documented way to disable narration entirely.
func (c Config) Enabled() bool {
	return c.Provider != ""
}

// ConfigFromEnv reads VEGALOAD_LLM_PROVIDER, VEGALOAD_LLM_API_KEY,
// VEGALOAD_LLM_BASE_URL, and VEGALOAD_LLM_MODEL. Only Provider is
// required to enable narration — ollama commonly needs no API key, and
// each provider has a sensible BaseURL/Model default (see defaults.go
// equivalents below) so a user who only sets the provider still gets a
// working call.
func ConfigFromEnv() Config {
	return Config{
		Provider: os.Getenv("VEGALOAD_LLM_PROVIDER"),
		APIKey:   os.Getenv("VEGALOAD_LLM_API_KEY"),
		BaseURL:  os.Getenv("VEGALOAD_LLM_BASE_URL"),
		Model:    os.Getenv("VEGALOAD_LLM_MODEL"),
	}
}

// defaultBaseURL and defaultModel fill in a provider's usual endpoint
// and a reasonable model when the user didn't set one explicitly.
func defaultBaseURL(provider string) string {
	switch provider {
	case "openai":
		return "https://api.openai.com/v1"
	case "ollama":
		return "http://localhost:11434/v1"
	case "anthropic":
		return "https://api.anthropic.com"
	default:
		return ""
	}
}

func defaultModel(provider string) string {
	switch provider {
	case "openai":
		return "gpt-4o-mini"
	case "ollama":
		return "llama3.1"
	case "anthropic":
		return "claude-3-5-haiku-latest"
	default:
		return ""
	}
}

// httpClient is overridable by tests so they never make a real network
// call; Narrate and its helpers always go through this variable rather
// than http.DefaultClient directly.
var httpClient = &http.Client{Timeout: 20 * time.Second}

// Narrate turns prompt — already-composed text describing a run's
// diagnose.Findings, built by the caller so this package stays
// prompt-content-agnostic — into a short natural-language explanation
// via cfg's configured provider. It returns ("", nil) without making any
// network call when cfg is not Enabled(), which is the expected path
// whenever the user hasn't opted in.
func Narrate(ctx context.Context, cfg Config, prompt string) (string, error) {
	if !cfg.Enabled() {
		return "", nil
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL(cfg.Provider)
	}
	model := cfg.Model
	if model == "" {
		model = defaultModel(cfg.Provider)
	}
	if baseURL == "" || model == "" {
		return "", fmt.Errorf("llm: unknown provider %q and no VEGALOAD_LLM_BASE_URL/VEGALOAD_LLM_MODEL set", cfg.Provider)
	}

	switch cfg.Provider {
	case "openai", "ollama":
		return chatCompletionsAPI(ctx, baseURL, cfg.APIKey, model, prompt)
	case "anthropic":
		return anthropicMessages(ctx, baseURL, cfg.APIKey, model, prompt)
	default:
		return "", fmt.Errorf("llm: unknown provider %q (want openai, ollama, or anthropic)", cfg.Provider)
	}
}

// chatCompletionsAPI calls the OpenAI-compatible /chat/completions
// endpoint shape shared by OpenAI and Ollama's OpenAI-compatibility
// layer.
func chatCompletionsAPI(ctx context.Context, baseURL, apiKey, model, prompt string) (string, error) {
	reqBody := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0.2,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("llm: encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("llm: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("llm: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm: %s returned %d: %s", baseURL, resp.StatusCode, truncate(respBody, 300))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("llm: decoding response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("llm: response had no choices")
	}
	return parsed.Choices[0].Message.Content, nil
}

// anthropicMessages calls Anthropic's /v1/messages endpoint shape,
// which differs from the OpenAI-compatible shape in its auth header,
// its required max_tokens, and its response envelope.
func anthropicMessages(ctx context.Context, baseURL, apiKey, model, prompt string) (string, error) {
	reqBody := map[string]any{
		"model":      model,
		"max_tokens": 1024,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("llm: encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("llm: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("llm: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm: %s returned %d: %s", baseURL, resp.StatusCode, truncate(respBody, 300))
	}

	var parsed struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("llm: decoding response: %w", err)
	}
	if len(parsed.Content) == 0 {
		return "", fmt.Errorf("llm: response had no content blocks")
	}
	return parsed.Content[0].Text, nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
