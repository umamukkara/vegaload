package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCmdInit_CreatesAllFour(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if code := cmdInit(nil); code != 0 {
		t.Fatalf("cmdInit returned exit code %d", code)
	}

	for _, p := range []string{
		filepath.Join(".claude", "skills", "vegaload", "SKILL.md"),
		filepath.Join(".cursor", "rules", "vegaload.mdc"),
		".mcp.json",
		filepath.Join(".cursor", "mcp.json"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to exist: %v", p, err)
		}
	}

	data, err := os.ReadFile(".mcp.json")
	if err != nil {
		t.Fatalf("reading .mcp.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf(".mcp.json is not valid JSON: %v", err)
	}
	servers := doc["mcpServers"].(map[string]any)
	if _, ok := servers["vegaload"]; !ok {
		t.Error("expected a \"vegaload\" entry in .mcp.json's mcpServers")
	}
}

func TestCmdInit_SecondRunSkipsWithoutForce(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if code := cmdInit(nil); code != 0 {
		t.Fatalf("first cmdInit returned exit code %d", code)
	}
	before, err := os.ReadFile(".mcp.json")
	if err != nil {
		t.Fatalf("reading .mcp.json: %v", err)
	}

	if code := cmdInit([]string{"-output", "json"}); code != 0 {
		t.Fatalf("second cmdInit returned exit code %d", code)
	}
	after, err := os.ReadFile(".mcp.json")
	if err != nil {
		t.Fatalf("reading .mcp.json: %v", err)
	}
	if string(before) != string(after) {
		t.Error("expected a second cmdInit (no -force) to leave .mcp.json unchanged")
	}
}

func TestCmdInit_PreservesExistingMCPServers(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	existing := `{"mcpServers":{"other-tool":{"command":"other","args":["serve"]}}}`
	if err := os.WriteFile(".mcp.json", []byte(existing), 0o644); err != nil {
		t.Fatalf("writing existing .mcp.json: %v", err)
	}

	if code := cmdInit(nil); code != 0 {
		t.Fatalf("cmdInit returned exit code %d", code)
	}

	data, err := os.ReadFile(".mcp.json")
	if err != nil {
		t.Fatalf("reading .mcp.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf(".mcp.json is not valid JSON: %v", err)
	}
	servers := doc["mcpServers"].(map[string]any)
	if _, ok := servers["other-tool"]; !ok {
		t.Error("expected the pre-existing \"other-tool\" entry to survive")
	}
	if _, ok := servers["vegaload"]; !ok {
		t.Error("expected a new \"vegaload\" entry to be added")
	}
}

func TestCmdInit_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if code := cmdInit(nil); code != 0 {
		t.Fatalf("first cmdInit returned exit code %d", code)
	}
	if err := os.WriteFile(filepath.Join(".cursor", "rules", "vegaload.mdc"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("overwriting rules file: %v", err)
	}

	if code := cmdInit([]string{"-force"}); code != 0 {
		t.Fatalf("cmdInit -force returned exit code %d", code)
	}
	data, err := os.ReadFile(filepath.Join(".cursor", "rules", "vegaload.mdc"))
	if err != nil {
		t.Fatalf("reading rules file: %v", err)
	}
	if string(data) == "stale" {
		t.Error("expected -force to overwrite the stale rules file")
	}
}

func TestCmdInit_InvalidOutputMode(t *testing.T) {
	if code := cmdInit([]string{"-output", "bogus"}); code == 0 {
		t.Error("expected a non-zero exit code for an invalid -output mode")
	}
}
