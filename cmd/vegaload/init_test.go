package main

import (
	"encoding/json"
	"github.com/vegaload/vegaload/internal/hosts"
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

func inTempDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(cwd) }) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
}

func TestCmdInit_EditorLimitsTheFiles(t *testing.T) {
	inTempDir(t)
	if code := cmdInit([]string{"-editor", "cursor"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, p := range []string{filepath.Join(".cursor", "rules", "vegaload.mdc"), filepath.Join(".cursor", "mcp.json")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s: %v", p, err)
		}
	}
	for _, p := range []string{".mcp.json", filepath.Join(".claude", "skills", "vegaload", "SKILL.md")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s should not exist with -editor cursor", p)
		}
	}
}

func TestCmdInit_EditorRejectsUnknown(t *testing.T) {
	inTempDir(t)
	if code := cmdInit([]string{"-editor", "vim"}); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if code := cmdInit([]string{"-editor", " "}); code != 2 {
		t.Fatalf("empty editor: exit %d, want 2", code)
	}
}

func TestCmdInit_EditorAcceptsAList(t *testing.T) {
	inTempDir(t)
	if code := cmdInit([]string{"-editor", "claude-code, cursor"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(".mcp.json"); err != nil {
		t.Errorf(".mcp.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(".cursor", "mcp.json")); err != nil {
		t.Errorf(".cursor/mcp.json: %v", err)
	}
}

func TestCmdInit_StatusChangesNothing(t *testing.T) {
	inTempDir(t)
	if code := cmdInit([]string{"-status"}); code != 1 {
		t.Fatalf("empty project: exit %d, want 1", code)
	}
	entries, _ := os.ReadDir(".")
	if len(entries) != 0 {
		t.Errorf("-status wrote files: %v", entries)
	}
}

func TestCmdInit_StatusAfterInitIsOK(t *testing.T) {
	inTempDir(t)
	if code := cmdInit(nil); code != 0 {
		t.Fatalf("init exit %d", code)
	}
	if code := cmdInit([]string{"-status"}); code != 0 {
		t.Fatalf("status after init: exit %d, want 0", code)
	}
}

func TestCmdInit_StatusSeesOutdatedRulesAndOtherBinary(t *testing.T) {
	inTempDir(t)
	if code := cmdInit([]string{"-editor", "cursor"}); code != 0 {
		t.Fatalf("init exit %d", code)
	}
	rules := filepath.Join(".cursor", "rules", "vegaload.mdc")
	if err := os.WriteFile(rules, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	mcp := filepath.Join(".cursor", "mcp.json")
	if err := os.WriteFile(mcp, []byte(`{"mcpServers":{"vegaload":{"command":"/elsewhere/vegaload","args":["mcp","serve"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	rows := map[string]string{}
	for _, a := range hosts.ProjectArtifacts() {
		if a.Host != "cursor" {
			continue
		}
		st, _ := hosts.ArtifactStatus(".", a, exe)
		rows[a.RelPath] = st
	}
	if rows[rules] != hosts.StatusOutdated {
		t.Errorf("rules = %q, want outdated", rows[rules])
	}
	if rows[mcp] != hosts.StatusOtherExe {
		t.Errorf("mcp = %q, want other binary", rows[mcp])
	}
	if code := cmdInit([]string{"-status", "-editor", "cursor"}); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	// -status must not have repaired anything.
	if b, _ := os.ReadFile(rules); string(b) != "old" {
		t.Error("-status changed the rules file")
	}
}

func TestCmdInit_StatusEditorOnlyChecksThatEditor(t *testing.T) {
	inTempDir(t)
	if code := cmdInit([]string{"-editor", "cursor"}); code != 0 {
		t.Fatalf("init exit %d", code)
	}
	if code := cmdInit([]string{"-status", "-editor", "cursor"}); code != 0 {
		t.Errorf("cursor only: exit %d, want 0", code)
	}
	if code := cmdInit([]string{"-status"}); code != 1 {
		t.Errorf("all editors: exit %d, want 1", code)
	}
}
