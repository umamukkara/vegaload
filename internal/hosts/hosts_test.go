package hosts

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInspect(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	write(t, good, `{"mcpServers":{"vegaload":{"command":"/bin/vl","args":["mcp","serve"],"env":{"A":"1"}},"other":{"command":"x"}}}`)
	st := Inspect(MCPConfig{Path: good})
	if st.Err != nil || !st.Exists || st.Entry == nil {
		t.Fatalf("good config: %+v", st)
	}
	if st.Entry.Command != "/bin/vl" || len(st.Entry.Args) != 2 || st.Entry.Env["A"] != "1" {
		t.Errorf("entry = %+v", st.Entry)
	}

	if st := Inspect(MCPConfig{Path: filepath.Join(dir, "missing.json")}); st.Exists || st.Err != nil || st.Entry != nil {
		t.Errorf("missing file: %+v", st)
	}

	bad := filepath.Join(dir, "bad.json")
	write(t, bad, `{nope`)
	if st := Inspect(MCPConfig{Path: bad}); st.Err == nil {
		t.Error("invalid JSON should set Err")
	}

	noEntry := filepath.Join(dir, "none.json")
	write(t, noEntry, `{"mcpServers":{"other":{"command":"x"}}}`)
	if st := Inspect(MCPConfig{Path: noEntry}); !st.Exists || st.Entry != nil {
		t.Errorf("no vegaload entry: %+v", st)
	}

	nested := filepath.Join(dir, "claude.json")
	write(t, nested, `{"projects":{"/work/app":{"mcpServers":{"vegaload":{"command":"vl","args":["mcp","serve"]}}}}}`)
	if st := Inspect(MCPConfig{Path: nested, Key: []string{"projects", "/work/app"}}); st.Entry == nil {
		t.Error("nested key should find the entry")
	}
	if st := Inspect(MCPConfig{Path: nested, Key: []string{"projects", "/other"}}); st.Entry != nil {
		t.Error("a different project must not match")
	}
}

func TestEntryExpand(t *testing.T) {
	e := Entry{Command: "${workspaceFolder}/bin/vl", Args: []string{"--home", "${userHome}", "--x", "${env:FOO}"}}
	got := e.Expand(Env{Dir: "/proj", Home: "/home/u"}, func(n string) string {
		if n == "FOO" {
			return "bar"
		}
		return ""
	})
	if got.Command != "/proj/bin/vl" || got.Args[1] != "/home/u" || got.Args[3] != "bar" {
		t.Errorf("Expand = %+v", got)
	}
}

func TestHostsPaths(t *testing.T) {
	env := Env{OS: "darwin", Home: "/h", Dir: "/p"}
	cur, _ := Get("cursor")
	cfgs := cur.Configs(env)
	if cfgs[0].Path != "/p/.cursor/mcp.json" || cfgs[0].Scope != ScopeProject || cfgs[1].Path != "/h/.cursor/mcp.json" {
		t.Errorf("cursor configs: %+v", cfgs)
	}
	cc, _ := Get("claude-code")
	if got := cc.Configs(env)[0].Path; got != "/p/.mcp.json" {
		t.Errorf("claude-code project config = %s", got)
	}
	for _, c := range cc.Configs(env)[1:] {
		if !c.ReadOnly {
			t.Errorf("claude-code user config %+v must be read-only", c)
		}
	}
	cd, _ := Get("claude-desktop")
	if !cd.SupportedOn("darwin") || cd.SupportedOn("linux") {
		t.Error("claude-desktop should be macOS only")
	}
	if got := cd.Configs(env)[0].Path; got != "/h/Library/Application Support/Claude/claude_desktop_config.json" {
		t.Errorf("claude-desktop config = %s", got)
	}
	if _, ok := Get("nope"); ok {
		t.Error("unknown host should not resolve")
	}
}

func TestProjectArtifactsOrderMatchesInit(t *testing.T) {
	want := []string{
		filepath.Join(".claude", "skills", "vegaload", "SKILL.md"),
		filepath.Join(".cursor", "rules", "vegaload.mdc"),
		".mcp.json",
		filepath.Join(".cursor", "mcp.json"),
	}
	got := ProjectArtifacts()
	if len(got) != len(want) {
		t.Fatalf("got %d artifacts", len(got))
	}
	for i, a := range got {
		if a.RelPath != want[i] {
			t.Errorf("artifact %d = %s, want %s", i, a.RelPath, want[i])
		}
		if a.Kind == "rules" && a.Content == "" {
			t.Errorf("rules artifact %s has no content", a.RelPath)
		}
	}
}

func TestMergeConfigFile_KeepsOtherServers(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mcp.json")
	write(t, p, `{"mcpServers":{"other":{"command":"x"}},"keep":true}`)
	res, err := MergeConfigFile(p, "/bin/vl", false)
	if err != nil || res.Status != "updated" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	st := Inspect(MCPConfig{Path: p})
	if st.Entry == nil || st.Entry.Command != "/bin/vl" {
		t.Errorf("entry not written: %+v", st)
	}
	data, _ := os.ReadFile(p)
	if !contains(string(data), `"other"`) || !contains(string(data), `"keep"`) {
		t.Errorf("existing keys lost: %s", data)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
