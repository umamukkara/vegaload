package sftp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/protocol/sftp/sftptest"
	"github.com/vegaload/vegaload/internal/protocol/xfercommon"
)

func srv(t *testing.T) *sftptest.Server {
	t.Helper()
	return sftptest.Start(t)
}

func drive(t *testing.T, s *sftptest.Server, mode string, opt map[string]string, body string, timeout time.Duration) (*Driver, protocol.Result, xfercommon.Reply) {
	t.Helper()
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	t.Setenv("SFTP_PW", "secret")
	cp := map[string]string{}
	for k, v := range opt {
		cp[k] = v
	}
	opt = cp
	opt["mode"] = mode
	if _, ok := opt["password_env"]; !ok && opt["key_file"] == "" {
		opt["password_env"] = "SFTP_PW"
	}
	insecure := true
	if _, ok := opt["host_key"]; ok {
		insecure = false
	}
	if v, ok := opt["insecure"]; ok {
		insecure = v == "true"
		delete(opt, "insecure")
	}
	d, err := New(protocol.Target{
		URL: s.URL(), Body: []byte(body), Options: opt, InsecureSkipVerify: insecure,
	}, timeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	res, rep := d.Run(context.Background())
	return d, res, rep
}

func run(t *testing.T, s *sftptest.Server, mode string, opt map[string]string, body string, timeout time.Duration) (protocol.Result, xfercommon.Reply) {
	t.Helper()
	_, res, rep := drive(t, s, mode, opt, body, timeout)
	return res, rep
}

func TestSmoke_Connect(t *testing.T) {
	s := srv(t)
	res, rep := run(t, s, modeConnect, nil, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	if rep.Text != "logged in" || rep.Timing.ConnectMs < 0 || rep.Timing.LoginMs < 0 {
		t.Fatalf("%+v", rep)
	}
	if s.ConnCount() == 0 {
		t.Fatal("connect did not dial")
	}
	if reqs := s.Requests(); len(reqs) != 0 {
		t.Fatalf("unexpected session requests %v", reqs)
	}
}

func TestNew_DoesNotConnect(t *testing.T) {
	s := srv(t)
	t.Setenv("SFTP_PW", "secret")
	d, err := New(protocol.Target{
		URL: s.URL(), Options: map[string]string{"mode": "connect", "password_env": "SFTP_PW"},
		InsecureSkipVerify: true,
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if s.ConnCount() != 0 {
		t.Fatalf("New dialed %d connections", s.ConnCount())
	}
}

func TestBadOptions(t *testing.T) {
	s := srv(t)
	t.Setenv("SFTP_PW", "secret")
	base := func(opt map[string]string) protocol.Target {
		return protocol.Target{URL: s.URL(), Options: opt, InsecureSkipVerify: true}
	}
	cases := []struct {
		name string
		t    protocol.Target
		want string
		hide string
	}{
		{"no user", protocol.Target{URL: "sftp://127.0.0.1:1", Options: map[string]string{"password_env": "SFTP_PW"}, InsecureSkipVerify: true}, "username is required", ""},
		{"no secret", protocol.Target{URL: "sftp://load@127.0.0.1:1", Options: map[string]string{"mode": "connect"}, InsecureSkipVerify: true}, "set password_env or key_file", ""},
		{"url password", protocol.Target{URL: "sftp://load:super-secret-pw@127.0.0.1:1", InsecureSkipVerify: true}, "do not put a password", "super-secret-pw"},
		{"query", protocol.Target{URL: "sftp://load@127.0.0.1:1?token=query-secret", Options: map[string]string{"password_env": "SFTP_PW"}, InsecureSkipVerify: true}, "token", "query-secret"},
		{"url path", protocol.Target{URL: "sftp://load@127.0.0.1:1/secret/file", Options: map[string]string{"password_env": "SFTP_PW"}, InsecureSkipVerify: true}, "put the remote path", "secret/file"},
		{"ssh scheme", protocol.Target{URL: "ssh://load@127.0.0.1:1", Options: map[string]string{"password_env": "SFTP_PW"}, InsecureSkipVerify: true}, "sftp://", ""},
		{"no host", protocol.Target{URL: "sftp://load@", InsecureSkipVerify: true}, "no host", "load"},
		{"user clash", base(map[string]string{"username": "other", "password_env": "SFTP_PW", "mode": "connect"}), "they differ", ""},
		{"both size and body", protocol.Target{URL: s.URL(), Body: []byte("x"), Options: map[string]string{"mode": "upload", "path": "/a-{id}", "size": "1", "password_env": "SFTP_PW", "allow_writes": "true"}, InsecureSkipVerify: true}, "not both", ""},
		{"bad size", base(map[string]string{"mode": "upload", "path": "/a-{id}", "size": "10MB", "password_env": "SFTP_PW", "allow_writes": "true"}), "unit", ""},
		{"bad connections", base(map[string]string{"mode": "connect", "password_env": "SFTP_PW", "connections": "0"}), "1 to 256", ""},
		{"bad limit", base(map[string]string{"mode": "list", "password_env": "SFTP_PW", "limit": "0"}), "limit", ""},
		{"bad sha", base(map[string]string{"mode": "download", "path": "/a", "password_env": "SFTP_PW", "expect_sha256": "zz"}), "64 hex", ""},
		{"bad host key", base(map[string]string{"mode": "connect", "password_env": "SFTP_PW", "host_key": "not-a-fingerprint-secret"}), "SHA256:", "not-a-fingerprint-secret"},
		{"host key and insecure", protocol.Target{URL: s.URL(), Options: map[string]string{"mode": "connect", "password_env": "SFTP_PW", "host_key": s.HostKeyFingerprint()}, InsecureSkipVerify: true}, "cannot be used with -insecure", ""},
		{"passphrase without key", base(map[string]string{"mode": "connect", "password_env": "SFTP_PW", "key_passphrase_env": "SFTP_PP"}), "needs key_file", ""},
		{"tls option", base(map[string]string{"mode": "connect", "password_env": "SFTP_PW", "ca_file": "ca.pem"}), "unknown option", ""},
		{"foreign", base(map[string]string{"mode": "connect", "password_env": "SFTP_PW", "path": "/a"}), "not used in mode connect", ""},
		{"newline path", base(map[string]string{"mode": "download", "password_env": "SFTP_PW", "path": "a\nb"}), "line break", "a\nb"},
		{"long path", base(map[string]string{"mode": "download", "password_env": "SFTP_PW", "path": strings.Repeat("a", 5000)}), "4096", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.t, time.Second)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if tc.hide != "" && strings.Contains(err.Error(), tc.hide) {
				t.Fatal("the error contains a secret or a value it should not")
			}
			if s.ConnCount() != 0 {
				t.Fatal("a refused setup dialed")
			}
		})
	}
}

func TestHostKey(t *testing.T) {
	s := srv(t)
	t.Setenv("SFTP_PW", "secret")
	fp := s.HostKeyFingerprint()
	res, _ := run(t, s, modeConnect, map[string]string{"insecure": "false"}, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "not trusted") || !strings.Contains(res.Err.Error(), fp) || !strings.Contains(res.Err.Error(), "ssh-ed25519") {
		t.Fatal(res.Err)
	}
	res, _ = run(t, s, modeConnect, map[string]string{"host_key": fp}, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	res, _ = run(t, s, modeConnect, map[string]string{"host_key": "SHA256:" + strings.Repeat("A", 43)}, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "does not match") || strings.Contains(res.Err.Error(), "-insecure") {
		t.Fatal(res.Err)
	}
	res, _ = run(t, s, modeConnect, map[string]string{"host_key": "SHA256:" + strings.Repeat("A", 43) + "," + fp}, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	if err := s.AddRSA(); err != nil {
		t.Fatal(err)
	}
	rsaFP := s.RSAFingerprint()
	res, _ = run(t, s, modeConnect, map[string]string{"host_key": fp + "," + rsaFP}, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
}

func TestSource_OneInsecureHostKey(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	hits := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		if strings.Contains(text, "WithDebug") || strings.Contains(text, "ClientDebug") || strings.Contains(text, "RequestPty") || strings.Contains(text, "exec") {
			t.Errorf("%s contains a forbidden word", name)
		}
		if strings.Contains(text, "InsecureIgnoreHostKey") {
			hits++
			if !strings.Contains(text, "Only -insecure skips the host key check.") {
				t.Errorf("%s uses InsecureIgnoreHostKey outside the one allowed place", name)
			}
		}
	}
	if hits != 1 {
		t.Fatalf("InsecureIgnoreHostKey appears %d times, want 1", hits)
	}
}
