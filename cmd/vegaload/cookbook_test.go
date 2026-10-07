package main

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// helpFlags returns the flag names a command prints for -h. Running the
// real command keeps this test from copying the flag list.
func helpFlags(t *testing.T, run func([]string) int, args ...string) map[string]bool {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	run(append(args, "-h"))
	w.Close()
	os.Stderr = old
	out := <-done

	flags := map[string]bool{"h": true, "help": true}
	re := regexp.MustCompile(`(?m)^\s+-([A-Za-z][A-Za-z0-9-]*)`)
	for _, m := range re.FindAllStringSubmatch(out, -1) {
		flags[m[1]] = true
	}
	return flags
}

// The cookbook is documentation people copy. This test reads every
// `vegaload ...` command in its code blocks and checks that each flag
// exists, so a renamed flag cannot leave a recipe broken.
func TestCookbookFlagsExist(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "cookbook")
	data, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}

	known := map[string]map[string]bool{
		"run":      helpFlags(t, cmdRun),
		"compare":  helpFlags(t, cmdCompare),
		"new":      helpFlags(t, cmdNew),
		"init":     helpFlags(t, cmdInit),
		"diagnose": helpFlags(t, cmdDiagnose),
		"validate": helpFlags(t, cmdValidate),
		"doctor":   helpFlags(t, cmdDoctor),
		"import":   helpFlags(t, cmdImportHAR),
	}
	for name, f := range known {
		if len(f) < 4 {
			t.Fatalf("could not read the flags of %q", name)
		}
	}

	// Join "\" continuation lines inside code fences, then look at each command.
	text := strings.ReplaceAll(string(data), "\\\n", " ")
	inFence := false
	checked := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if !inFence {
			continue
		}
		i := strings.Index(line, "vegaload ")
		if i < 0 {
			continue
		}
		fields := strings.Fields(line[i+len("vegaload "):])
		if len(fields) == 0 {
			continue
		}
		cmd := fields[0]
		if cmd == "import" && len(fields) > 1 && fields[1] == "har" {
			fields = fields[1:]
		}
		flags, ok := known[cmd]
		if !ok {
			t.Errorf("cookbook uses an unknown command %q: %s", cmd, line)
			continue
		}
		for _, f := range fields[1:] {
			if !strings.HasPrefix(f, "-") {
				continue
			}
			name := strings.TrimLeft(f, "-")
			if eq := strings.Index(name, "="); eq >= 0 {
				name = name[:eq]
			}
			if name == "" || name[0] < 'A' {
				continue // a negative number or a stray dash
			}
			checked++
			if !flags[name] {
				t.Errorf("cookbook: %q is not a flag of vegaload %s: %s", f, cmd, strings.TrimSpace(line))
			}
		}
	}
	if checked < 30 {
		t.Errorf("only %d flags were checked; did the cookbook move?", checked)
	}
}

// Every scenario file the cookbook names must exist.
func TestCookbookFilesExist(t *testing.T) {
	root := filepath.Join("..", "..", "examples")
	data, err := os.ReadFile(filepath.Join(root, "cookbook", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`\]\((\.\./[A-Za-z0-9_./-]+)\)`)
	links := re.FindAllStringSubmatch(string(data), -1)
	if len(links) == 0 {
		t.Fatal("no file links found in the cookbook")
	}
	for _, m := range links {
		if _, err := os.Stat(filepath.Join(root, "cookbook", m[1])); err != nil {
			t.Errorf("cookbook links to %s, which is missing", m[1])
		}
	}
}
