package ftp

import (
	"context"
	"crypto/tls"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/protocol/ftp/ftptest"
)

func TestImplicitTLSPort(t *testing.T) {
	cases := []struct {
		name string
		url  string
		opts map[string]string
		want string
	}{
		{"ftp", "ftp://127.0.0.1", nil, "21"},
		{"ftps", "ftps://127.0.0.1", nil, "990"},
		{"ftp with tls=implicit, no port", "ftp://127.0.0.1", map[string]string{"tls": "implicit"}, "990"},
		{"ftp with tls=implicit and a port", "ftp://127.0.0.1:2121", map[string]string{"tls": "implicit"}, "2121"},
		{"ftp with tls=explicit", "ftp://127.0.0.1", map[string]string{"tls": "explicit"}, "21"},
		{"ftps with a port", "ftps://127.0.0.1:2990", nil, "2990"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := New(protocol.Target{URL: c.url, Options: c.opts}, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if d.port != c.want {
				t.Fatalf("port %s, want %s", d.port, c.want)
			}
		})
	}
}

func TestTLSMax(t *testing.T) {
	cases := []struct {
		name string
		opts map[string]string
		want uint16
		bad  bool
	}{
		{"default", map[string]string{"tls": "explicit"}, tls.VersionTLS12, false},
		{"1.2", map[string]string{"tls": "explicit", "tls_max": "1.2"}, tls.VersionTLS12, false},
		{"1.3", map[string]string{"tls": "explicit", "tls_max": "1.3"}, tls.VersionTLS13, false},
		{"implicit default", map[string]string{"tls": "implicit"}, tls.VersionTLS12, false},
		{"bad value", map[string]string{"tls": "explicit", "tls_max": "1.1"}, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := New(protocol.Target{URL: "ftp://127.0.0.1", Options: c.opts}, time.Second)
			if c.bad {
				if err == nil {
					t.Fatal("want an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if d.tlsCfg == nil || d.tlsCfg.MaxVersion != c.want {
				t.Fatalf("max version %v, want %x", d.tlsCfg, c.want)
			}
		})
	}
}

// Each session has its own TLS session cache, so its data connections resume
// only its own control session (ProFTPD answers 425 to any other).
func TestSessionsDoNotShareTheTLSCache(t *testing.T) {
	srv := ftptest.StartTLS(t)
	tg := tgt(srv.URL(), modeConnect, map[string]string{"tls": "explicit", "sessions": "2"}, "")
	tg.InsecureSkipVerify = true
	d, err := New(tg, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var h1, h2 atomic.Pointer[session]
	s1, err := d.acquire(context.Background(), &h1)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := d.acquire(context.Background(), &h2)
	if err != nil {
		t.Fatal(err)
	}
	if s1.tlsCfg == nil || s2.tlsCfg == nil {
		t.Fatal("a TLS session has no TLS settings")
	}
	if s1.tlsCfg.ClientSessionCache == s2.tlsCfg.ClientSessionCache {
		t.Fatal("two sessions share one TLS session cache")
	}
	if s1.tlsCfg.ClientSessionCache == d.tlsCfg.ClientSessionCache {
		t.Fatal("a session uses the driver's cache")
	}
	if s1.tlsCfg.ServerName != d.tlsCfg.ServerName || s1.tlsCfg.MaxVersion != d.tlsCfg.MaxVersion {
		t.Fatal("the session copy lost a setting")
	}
}
