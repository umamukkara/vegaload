package sftp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/protocol/xfercommon"
)

// Live tests run only when VEGALOAD_TEST_SFTP is set to an sftp:// URL.
// VEGALOAD_TEST_SFTP_PASSWORD_ENV names the environment variable that holds
// the password. VEGALOAD_TEST_SFTP_KEY_FILE is an optional private key.
// VEGALOAD_TEST_SFTP_KEY_PASSPHRASE_ENV names the passphrase variable.
// VEGALOAD_TEST_SFTP_HOST_KEY is the SHA256 fingerprint. Empty means -insecure.
// VEGALOAD_TEST_SFTP_DIR is the scratch directory (default .).
// Only names that start with vegaload-test- are deleted.
func TestLive_SFTP(t *testing.T) {
	raw := os.Getenv("VEGALOAD_TEST_SFTP")
	if raw == "" {
		t.Skip("VEGALOAD_TEST_SFTP is not set")
	}
	dir := os.Getenv("VEGALOAD_TEST_SFTP_DIR")
	if dir == "" {
		dir = "."
	}
	base := liveOpts(t)
	clean := func() {
		t.Helper()
		liveClean(t, raw, dir, base)
	}
	clean()
	t.Cleanup(func() { liveClean(t, raw, dir, base) })

	once := func() {
		t.Helper()
		for _, size := range []string{"0", "1", "32KiB", "10MiB", "100MiB"} {
			path := dir + "/vegaload-test-{id}.bin"
			limit := 30 * time.Second
			if size == "100MiB" {
				limit = 3 * time.Minute
			}
			res, rep := liveRun(t, raw, modeRoundtrip, path, base, map[string]string{"size": size, "fill": "zero"}, limit)
			if !res.Success {
				t.Fatalf("roundtrip %s: %v", size, res.Err)
			}
			if strings.Contains(rep.Text, "left on the server") {
				t.Fatalf("roundtrip %s left a file: %s", size, rep.Text)
			}
		}
		res, rep := liveRun(t, raw, modeRoundtrip, dir+"/vegaload-test-{id}.bin", base, map[string]string{
			"size": "50MiB", "fill": "zero", "concurrent_writes": "true",
		}, 3*time.Minute)
		if !res.Success || rep.Size != 50<<20 || strings.Contains(rep.Text, "left on the server") {
			t.Fatalf("concurrent_writes roundtrip: success=%v size=%d text=%q err=%v", res.Success, rep.Size, rep.Text, res.Err)
		}
		name := fmt.Sprintf("vegaload-test-%d.bin", time.Now().UnixNano())
		path := dir + "/" + name
		res, _ = liveRun(t, raw, modeUpload, path, base, map[string]string{"size": "32", "fill": "text"}, 15*time.Second)
		if !res.Success {
			t.Fatal(res.Err)
		}
		res, rep = liveRun(t, raw, modeList, dir, base, map[string]string{"expect": name}, 15*time.Second)
		if !res.Success || rep.Total < 1 {
			t.Fatalf("list: %v total %d", res.Err, rep.Total)
		}
		res, rep = liveRun(t, raw, modeStat, path, base, nil, 15*time.Second)
		if !res.Success || rep.Size != 32 || !strings.Contains(rep.Text, "type file") {
			t.Fatalf("stat: %v %q size %d", res.Err, rep.Text, rep.Size)
		}
		res, _ = liveRun(t, raw, modeDownload, dir+"/vegaload-test-missing.bin", base, nil, 15*time.Second)
		if res.Success || !strings.Contains(res.Err.Error(), "no such file") {
			t.Fatal(res.Err)
		}
		res, _ = liveRun(t, raw, modeStat, path, base, nil, 15*time.Second)
		if !res.Success {
			t.Fatal(res.Err)
		}
		res, _ = liveRun(t, raw, modeDelete, path, base, nil, 15*time.Second)
		if !res.Success {
			t.Fatal(res.Err)
		}
	}
	once()
	once()

	wrong := liveOpts(t)
	wrongName := "VEGALOAD_TEST_SFTP_WRONG_PW"
	t.Setenv(wrongName, "not-the-password")
	wrong["password_env"] = wrongName
	delete(wrong, "key_file")
	delete(wrong, "key_passphrase_env")
	res, _ := liveRun(t, raw, modeConnect, "", wrong, nil, 15*time.Second)
	if res.Success || res.Err == nil || strings.Contains(res.Err.Error(), "not-the-password") || !strings.Contains(res.Err.Error(), "refused the login") {
		t.Fatal(res.Err)
	}

	if fp := os.Getenv("VEGALOAD_TEST_SFTP_HOST_KEY"); fp != "" {
		good := liveOpts(t)
		good["host_key"] = fp
		res, _ = liveRun(t, raw, modeConnect, "", good, nil, 15*time.Second)
		if !res.Success {
			t.Fatal(res.Err)
		}
		bad := liveOpts(t)
		bad["host_key"] = "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
		res, _ = liveRun(t, raw, modeConnect, "", bad, nil, 15*time.Second)
		if res.Success || !strings.Contains(res.Err.Error(), "does not match") || strings.Contains(res.Err.Error(), "-insecure") {
			t.Fatal(res.Err)
		}
	}
	none := liveOpts(t)
	delete(none, "host_key")
	res, _ = liveRun(t, raw, modeConnect, "", none, map[string]string{"insecure": "off"}, 15*time.Second)
	if res.Success || res.Err == nil || !strings.Contains(res.Err.Error(), "not trusted") {
		t.Fatal(res.Err)
	}

	if os.Getenv("VEGALOAD_TEST_SFTP_KEY_FILE") != "" {
		keyOpts := liveOpts(t)
		delete(keyOpts, "password_env")
		res, _ = liveRun(t, raw, modeConnect, "", keyOpts, nil, 15*time.Second)
		if !res.Success {
			t.Fatal(res.Err)
		}
	}

	fixed := dir + "/vegaload-test-fixed.bin"
	noAdmin := liveOpts(t)
	noAdmin["allow_writes"] = "true"
	noAdmin["allow_admin"] = "false"
	res, _ = liveRun(t, raw, modeUpload, fixed, noAdmin, map[string]string{"size": "1"}, 15*time.Second)
	if res.Success || !strings.Contains(res.Err.Error(), "allow_admin") {
		t.Fatal(res.Err)
	}
	res, _ = liveRun(t, raw, modeDelete, fixed, noAdmin, nil, 15*time.Second)
	if res.Success || !strings.Contains(res.Err.Error(), "allow_admin") {
		t.Fatal(res.Err)
	}
	res, _ = liveRun(t, raw, modeUpload, fixed, base, map[string]string{"size": "1"}, 15*time.Second)
	if !res.Success {
		t.Fatal(res.Err)
	}
	res, _ = liveRun(t, raw, modeDelete, dir, base, nil, 15*time.Second)
	if res.Success || !strings.Contains(res.Err.Error(), "directory") {
		t.Fatal(res.Err)
	}
	res, _ = liveRun(t, raw, modeDelete, fixed, base, nil, 15*time.Second)
	if !res.Success {
		t.Fatal(res.Err)
	}

	for _, n := range []string{"1", "4"} {
		opt := map[string]string{"connections": n, "size": "8", "fill": "zero"}
		if err := liveTogether(t, raw, dir+"/vegaload-test-{id}.bin", base, opt); err != nil {
			t.Fatalf("connections=%s: %v", n, err)
		}
	}
	per := map[string]string{"connection": "per_call", "size": "8", "fill": "zero"}
	res, _ = liveRun(t, raw, modeRoundtrip, dir+"/vegaload-test-{id}.bin", base, per, 20*time.Second)
	if !res.Success {
		t.Fatal(res.Err)
	}
}

