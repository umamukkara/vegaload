package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vegaload/vegaload/internal/protocol/ftp/ftptest"
	"github.com/vegaload/vegaload/internal/report"
)

func TestCmdRun_ProtocolThreshold(t *testing.T) {
	srv := ftptest.Start(t)
	srv.Apply(ftptest.Config{Files: map[string][]byte{"/pub/data.bin": []byte("hello ftp")}})

	run := func(t *testing.T, extra ...string) (int, report.Result) {
		t.Helper()
		dir := t.TempDir()
		out := filepath.Join(dir, "summary.json")
		args := []string{
			"-vus", "1", "-duration", "200ms", "-no-report",
			"-audit-log", filepath.Join(dir, "audit.log"), "-out", out,
			"-target", srv.URL(), "-protocol", "ftp",
			"-opt", "mode=download", "-opt", "path=/pub/data.bin",
		}
		args = append(args, extra...)
		code := cmdRun(args)
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		var res report.Result
		if err := json.Unmarshal(data, &res); err != nil {
			t.Fatal(err)
		}
		return code, res
	}

	t.Run("pass", func(t *testing.T) {
		code, res := run(t, "-threshold", `error_rate{protocol="ftp",op="download"} < 1%`)
		if code != 0 || len(res.Thresholds) != 1 || !res.Thresholds[0].Passed {
			t.Fatalf("code %d threshold %+v", code, res.Thresholds)
		}
		if res.Thresholds[0].Protocol != "ftp" || res.Thresholds[0].Op != "download" {
			t.Fatalf("selector %+v", res.Thresholds[0])
		}
	})

	t.Run("no calls", func(t *testing.T) {
		code, res := run(t, "-threshold", `p95{protocol="mqtt"} < 1s`)
		if code != 3 || len(res.Thresholds) != 1 || res.Thresholds[0].Passed {
			t.Fatalf("code %d threshold %+v", code, res.Thresholds)
		}
		if res.Thresholds[0].Observed != `no calls to protocol "mqtt" ran` {
			t.Fatalf("observed %q", res.Thresholds[0].Observed)
		}
	})

	t.Run("file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "thresholds.json")
		body := []byte(`[{"name":"ftp-fast","metric":"p95","protocol":"ftp","op":"download","operator":"<","value":"5s"}]`)
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		code, res := run(t, "-thresholds", path)
		if code != 0 || len(res.Thresholds) != 1 || !res.Thresholds[0].Passed || res.Thresholds[0].Name != "ftp-fast" {
			t.Fatalf("code %d threshold %+v", code, res.Thresholds)
		}
		if res.Thresholds[0].Protocol != "ftp" || res.Thresholds[0].Op != "download" {
			t.Fatalf("selector %+v", res.Thresholds[0])
		}
	})
}
