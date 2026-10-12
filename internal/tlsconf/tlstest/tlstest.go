// Package tlstest builds a short-lived CA, server certificate and client
// certificate for tests. The server name on the certificate is vegaload.test
// and there is no IP address, so a client that connects to 127.0.0.1 has to
// set tls_server_name.
package tlstest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ErrCase is one TLS option error a driver should report at construction.
type ErrCase struct {
	Name string
	URL  string
	Opts map[string]string
	Skip bool
	Want string
	Hide string
}

// Name is the only DNS name on the server certificate.
const Name = "vegaload.test"

// Material is one CA and the certificates it signed.
type Material struct {
	Dir            string
	CACertPEM      []byte
	CACertPath     string
	ServerCertPEM  []byte
	ServerKeyPEM   []byte
	ServerCertPath string
	ServerKeyPath  string
	ClientCertPEM  []byte
	ClientKeyPEM   []byte
	ClientCertPath string
	ClientKeyPath  string
	Server         tls.Certificate
	Client         tls.Certificate
}

// New writes a CA, a server certificate for Name, and a client certificate.
// Each New call is a different CA.
func New(tb testing.TB) *Material {
	tb.Helper()
	caKey, caCert, caPEM := newCA(tb)
	srvPEM, srvKey, srv := newLeaf(tb, caCert, caKey, Name, x509.ExtKeyUsageServerAuth)
	cliPEM, cliKey, cli := newLeaf(tb, caCert, caKey, "vegaload-client", x509.ExtKeyUsageClientAuth)
	dir := tb.TempDir()
	m := &Material{
		Dir:            dir,
		CACertPEM:      caPEM,
		CACertPath:     filepath.Join(dir, "ca.pem"),
		ServerCertPEM:  srvPEM,
		ServerKeyPEM:   srvKey,
		ServerCertPath: filepath.Join(dir, "server.pem"),
		ServerKeyPath:  filepath.Join(dir, "server.key"),
		ClientCertPEM:  cliPEM,
		ClientKeyPEM:   cliKey,
		ClientCertPath: filepath.Join(dir, "client.pem"),
		ClientKeyPath:  filepath.Join(dir, "client.key"),
		Server:         srv,
		Client:         cli,
	}
	writeFile(tb, m.CACertPath, caPEM, 0o644)
	writeFile(tb, m.ServerCertPath, srvPEM, 0o644)
	writeFile(tb, m.ServerKeyPath, srvKey, 0o600)
	writeFile(tb, m.ClientCertPath, cliPEM, 0o644)
	writeFile(tb, m.ClientKeyPath, cliKey, 0o600)
	return m
}

// ErrorCases builds the shared construction errors. tlsURL is a TLS target
// and plainURL is the same service with TLS off.
func ErrorCases(tb testing.TB, m *Material, tlsURL, plainURL string) []ErrCase {
	tb.Helper()
	missing := filepath.Join(m.Dir, "missing.pem")
	leak := filepath.Join(m.Dir, "leak.key")
	if err := os.WriteFile(leak, []byte("-----BEGIN PRIVATE KEY-----\nLEAKMARKER\n-----END PRIVATE KEY-----\n"), 0o600); err != nil {
		tb.Fatal(err)
	}
	enc := filepath.Join(m.Dir, "enc.key")
	if err := os.WriteFile(enc, pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte("no")}), 0o600); err != nil {
		tb.Fatal(err)
	}
	other := New(tb)
	return []ErrCase{
		{"missing", tlsURL, map[string]string{"ca_file": missing}, false, "ca_file: cannot read " + missing, ""},
		{"no pem", tlsURL, map[string]string{"ca_file": leak}, false, "has no PEM certificate", "LEAKMARKER"},
		{"leak key", tlsURL, map[string]string{"cert_file": m.ClientCertPath, "key_file": leak}, false, "has no PEM private key", "LEAKMARKER"},
		{"need key", tlsURL, map[string]string{"cert_file": m.ClientCertPath}, false, "cert_file needs key_file", ""},
		{"need cert", tlsURL, map[string]string{"key_file": m.ClientKeyPath}, false, "key_file needs cert_file", ""},
		{"mismatch", tlsURL, map[string]string{"cert_file": m.ClientCertPath, "key_file": other.ClientKeyPath}, false, "do not match", ""},
		{"insecure", tlsURL, map[string]string{"ca_file": m.CACertPath}, true, "ca_file cannot be used with -insecure", ""},
		{"tls off", plainURL, map[string]string{"ca_file": m.CACertPath}, false, "ca_file needs TLS, but TLS is off", ""},
		{"name off", plainURL, map[string]string{"tls_server_name": Name}, false, "tls_server_name needs TLS, but TLS is off", ""},
		{"cert off", plainURL, map[string]string{"cert_file": m.ClientCertPath}, false, "cert_file needs TLS, but TLS is off", ""},
		{"key off", plainURL, map[string]string{"key_file": m.ClientKeyPath}, false, "key_file needs TLS, but TLS is off", ""},
		{"encrypted", tlsURL, map[string]string{"cert_file": m.ClientCertPath, "key_file": enc}, false, "is an encrypted key, which is not supported", ""},
	}
}

// KeyLine returns one line of a PEM key, so a test can check it never
// appears in an error.
func KeyLine(tb testing.TB, path string) string {
	tb.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if len(line) > 20 && !strings.Contains(line, "-----") {
			return line
		}
	}
	tb.Fatal("no key line")
	return ""
}

// Listen serves TLS on 127.0.0.1 and keeps accepting after a handshake
// failure. A plain tls.Listener stops its accept loop on that error.
func Listen(tb testing.TB, cfg *tls.Config) net.Listener {
	tb.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		tb.Fatalf("tlstest: %v", err)
	}
	tb.Cleanup(func() { _ = ln.Close() })
	return retryListener{Listener: ln}
}

// retryListener hides a failed handshake from the server's accept loop.
type retryListener struct{ net.Listener }

func (r retryListener) Accept() (net.Conn, error) {
	for {
		c, err := r.Listener.Accept()
		if err == nil || errors.Is(err, net.ErrClosed) {
			return c, err
		}
	}
}

// ServerConfig is the config for a fake server. requireClient asks for a
// client certificate and checks it against this CA.
func (m *Material) ServerConfig(requireClient bool) *tls.Config {
	cfg := &tls.Config{
		Certificates: []tls.Certificate{m.Server},
		MinVersion:   tls.VersionTLS12,
	}
	if requireClient {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(m.CACertPEM)
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg
}

func newCA(tb testing.TB) (*ecdsa.PrivateKey, *x509.Certificate, []byte) {
	tb.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		tb.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(tb),
		Subject:               pkix.Name{CommonName: "vegaload-test-ca"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		tb.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		tb.Fatal(err)
	}
	return key, cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func newLeaf(tb testing.TB, ca *x509.Certificate, caKey *ecdsa.PrivateKey, name string, use x509.ExtKeyUsage) ([]byte, []byte, tls.Certificate) {
	tb.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		tb.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(tb),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{name},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{use},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		tb.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		tb.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		tb.Fatal(err)
	}
	return certPEM, keyPEM, pair
}

func serial(tb testing.TB) *big.Int {
	tb.Helper()
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		tb.Fatal(err)
	}
	return n
}

func writeFile(tb testing.TB, path string, b []byte, mode os.FileMode) {
	tb.Helper()
	if err := os.WriteFile(path, b, mode); err != nil {
		tb.Fatal(err)
	}
}
