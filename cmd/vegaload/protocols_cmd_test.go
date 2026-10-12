package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func captureCmd(t *testing.T, fn func() int) (int, string, string) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	outC := make(chan string)
	errC := make(chan string)
	go func() {
		b, _ := io.ReadAll(outR)
		outC <- string(b)
	}()
	go func() {
		b, _ := io.ReadAll(errR)
		errC <- string(b)
	}()
	code := fn()
	outW.Close()
	errW.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	return code, <-outC, <-errC
}

func TestCmdProtocols_Table(t *testing.T) {
	code, stdout, stderr := captureCmd(t, func() int { return cmdProtocols(nil) })
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	if !strings.HasPrefix(stdout, "PROTOCOL") {
		t.Fatalf("table missing header:\n%s", stdout)
	}
	for _, d := range drivers {
		if !strings.Contains(stdout, d.name) || !strings.Contains(stdout, d.since) {
			t.Errorf("table missing %s %s", d.name, d.since)
		}
		for _, call := range d.scriptCalls() {
			if !strings.Contains(stdout, call) {
				t.Errorf("table missing call %s", call)
			}
		}
	}
	if strings.Contains(stdout, "\x1b") {
		t.Error("table uses a terminal code")
	}
}

func TestCmdProtocols_DetailAndJSON(t *testing.T) {
	code, stdout, stderr := captureCmd(t, func() int { return cmdProtocols([]string{"sftp"}) })
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	if !strings.HasPrefix(stdout, "sftp (since 0.9.0)\n") {
		t.Fatalf("first line = %q", stdout)
	}
	for _, want := range []string{
		"SFTP over SSH: upload, download, list, stat, delete and roundtrip.",
		"URL schemes:  sftp",
		"-protocol:    sftp",
		"sftp.connect",
		"sftp.roundtrip",
		"TLS options:  no (SFTP uses SSH host keys: host_key)",
		"allow_writes=true",
		"allow_admin=true",
		"examples/scenarios/sftp-transfer.vl.js",
		"examples/scenarios/sftp_transfer.py",
		"vegaload doctor -target sftp://HOST",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("detail missing %q\n%s", want, stdout)
		}
	}

	code, stdout, stderr = captureCmd(t, func() int { return cmdProtocols([]string{"sftp", "-output", "json"}) })
	if code != 0 || stderr != "" {
		t.Fatalf("json code %d stderr %q", code, stderr)
	}
	var doc protocolsDocument
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != 1 || len(doc.Protocols) != 1 || doc.Protocols[0].Name != "sftp" {
		t.Fatalf("doc = %+v", doc)
	}
	if doc.Protocols[0].Status != "supported" || doc.Protocols[0].TLSOptions {
		t.Fatalf("sftp record = %+v", doc.Protocols[0])
	}
	if strings.Contains(stdout, ": null") || strings.Contains(stdout, ":null") {
		t.Fatalf("json has a null list:\n%s", stdout)
	}
}

func TestCmdProtocols_ListJSONOrder(t *testing.T) {
	code, stdout, stderr := captureCmd(t, func() int { return cmdProtocols([]string{"-output", "json"}) })
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	var doc protocolsDocument
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != 1 || len(doc.Protocols) != len(drivers) {
		t.Fatalf("len %d", len(doc.Protocols))
	}
	for i, d := range drivers {
		if doc.Protocols[i].Name != d.name {
			t.Errorf("protocol %d = %s, want %s", i, doc.Protocols[i].Name, d.name)
		}
		if doc.Protocols[i].Modes == nil || doc.Protocols[i].Options == nil || doc.Protocols[i].Examples == nil {
			t.Errorf("%s has a nil list", d.name)
		}
	}
	if strings.Contains(stdout, "null") {
		t.Fatalf("json has null:\n%s", stdout)
	}
}

func TestCmdProtocols_BadOutputAndUnknown(t *testing.T) {
	code, _, stderr := captureCmd(t, func() int { return cmdProtocols([]string{"-output", "jsonl"}) })
	if code != 2 || !strings.Contains(stderr, "want text or json") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	code, stdout, stderr := captureCmd(t, func() int { return cmdProtocols([]string{"bogus"}) })
	if code != 2 || stdout != "" {
		t.Fatalf("code %d stdout %q", code, stdout)
	}
	if !strings.Contains(stderr, "unknown -protocol \"bogus\"") || !strings.Contains(stderr, "http1") || !strings.Contains(stderr, "vegaload protocols") {
		t.Fatalf("stderr = %q", stderr)
	}
}
