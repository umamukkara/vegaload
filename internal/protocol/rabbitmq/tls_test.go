package rabbitmq

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
	"github.com/vegaload/vegaload/internal/protocol/rabbitmq/rabbitmqtest"
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
		url  string
		opts map[string]string
		skip bool
		want string
		hide string
	}{
		{"amqps://127.0.0.1:1", map[string]string{"ca_file": missing}, false, "ca_file: cannot read " + missing, ""},
		{"amqps://127.0.0.1:1", map[string]string{"ca_file": leak}, false, "has no PEM certificate", "LEAKMARKER"},
		{"amqps://127.0.0.1:1", map[string]string{"cert_file": m.ClientCertPath, "key_file": leak}, false, "has no PEM private key", "LEAKMARKER"},
		{"amqps://127.0.0.1:1", map[string]string{"cert_file": m.ClientCertPath}, false, "cert_file needs key_file and key_file needs cert_file", ""},
		{"amqps://127.0.0.1:1", map[string]string{"cert_file": m.ClientCertPath, "key_file": tlstest.New(t).ClientKeyPath}, false, "do not match", ""},
		{"amqps://127.0.0.1:1", map[string]string{"ca_file": m.CACertPath}, true, "ca_file cannot be used with -insecure", ""},
		{"amqp://127.0.0.1:1", map[string]string{"tls": "skip-verify", "ca_file": m.CACertPath}, false, "ca_file cannot be used with -insecure", ""},
		{"amqp://127.0.0.1:1", map[string]string{"ca_file": m.CACertPath}, false, "ca_file needs TLS, but TLS is off", ""},
		{"amqp://127.0.0.1:1", map[string]string{"tls_server_name": "vegaload.test"}, false, "tls_server_name needs TLS, but TLS is off", ""},
		{"amqp://127.0.0.1:1", map[string]string{"cert_file": m.ClientCertPath}, false, "cert_file needs TLS, but TLS is off", ""},
		{"amqp://127.0.0.1:1", map[string]string{"key_file": m.ClientKeyPath}, false, "key_file needs TLS, but TLS is off", ""},
		{"amqps://127.0.0.1:1", map[string]string{"cert_file": m.ClientCertPath, "key_file": enc}, false, "is an encrypted key, which is not supported", ""},
	}
	for _, c := range cases {
		_, err := New(protocol.Target{URL: c.url, Body: []byte("x"), Options: c.opts, InsecureSkipVerify: c.skip}, time.Second)
		if err == nil || !strings.HasPrefix(err.Error(), "rabbitmq:") || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%v, want rabbitmq: ... %s", err, c.want)
		}
		if c.hide != "" && strings.Contains(err.Error(), c.hide) {
			t.Fatalf("leaked %s: %v", c.hide, err)
		}
	}
	d, err := New(protocol.Target{
		URL: "amqp://127.0.0.1:1", Body: []byte("x"),
		Options: map[string]string{"mode": "publish", "mandatory": "false", "tls": "skip-verify", "cert_file": m.ClientCertPath, "key_file": m.ClientKeyPath},
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
}

func TestTLSFiles_Handshake(t *testing.T) {
	m := tlstest.New(t)
	other := tlstest.New(t)
	line := pemLine(t, m.ClientKeyPath)
	s := rabbitmqtest.StartTLSConfig(t, m.ServerConfig(false))
	if res := runRMQ(t, s.URL(), map[string]string{"mandatory": "false"}); res.Success || !strings.Contains(res.Err.Error(), "x509") {
		t.Fatalf("no ca: %+v", res.Err)
	}
	ok := map[string]string{"mandatory": "false", "ca_file": m.CACertPath, "tls_server_name": tlstest.Name}
	if res := runRMQ(t, s.URL(), ok); !res.Success {
		t.Fatal(res.Err)
	}
	wrong := runRMQ(t, s.URL(), map[string]string{"mandatory": "false", "ca_file": other.CACertPath, "tls_server_name": tlstest.Name})
	if wrong.Success || !strings.Contains(wrong.Err.Error(), "x509") || strings.Contains(wrong.Err.Error(), line) {
		t.Fatalf("wrong CA: %+v", wrong.Err)
	}
	if res := runRMQ(t, s.URL(), map[string]string{"mandatory": "false", "ca_file": m.CACertPath}); res.Success || !strings.Contains(res.Err.Error(), "certificate") {
		t.Fatalf("name: %+v", res.Err)
	}

	auth := rabbitmqtest.StartTLSConfig(t, m.ServerConfig(true))
	if res := runRMQ(t, auth.URL(), ok); res.Success {
		t.Fatal("client cert was not required")
	}
	good := copyOpt(ok, "cert_file", m.ClientCertPath, "key_file", m.ClientKeyPath)
	if res := runRMQ(t, auth.URL(), good); !res.Success {
		t.Fatal(res.Err)
	}
	bad := copyOpt(ok, "cert_file", other.ClientCertPath, "key_file", other.ClientKeyPath)
	res := runRMQ(t, auth.URL(), bad)
	if res.Success || strings.Contains(res.Err.Error(), line) {
		t.Fatalf("other client: %+v", res.Err)
	}
}

func TestTLSFiles_CallKeepsFiles(t *testing.T) {
	m := tlstest.New(t)
	s := rabbitmqtest.StartTLSConfig(t, m.ServerConfig(false))
	parent, err := NewConn(protocol.Target{
		URL:     s.URL(),
		Options: map[string]string{"ca_file": m.CACertPath, "tls_server_name": tlstest.Name},
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
			c, err := parent.Call(map[string]string{"mode": "publish", "mandatory": "false"}, []byte("x"), 3*time.Second)
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

func runRMQ(t *testing.T, url string, opts map[string]string) protocol.Result {
	t.Helper()
	d, err := New(protocol.Target{URL: url, Body: []byte("x"), Options: opts}, 3*time.Second)
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

func copyOpt(in map[string]string, extra ...string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	for i := 0; i+1 < len(extra); i += 2 {
		out[extra[i]] = extra[i+1]
	}
	return out
}

func pemLine(t *testing.T, path string) string {
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
