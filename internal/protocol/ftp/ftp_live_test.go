package ftp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
)

// Live tests run only when VEGALOAD_TEST_FTP is set to an ftp:// or ftps://
// URL. VEGALOAD_TEST_FTP_USER is the account. VEGALOAD_TEST_FTP_PASSWORD_ENV
// names the environment variable that holds the password.
// VEGALOAD_TEST_FTP_DIR is the scratch directory (default /).
// VEGALOAD_TEST_FTP_TLS is explicit or implicit. VEGALOAD_TEST_FTP_INSECURE=1
// skips certificate checks.
func TestLive_UploadListDownloadAndRoundtrip(t *testing.T) {
	raw := os.Getenv("VEGALOAD_TEST_FTP")
	if raw == "" {
		t.Skip("VEGALOAD_TEST_FTP is not set")
	}
	base := liveOpts(t)
	dir := os.Getenv("VEGALOAD_TEST_FTP_DIR")
	if dir == "" {
		dir = "/"
	}
	dir = strings.TrimRight(dir, "/")
	name := fmt.Sprintf("vegaload-test-%d.bin", time.Now().UnixNano())
	path := dir + "/" + name
	if dir == "" {
		path = "/" + name
	}

	cleanup := liveTarget(raw, modeDelete, path, base, nil)
	t.Cleanup(func() {
		d, err := New(cleanup, 10*time.Second)
		if err != nil {
			return
		}
		_, _ = d.Run(context.Background())
		_ = d.Close()
	})

	once := func() {
		t.Helper()
		res, _ := liveRun(t, raw, modeUpload, path, "", base, map[string]string{"size": "1MiB", "fill": "zero"}, 30*time.Second)
		if !res.Success {
			t.Fatal(res.Err)
		}
		res, rep := liveRun(t, raw, modeList, dir, "", base, map[string]string{"expect": name}, 15*time.Second)
		if !res.Success {
			t.Fatal(res.Err)
		}
		if rep.Total < 1 {
			t.Fatalf("listing total %d", rep.Total)
		}
		res, rep = liveRun(t, raw, modeDownload, path, "", base, map[string]string{"expect_size": "1MiB"}, 30*time.Second)
		if !res.Success || rep.Size != 1<<20 {
			t.Fatalf("%v size %d", res.Err, rep.Size)
		}
		res, _ = liveRun(t, raw, modeDelete, path, "", base, nil, 15*time.Second)
		if !res.Success {
			t.Fatal(res.Err)
		}
	}
	once()
	once()

	for _, size := range []string{"10MiB", "100MiB"} {
		rt := dir + "/vegaload-test-{id}.bin"
		if dir == "" {
			rt = "/vegaload-test-{id}.bin"
		}
		res, rep := liveRun(t, raw, modeRoundtrip, rt, "", base, map[string]string{"size": size, "fill": "zero"}, 2*time.Minute)
		if !res.Success {
			t.Fatalf("%s: %v", size, res.Err)
		}
		if rep.Text != "" && strings.Contains(rep.Text, "left on the server") {
			t.Fatalf("%s left a file: %s", size, rep.Text)
		}
	}
}

func liveOpts(t *testing.T) map[string]string {
	t.Helper()
	opts := map[string]string{"allow_writes": "true", "allow_admin": "true"}
	if user := os.Getenv("VEGALOAD_TEST_FTP_USER"); user != "" {
		opts["username"] = user
	}
	if name := os.Getenv("VEGALOAD_TEST_FTP_PASSWORD_ENV"); name != "" {
		opts["password_env"] = name
	}
	switch os.Getenv("VEGALOAD_TEST_FTP_TLS") {
	case "", "none":
	case "explicit", "implicit":
		opts["tls"] = os.Getenv("VEGALOAD_TEST_FTP_TLS")
	default:
		t.Fatalf("VEGALOAD_TEST_FTP_TLS=%q, want explicit or implicit", os.Getenv("VEGALOAD_TEST_FTP_TLS"))
	}
	if os.Getenv("VEGALOAD_TEST_FTP_INSECURE") == "1" {
		opts["tls_verify"] = "false"
	}
	return opts
}

func liveTarget(raw, mode, path string, base, extra map[string]string) protocol.Target {
	opt := map[string]string{"mode": mode, "path": path}
	for k, v := range base {
		opt[k] = v
	}
	for k, v := range extra {
		opt[k] = v
	}
	tg := protocol.Target{URL: raw, Options: opt}
	if opt["tls_verify"] == "false" {
		tg.InsecureSkipVerify = true
	}
	return tg
}

func liveRun(t *testing.T, raw, mode, path, body string, base, extra map[string]string, timeout time.Duration) (protocol.Result, Reply) {
	t.Helper()
	d, err := New(liveTarget(raw, mode, path, base, extra), timeout)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if body != "" {
		d.target.Body = []byte(body)
	}
	return d.Run(context.Background())
}
