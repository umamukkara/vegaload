package ftp

import (
	"crypto/tls"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
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
