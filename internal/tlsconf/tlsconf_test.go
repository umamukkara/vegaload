package tlsconf

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vegaload/vegaload/internal/tlsconf/tlstest"
)

func TestApply_Errors(t *testing.T) {
	m := tlstest.New(t)
	other := tlstest.New(t)
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.pem")
	noPEM := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(noPEM, []byte("this is not a certificate"), 0o644); err != nil {
		t.Fatal(err)
	}
	leak := filepath.Join(dir, "leak.key")
	if err := os.WriteFile(leak, []byte("-----BEGIN PRIVATE KEY-----\nLEAKMARKER\n-----END PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	enc := filepath.Join(dir, "enc.key")
	if err := os.WriteFile(enc, pem.EncodeToMemory(&pem.Block{
		Type:  "ENCRYPTED PRIVATE KEY",
		Bytes: []byte("not a real key"),
	}), 0o600); err != nil {
		t.Fatal(err)
	}
	trad := filepath.Join(dir, "trad.key")
	if err := os.WriteFile(trad, pem.EncodeToMemory(&pem.Block{
		Type:    "RSA PRIVATE KEY",
		Headers: map[string]string{"Proc-Type": "4,ENCRYPTED"},
		Bytes:   []byte("not a real key"),
	}), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		files    Files
		insecure bool
		tlsOn    bool
		want     string
		hide     string
	}{
		{"missing ca", Files{CAFile: missing}, false, true, "ca_file: cannot read " + missing, "LEAKMARKER"},
		{"ca not pem", Files{CAFile: noPEM}, false, true, "ca_file: " + noPEM + " has no PEM certificate", ""},
		{"key not pem", Files{CertFile: m.ClientCertPath, KeyFile: noPEM}, false, true, "key_file: " + noPEM + " has no PEM private key", ""},
		{"leak body", Files{CertFile: m.ClientCertPath, KeyFile: leak}, false, true, "key_file: " + leak + " has no PEM private key", "LEAKMARKER"},
		{"encrypted", Files{CertFile: m.ClientCertPath, KeyFile: enc}, false, true, "key_file: " + enc + " is an encrypted key, which is not supported", ""},
		{"traditional encrypted", Files{CertFile: m.ClientCertPath, KeyFile: trad}, false, true, "is an encrypted key, which is not supported", ""},
		{"mismatch", Files{CertFile: m.ClientCertPath, KeyFile: other.ClientKeyPath}, false, true, "cert_file and key_file do not match", ""},
		{"cert only", Files{CertFile: m.ClientCertPath}, false, true, "cert_file needs key_file", ""},
		{"key only", Files{KeyFile: m.ClientKeyPath}, false, true, "key_file needs cert_file", ""},
		{"ca insecure", Files{CAFile: m.CACertPath}, true, true, "ca_file cannot be used with -insecure", ""},
		{"ca tls off", Files{CAFile: m.CACertPath}, false, false, "ca_file needs TLS, but TLS is off", ""},
		{"cert tls off", Files{CertFile: m.ClientCertPath}, false, false, "cert_file needs TLS, but TLS is off", ""},
		{"key tls off", Files{KeyFile: m.ClientKeyPath}, false, false, "key_file needs TLS, but TLS is off", ""},
		{"name tls off", Files{ServerName: "vegaload.test"}, false, false, "tls_server_name needs TLS, but TLS is off", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &tls.Config{}
			err := c.files.Apply(cfg, c.insecure, c.tlsOn)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %v, want %q", err, c.want)
			}
			if c.hide != "" && strings.Contains(err.Error(), c.hide) {
				t.Fatalf("error contains %s: %v", c.hide, err)
			}
			if cfg.RootCAs != nil || len(cfg.Certificates) != 0 || cfg.ServerName != "" {
				t.Fatalf("failed Apply changed the config: %+v", cfg)
			}
		})
	}
}

func TestApply_CertWithSkipVerify(t *testing.T) {
	m := tlstest.New(t)
	cfg := &tls.Config{}
	err := (Files{CertFile: m.ClientCertPath, KeyFile: m.ClientKeyPath}).Apply(cfg, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("certificates = %d", len(cfg.Certificates))
	}
}

func TestApply_TwoCAsAndServerName(t *testing.T) {
	a := tlstest.New(t)
	b := tlstest.New(t)
	both := filepath.Join(t.TempDir(), "both.pem")
	if err := os.WriteFile(both, append(append([]byte{}, a.CACertPEM...), b.CACertPEM...), 0o644); err != nil {
		t.Fatal(err)
	}
	la := listen(t, a.ServerConfig(false))
	lb := listen(t, b.ServerConfig(false))

	dial := func(addr string) error {
		cfg := &tls.Config{MinVersion: tls.VersionTLS12}
		if err := (Files{CAFile: both, ServerName: tlstest.Name}).Apply(cfg, false, true); err != nil {
			return err
		}
		c, err := tls.Dial("tcp", addr, cfg)
		if err != nil {
			return err
		}
		return c.Close()
	}
	if err := dial(la.Addr().String()); err != nil {
		t.Fatal(err)
	}
	if err := dial(lb.Addr().String()); err != nil {
		t.Fatal(err)
	}

	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if err := (Files{CAFile: a.CACertPath}).Apply(cfg, false, true); err != nil {
		t.Fatal(err)
	}
	c, err := tls.Dial("tcp", la.Addr().String(), cfg)
	if err == nil {
		c.Close()
		t.Fatal("connecting by IP without tls_server_name should fail the name check")
	}
	var host x509.HostnameError
	if !errors.As(err, &host) && !strings.Contains(err.Error(), "certificate") {
		t.Fatal(err)
	}

	cfg = &tls.Config{MinVersion: tls.VersionTLS12}
	if err := (Files{CAFile: b.CACertPath, ServerName: tlstest.Name}).Apply(cfg, false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := tls.Dial("tcp", la.Addr().String(), cfg); err == nil {
		t.Fatal("the other CA should be refused")
	}
}

func TestFromOptions(t *testing.T) {
	f := FromOptions(map[string]string{
		"ca_file": "a", "cert_file": "b", "key_file": "c", "tls_server_name": "n", "other": "x",
	})
	if f != (Files{CAFile: "a", CertFile: "b", KeyFile: "c", ServerName: "n"}) {
		t.Fatalf("%+v", f)
	}
	if !f.Set() || FromOptions(nil).Set() {
		t.Fatal("Set")
	}
}

func listen(t *testing.T, cfg *tls.Config) net.Listener {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(io.Discard, c); _ = c.Close() }()
		}
	}()
	return ln
}
