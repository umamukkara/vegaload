package mysql

import (
	"context"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/protocol/mysql/mysqltest"
	"github.com/vegaload/vegaload/internal/tlsconf/tlstest"
)

func TestTLSFiles_Errors(t *testing.T) {
	m := tlstest.New(t)
	missing := filepath.Join(m.Dir, "missing.pem")
	leak := filepath.Join(m.Dir, "leak.key")
	if err := os.WriteFile(leak, []byte("-----BEGIN PRIVATE KEY-----\nLEAKMARKER\n-----END PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	enc := filepath.Join(m.Dir, "enc.key")
	if err := os.WriteFile(enc, pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte("no")}), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		opts map[string]string
		skip bool
		want string
		hide string
	}{
		{map[string]string{"username": "app", "tls": "true", "ca_file": missing}, false, "ca_file: cannot read " + missing, ""},
		{map[string]string{"username": "app", "tls": "true", "ca_file": leak}, false, "has no PEM certificate", "LEAKMARKER"},
		{map[string]string{"username": "app", "tls": "true", "cert_file": m.ClientCertPath, "key_file": leak}, false, "has no PEM private key", "LEAKMARKER"},
		{map[string]string{"username": "app", "tls": "true", "cert_file": m.ClientCertPath}, false, "cert_file needs key_file", ""},
		{map[string]string{"username": "app", "tls": "true", "key_file": m.ClientKeyPath}, false, "key_file needs cert_file", ""},
		{map[string]string{"username": "app", "tls": "true", "cert_file": m.ClientCertPath, "key_file": tlstest.New(t).ClientKeyPath}, false, "do not match", ""},
		{map[string]string{"username": "app", "tls": "true", "ca_file": m.CACertPath}, true, "ca_file cannot be used with -insecure", ""},
		{map[string]string{"username": "app", "tls": "skip-verify", "ca_file": m.CACertPath}, false, "ca_file cannot be used with -insecure", ""},
		{map[string]string{"username": "app", "tls": "false", "ca_file": m.CACertPath}, false, "ca_file needs TLS, but TLS is off", ""},
		{map[string]string{"username": "app", "tls": "false", "tls_server_name": "vegaload.test"}, false, "tls_server_name needs TLS, but TLS is off", ""},
		{map[string]string{"username": "app", "tls": "false", "cert_file": m.ClientCertPath}, false, "cert_file needs TLS, but TLS is off", ""},
		{map[string]string{"username": "app", "tls": "false", "key_file": m.ClientKeyPath}, false, "key_file needs TLS, but TLS is off", ""},
		{map[string]string{"username": "app", "tls": "true", "cert_file": m.ClientCertPath, "key_file": enc}, false, "is an encrypted key, which is not supported", ""},
	}
	for _, c := range cases {
		_, err := New(protocol.Target{URL: "mysql://127.0.0.1:1/db", Body: []byte("select 1"), Options: c.opts, InsecureSkipVerify: c.skip}, time.Second)
		if err == nil || !strings.HasPrefix(err.Error(), "mysql:") || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%v, want mysql: ... %s", err, c.want)
		}
		if c.hide != "" && strings.Contains(err.Error(), c.hide) {
			t.Fatalf("leaked %s: %v", c.hide, err)
		}
	}
	d, err := New(protocol.Target{
		URL: "mysql://127.0.0.1:1/db", Body: []byte("select 1"),
		Options: map[string]string{"username": "app", "tls": "skip-verify", "cert_file": m.ClientCertPath, "key_file": m.ClientKeyPath},
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
}

func TestTLSFiles_Handshake(t *testing.T) {
	m := tlstest.New(t)
	other := tlstest.New(t)
	line := myPEMLine(t, m.ClientKeyPath)
	s := mysqltest.StartTLS(t, myOK, m.ServerConfig(false))
	if res := runMy(t, s.URL(), map[string]string{"username": "app", "tls": "true"}); res.Success || !strings.Contains(res.Err.Error(), "x509") {
		t.Fatalf("no ca: %+v", res.Err)
	}
	ok := map[string]string{"username": "app", "tls": "true", "ca_file": m.CACertPath, "tls_server_name": tlstest.Name}
	if res := runMy(t, s.URL(), ok); !res.Success {
		t.Fatal(res.Err)
	}
	wrong := runMy(t, s.URL(), map[string]string{"username": "app", "tls": "true", "ca_file": other.CACertPath, "tls_server_name": tlstest.Name})
	if wrong.Success || !strings.Contains(wrong.Err.Error(), "x509") || strings.Contains(wrong.Err.Error(), line) {
		t.Fatalf("wrong CA: %+v", wrong.Err)
	}
	if res := runMy(t, s.URL(), map[string]string{"username": "app", "tls": "true", "ca_file": m.CACertPath}); res.Success || !strings.Contains(res.Err.Error(), "certificate") {
		t.Fatalf("name: %+v", res.Err)
	}
	// preferred without a CA file still skips verification. With a CA file it checks.
	if res := runMy(t, s.URL(), map[string]string{"username": "app", "tls": "preferred"}); !res.Success {
		t.Fatal(res.Err)
	}
	if res := runMy(t, s.URL(), map[string]string{"username": "app", "tls": "preferred", "ca_file": other.CACertPath, "tls_server_name": tlstest.Name}); res.Success {
		t.Fatal("preferred with a CA file should check it")
	}

	auth := mysqltest.StartTLS(t, myOK, m.ServerConfig(true))
	if res := runMy(t, auth.URL(), ok); res.Success {
		t.Fatal("client cert was not required")
	}
	good := copyMy(ok, "cert_file", m.ClientCertPath, "key_file", m.ClientKeyPath)
	if res := runMy(t, auth.URL(), good); !res.Success {
		t.Fatal(res.Err)
	}
	bad := copyMy(ok, "cert_file", other.ClientCertPath, "key_file", other.ClientKeyPath)
	res := runMy(t, auth.URL(), bad)
	if res.Success || (res.Err != nil && strings.Contains(res.Err.Error(), line)) {
		t.Fatalf("other client: %+v", res.Err)
	}
}

func TestTLSFiles_CallKeepsFiles(t *testing.T) {
	m := tlstest.New(t)
	s := mysqltest.StartTLS(t, myOK, m.ServerConfig(false))
	parent, err := NewConn(protocol.Target{
		URL: s.URL(),
		Options: map[string]string{
			"username": "app", "tls": "true", "ca_file": m.CACertPath, "tls_server_name": tlstest.Name, "pool": "8",
		},
	}, 3*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close() })
	var wg sync.WaitGroup
	errc := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := parent.Call(map[string]string{}, []byte("select 1"), 3*time.Second)
			if err != nil {
				errc <- err
				return
			}
			res, err := c.Do(context.Background())
			if err != nil || !res.Success {
				if res.Err != nil {
					errc <- res.Err
				} else {
					errc <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func myOK(string) []mysqltest.Result {
	return []mysqltest.Result{{Columns: []mysqltest.Column{mysqltest.Int("id")}, Rows: [][]any{{"1"}}}}
}

func runMy(t *testing.T, url string, opts map[string]string) protocol.Result {
	t.Helper()
	d, err := New(protocol.Target{URL: url, Body: []byte("select 1"), Options: opts}, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	res, err := d.Do(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func copyMy(in map[string]string, extra ...string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	for i := 0; i+1 < len(extra); i += 2 {
		out[extra[i]] = extra[i+1]
	}
	return out
}

func myPEMLine(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if len(line) > 20 && !strings.Contains(line, "-----") {
			return line
		}
	}
	t.Fatal("no pem line")
	return ""
}