func liveOpts(t *testing.T) map[string]string {
	t.Helper()
	opts := map[string]string{"allow_writes": "true", "allow_admin": "true"}
	if name := os.Getenv("VEGALOAD_TEST_SFTP_PASSWORD_ENV"); name != "" {
		opts["password_env"] = name
	}
	if path := os.Getenv("VEGALOAD_TEST_SFTP_KEY_FILE"); path != "" {
		opts["key_file"] = path
	}
	if name := os.Getenv("VEGALOAD_TEST_SFTP_KEY_PASSPHRASE_ENV"); name != "" {
		opts["key_passphrase_env"] = name
	}
	if fp := os.Getenv("VEGALOAD_TEST_SFTP_HOST_KEY"); fp != "" {
		opts["host_key"] = fp
	}
	if opts["password_env"] == "" && opts["key_file"] == "" {
		t.Fatal("set VEGALOAD_TEST_SFTP_PASSWORD_ENV or VEGALOAD_TEST_SFTP_KEY_FILE")
	}
	return opts
}

func liveTarget(raw, mode, path string, base, extra map[string]string) protocol.Target {
	opt := map[string]string{}
	for k, v := range base {
		opt[k] = v
	}
	for k, v := range extra {
		if k == "insecure" {
			continue
		}
		opt[k] = v
	}
	opt["mode"] = mode
	if path != "" {
		opt["path"] = path
	}
	insecure := os.Getenv("VEGALOAD_TEST_SFTP_HOST_KEY") == ""
	if extra != nil && extra["insecure"] == "off" {
		insecure = false
		delete(opt, "host_key")
	}
	return protocol.Target{URL: raw, Options: opt, InsecureSkipVerify: insecure}
}

