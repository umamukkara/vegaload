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
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
