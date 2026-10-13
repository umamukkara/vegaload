package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/doctor"
	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/protocol/ftp"
	"github.com/vegaload/vegaload/internal/protocol/grpc"
	"github.com/vegaload/vegaload/internal/protocol/http1"
	"github.com/vegaload/vegaload/internal/protocol/http2"
	"github.com/vegaload/vegaload/internal/protocol/kafka"
	"github.com/vegaload/vegaload/internal/protocol/mqtt"
	"github.com/vegaload/vegaload/internal/protocol/mysql"
	"github.com/vegaload/vegaload/internal/protocol/postgres"
	"github.com/vegaload/vegaload/internal/protocol/rabbitmq"
	"github.com/vegaload/vegaload/internal/protocol/redis"
	"github.com/vegaload/vegaload/internal/protocol/sftp"
	"github.com/vegaload/vegaload/internal/protocol/socket"
	"github.com/vegaload/vegaload/internal/protocol/websocket"
	"github.com/vegaload/vegaload/internal/scripting/js"
	"github.com/vegaload/vegaload/internal/scripting/netapi"
)

func TestProtocolCatalog_IsComplete(t *testing.T) {
	since := regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	options := map[string][]string{
		"http1":     http1.Options,
		"http2":     http2.Options,
		"grpc":      grpc.Options,
		"websocket": websocket.Options,
		"mqtt":      mqtt.Options,
		"kafka":     kafka.Options,
		"postgres":  postgres.Options,
		"mysql":     mysql.Options,
		"redis":     redis.Options,
		"rabbitmq":  rabbitmq.Options,
		"ftp":       ftp.Options,
		"sftp":      sftp.Options,
		"tcp":       socket.TCPOptions,
		"udp":       socket.UDPOptions,
	}
	root := filepath.Join("..", "..")
	for _, d := range drivers {
		if d.summary == "" || strings.Contains(d.summary, "\n") || !strings.HasSuffix(d.summary, ".") {
			t.Errorf("%s summary %q want one sentence", d.name, d.summary)
		}
		if !since.MatchString(d.since) {
			t.Errorf("%s since %q want X.Y.Z", d.name, d.since)
		}
		if len(d.examples) == 0 && d.noExample == "" {
			t.Errorf("%s has no example and no reason", d.name)
		}
		if len(d.examples) > 0 && d.noExample != "" {
			t.Errorf("%s has both examples and a no-example reason", d.name)
		}
		for _, ex := range d.examples {
			if _, err := os.Stat(filepath.Join(root, ex)); err != nil {
				t.Errorf("%s example %s: %v", d.name, ex, err)
			}
		}
		got, ok := options[d.name]
		if !ok || !reflect.DeepEqual(d.options, got) {
			t.Errorf("%s options are not the driver package's Options", d.name)
		}
		if len(doctor.SchemesFor(d.name)) == 0 {
			t.Errorf("%s has no URL scheme", d.name)
		}
	}
}

func TestProtocolCatalog_ScriptCalls(t *testing.T) {
	fns := netapi.ProtoFunctions()
	claimed := map[string]string{}
	for _, d := range drivers {
		switch d.scriptGlobal {
		case "http", "ws":
			if _, ok := fns[d.name]; ok {
				t.Errorf("%s is in ProtoFunctions and also has script global %s", d.name, d.scriptGlobal)
			}
		case "":
			calls, ok := fns[d.name]
			if !ok || len(calls) == 0 {
				t.Errorf("%s has no ProtoFunctions namespace", d.name)
			}
			if prev, ok := claimed[d.name]; ok {
				t.Errorf("namespace %s claimed by %s and %s", d.name, prev, d.name)
			}
			claimed[d.name] = d.name
		default:
			t.Errorf("%s script global %q", d.name, d.scriptGlobal)
		}
	}
	for ns := range fns {
		if _, ok := findDriver(ns); !ok {
			t.Errorf("ProtoFunctions namespace %s has no driver", ns)
		}
	}
	py, err := os.ReadFile(filepath.Join("..", "..", "internal", "scripting", "python", "python.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(py)
	for _, name := range js.ScriptHTTPMethods() {
		if !strings.Contains(src, "def "+name+"(") {
			t.Errorf("Python HTTP class has no %s", name)
		}
	}
	for _, name := range js.ScriptWSMethods() {
		if !strings.Contains(src, "def "+name+"(") {
			t.Errorf("Python WebSocket classes have no %s", name)
		}
	}
}

func TestProtocolCatalog_ModesBuildWithoutAServer(t *testing.T) {
	// New checks the mode before it dials for every driver that has modes.
	for _, d := range drivers {
		if len(d.modes) == 0 {
			continue
		}
		for _, mode := range d.modes {
			_, err := d.build(modeTarget(d.name, mode), time.Second)
			if unknownMode(err) {
				t.Errorf("%s mode %s: %v", d.name, mode, err)
			}
		}
		_, err := d.build(modeTarget(d.name, "not-a-mode"), time.Second)
		if !unknownMode(err) {
			t.Errorf("%s bogus mode: %v", d.name, err)
		}
	}
}

func modeTarget(name, mode string) protocol.Target {
	tg := wireTestTarget(name)
	opts := map[string]string{}
	for k, v := range tg.Options {
		opts[k] = v
	}
	opts["mode"] = mode
	tg.Options = opts
	return tg
}

func unknownMode(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	if strings.Contains(s, "unknown mode") {
		return true
	}
	return strings.Contains(s, "mode=") && strings.Contains(s, ", want ")
}

func TestReadmeProtocolsTableMatchesCommand(t *testing.T) {
	got, err := protocolsTableBetweenMarkers(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := protocolReadmeTable()
	if got != want {
		t.Fatalf("the README table does not match `vegaload protocols`.\nRun `vegaload protocols` and update the marked table.\nwant:\n%sgot:\n%s", want, got)
	}
}

func TestDocsProtocolsTableMatchesCommand(t *testing.T) {
	got, err := protocolsTableBetweenMarkers(filepath.Join("..", "..", "website", "docs", "guides", "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := stripMarkdownLinks(protocolReadmeTable())
	got = stripMarkdownLinks(got)
	if got != want {
		t.Fatalf("the docs protocol table does not match `vegaload protocols`.\nUpdate the marked table in website/docs/guides/index.md.\nwant:\n%sgot:\n%s", want, got)
	}
}

func protocolsTableBetweenMarkers(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	const start = "<!-- vegaload:protocols -->\n"
	const end = "<!-- /vegaload:protocols -->"
	text := string(b)
	i := strings.Index(text, start)
	j := strings.Index(text, end)
	if i < 0 || j < 0 || j < i {
		return "", fmt.Errorf("%s is missing the vegaload:protocols markers", path)
	}
	return text[i+len(start) : j], nil
}

func stripMarkdownLinks(s string) string {
	return regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`).ReplaceAllString(s, "$1")
}

func TestCmdProtocols_EveryName(t *testing.T) {
	for _, d := range drivers {
		code, stdout, stderr := captureCmd(t, func() int { return cmdProtocols([]string{d.name}) })
		if code != 0 || stderr != "" {
			t.Fatalf("%s code %d stderr %s", d.name, code, stderr)
		}
		first, _, _ := strings.Cut(stdout, "\n")
		if !strings.HasPrefix(first, d.name+" (since ") {
			t.Errorf("%s first line = %q", d.name, first)
		}
	}
	if code, _, _ := captureCmd(t, func() int { return cmdProtocols([]string{"no-such"}) }); code != 2 {
		t.Fatalf("unknown name code %d", code)
	}
}