func liveRun(t *testing.T, raw, mode, path string, base, extra map[string]string, timeout time.Duration) (protocol.Result, xfercommon.Reply) {
	t.Helper()
	d, err := New(liveTarget(raw, mode, path, base, extra), timeout)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	return d.Run(context.Background())
}

func liveTogether(t *testing.T, raw, path string, base, extra map[string]string) error {
	t.Helper()
	// One driver, so connections=1 is one SSH connection shared by every call.
	parent := map[string]string{}
	if v := extra["connections"]; v != "" {
		parent["connections"] = v
	}
	if v := extra["connection"]; v != "" {
		parent["connection"] = v
	}
	d, err := New(liveTarget(raw, modeConnect, "", base, parent), 30*time.Second)
	if err != nil {
		return err
	}
	defer d.Close()
	if res, _ := d.Run(context.Background()); !res.Success {
		return res.Err
	}
	job := map[string]string{"mode": modeRoundtrip, "path": path, "size": extra["size"], "fill": extra["fill"]}
	var wg sync.WaitGroup
	errc := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := d.Call(job, nil, 30*time.Second)
			if err != nil {
				errc <- err
				return
			}
			res, _ := c.Run(context.Background())
			if !res.Success {
				errc <- res.Err
			}
		}()
	}
	wg.Wait()
	select {
	case err := <-errc:
		return err
	default:
		return nil
	}
}

func liveClean(t *testing.T, raw, dir string, base map[string]string) {
	t.Helper()
	d, err := New(liveTarget(raw, modeList, dir, base, map[string]string{"limit": "100000"}), 20*time.Second)
	if err != nil {
		t.Log(err)
		return
	}
	res, rep := d.Run(context.Background())
	_ = d.Close()
	if !res.Success {
		t.Log(res.Err)
		return
	}
	for _, e := range rep.Entries {
		if !strings.HasPrefix(e.Name, "vegaload-test-") || e.Type != "file" {
			continue
		}
		del, err := New(liveTarget(raw, modeDelete, dir+"/"+e.Name, base, nil), 20*time.Second)
		if err != nil {
			t.Log(err)
			continue
		}
		_, _ = del.Run(context.Background())
		_ = del.Close()
	}
}
