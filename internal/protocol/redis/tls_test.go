package redis

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
	"github.com/vegaload/vegaload/internal/protocol/redis/redistest"
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
		name string
		url  string
		opts map[string]string
		skip bool
		want string
		hide string
	}{
		{"missing", "rediss://127.0.0.1:1", map[string]string{"ca_file": missing}, false, "ca_file: cannot read " + missing, ""},
		{"no pem", "rediss://127.0.0.1:1", map[string]string{"ca_file": leak}, false, "has no PEM certificate", "LEAKMARKER"},
		{"leak key", "rediss://127.0.0.1:1", map[string]string{"cert_file": m.ClientCertPath, "key_file": leak}, false, "has no PEM private key", "LEAKMARKER"},
		{"need both", "rediss://127.0.0.1:1", map[string]string{"cert_file": m.ClientCertPath}, false, "cert_file needs key_file and key_file needs cert_file", ""},
		{"mismatch", "rediss://127.0.0.1:1", map[string]string{"cert_file": m.ClientCertPath, "key_file": tlstest.New(t).ClientKeyPath}, false, "cert_file and key_file do not match", ""},
		{"insecure", "rediss://127.0.0.1:1", map[string]string{"ca_file": m.CACertPath}, true, "ca_file cannot be used with -insecure", ""},
		{"skip", "redis://127.0.0.1:1", map[string]string{"tls": "skip-verify", "ca_file": m.CACertPath}, false, "ca_file cannot be used with -insecure", ""},
		{"tls off", "redis://127.0.0.1:1", map[string]string{"ca_file": m.CACertPath}, false, "ca_file needs TLS, but TLS is off", ""},
		{"name off", "redis://127.0.0.1:1", map[string]string{"tls_server_name": "vegaload.test"}, false, "tls_server_name needs TLS, but TLS is off", ""},
		{"cert off", "redis://127.0.0.1:1", map[string]string{"cert_file": m.ClientCertPath}, false, "cert_file needs TLS, but TLS is off", ""},
		{"key off", "redis://127.0.0.1:1", map[string]string{"key_file": m.ClientKeyPath}, false, "key_file needs TLS, but TLS is off", ""},
		{"encrypted", "rediss://127.0.0.1:1", map[string]string{"cert_file": m.ClientCertPath, "key_file": enc}, false, "is an encrypted key, which is not supported", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(protocol.Target{
				URL: c.url, Body: []byte("PING"), Options: c.opts, InsecureSkipVerify: c.skip,
			}, time.Second)
			if err == nil || !strings.HasPrefix(err.Error(), "redis:") || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("%v, want redis: ... %s", err, c.want)
			}
			if c.hide != "" && strings.Contains(err.Error(), c.hide) {
				t.Fatalf("leaked %s: %v", c.hide, err)
			}
		})
	}
	d, err := New(protocol.Target{
		URL: "redis://127.0.0.1:1", Body: []byte("PING"),
		Options: map[string]string{"tls": "skip-verify", "cert_file": m.ClientCertPath, "key_file": m.ClientKeyPath},
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
}

func TestTLSFiles_Handshake(t *testing.T) {
	m := tlstest.New(t)
	other := tlstest.New(t)
	line := keyLine(t, m.ClientKeyPath)

	s := redistest.StartTLSConfig(t, nil, m.ServerConfig(false))
	if res, _ := runRedis(t, s.URL(), map[string]string{"tls": "true"}); res.Success || !strings.Contains(res.Err.Error(), "x509") {
		t.Fatalf("no ca: %+v", res.Err)
	}
	if res, _ := runRedis(t, s.URL(), map[string]string{"tls": "true", "ca_file": m.CACertPath, "tls_server_name": tlstest.Name}); !res.Success {
		t.Fatal(res.Err)
	}
	wrong := resOf(t, s.URL(), map[string]string{"tls": "true", "ca_file": other.CACertPath, "tls_server_name": tlstest.Name})
	if !strings.Contains(wrong.Err.Error(), "x509") {
		t.Fatal(wrong.Err)
	}
	if strings.Contains(wrong.Err.Error(), line) {
		t.Fatal("wrong CA error contains the key")
	}
	if res, _ := runRedis(t, s.URL(), map[string]string{"tls": "true", "ca_file": m.CACertPath}); res.Success || !strings.Contains(res.Err.Error(), "certificate") {
		t.Fatalf("name: %+v", res.Err)
	}

	auth := redistest.StartTLSConfig(t, nil, m.ServerConfig(true))
	if res, _ := runRedis(t, auth.URL(), map[string]string{"tls": "true", "ca_file": m.CACertPath, "tls_server_name": tlstest.Name}); res.Success {
		t.Fatal("client cert was not required")
	}
	good := map[string]string{
		"tls": "true", "ca_file": m.CACertPath, "tls_server_name": tlstest.Name,
		"cert_file": m.ClientCertPath, "key_file": m.ClientKeyPath,
	}
	res, _ := runRedis(t, auth.URL(), good)
	if !res.Success {
		t.Fatal(res.Err)
	}
	if res.Err != nil && strings.Contains(res.Err.Error(), line) {
		t.Fatal("success text contains the key")
	}
	bad := map[string]string{
		"tls": "true", "ca_file": m.CACertPath, "tls_server_name": tlstest.Name,
		"cert_file": other.ClientCertPath, "key_file": other.ClientKeyPath,
	}
	if res, _ = runRedis(t, auth.URL(), bad); res.Success {
		t.Fatal("a client cert from another CA was accepted")
	}
	if strings.Contains(res.Err.Error(), line) {
		t.Fatal("failure text contains the key")
	}
}

func TestTLSFiles_CallKeepsFiles(t *testing.T) {
	m := tlstest.New(t)
	s := redistest.StartTLSConfig(t, nil, m.ServerConfig(false))
	parent, err := NewConn(protocol.Target{
		URL: s.URL(),
		Options: map[string]string{
			"tls": "true", "ca_file": m.CACertPath, "tls_server_name": tlstest.Name, "pool": "8",
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
			c, err := parent.Call(map[string]string{}, []byte("PING"), 3*time.Second)
			if err != nil {
				errc <- err
				return
			}
			res, err := c.Do(context.Background())
			if err != nil || !res.Success {
				errc <- res.Err
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

func runRedis(t *testing.T, url string, opts map[string]string) (protocol.Result, error) {
	t.Helper()
	d, err := New(protocol.Target{URL: url, Body: []byte("PING"), Options: opts}, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d.Do(context.Background())
}

func resOf(t *testing.T, url string, opts map[string]string) protocol.Result {
	t.Helper()
	res, err := runRedis(t, url, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Err == nil {
		t.Fatal("expected a failed result")
	}
	return res
}

func keyLine(t *testing.T, path string) string {
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
	t.Fatal("no key line")
	return ""
}
