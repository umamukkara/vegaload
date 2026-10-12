// Package tlsconf turns the shared TLS file options into a tls.Config.
//
// The four keys are ca_file, cert_file, key_file and tls_server_name. Every
// TLS protocol uses them the same way. ca_file replaces the system roots.
// cert_file and key_file are sent together when the server asks for a client
// certificate. The files are read when Apply runs, and the file bytes are
// not kept after the config is built.
package tlsconf

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Keys are the option names, in the order a "TLS is off" error reports them.
var Keys = []string{"ca_file", "cert_file", "key_file", "tls_server_name"}

// Files is the four options. Empty strings mean the option was not set.
type Files struct {
	CAFile     string
	CertFile   string
	KeyFile    string
	ServerName string
}

// FromOptions reads the four keys. A missing key and an empty value are the same.
func FromOptions(opts map[string]string) Files {
	return Files{
		CAFile:     opts["ca_file"],
		CertFile:   opts["cert_file"],
		KeyFile:    opts["key_file"],
		ServerName: opts["tls_server_name"],
	}
}

// Set reports whether any of the four options has a value.
func (f Files) Set() bool {
	return f.CAFile != "" || f.CertFile != "" || f.KeyFile != "" || f.ServerName != ""
}

// Apply checks the options and fills cfg. tlsOn is false when the connection
// is plain text. insecure is true when the certificate will not be checked
// (-insecure, skip-verify, or a mode that skips verification). cfg must be a
// config no other goroutine is using. A nil cfg is only valid when nothing
// is set, or when TLS is off and Apply is reporting that.
//
// On success the PEM bytes are discarded. cfg.RootCAs, when set, is only the
// certificates from ca_file.
func (f Files) Apply(cfg *tls.Config, insecure, tlsOn bool) error {
	if !tlsOn {
		switch {
		case f.CAFile != "":
			return errors.New("ca_file needs TLS, but TLS is off")
		case f.CertFile != "":
			return errors.New("cert_file needs TLS, but TLS is off")
		case f.KeyFile != "":
			return errors.New("key_file needs TLS, but TLS is off")
		case f.ServerName != "":
			return errors.New("tls_server_name needs TLS, but TLS is off")
		default:
			return nil
		}
	}
	if f.CAFile != "" && insecure {
		return errors.New("ca_file cannot be used with -insecure")
	}
	if f.CertFile != "" && f.KeyFile == "" {
		return errors.New("cert_file needs key_file")
	}
	if f.KeyFile != "" && f.CertFile == "" {
		return errors.New("key_file needs cert_file")
	}

	var pool *x509.CertPool
	if f.CAFile != "" {
		p, err := loadCAs(f.CAFile)
		if err != nil {
			return err
		}
		pool = p
	}
	var cert tls.Certificate
	haveCert := false
	if f.CertFile != "" {
		c, err := loadPair(f.CertFile, f.KeyFile)
		if err != nil {
			return err
		}
		cert = c
		haveCert = true
	}
	if cfg == nil {
		return nil
	}
	if pool != nil {
		cfg.RootCAs = pool
	}
	if haveCert {
		cfg.Certificates = []tls.Certificate{cert}
	}
	if f.ServerName != "" {
		cfg.ServerName = f.ServerName
	}
	return nil
}

// LiveFiles copies VEGALOAD_TEST_<NAME>_CA_FILE, _CERT_FILE, _KEY_FILE and
// _TLS_SERVER_NAME into opts when they are set. prefix is the part before
// those suffixes, such as VEGALOAD_TEST_REDIS.
func LiveFiles(opts map[string]string, prefix string) {
	if opts == nil {
		return
	}
	for _, pair := range []struct{ env, key string }{
		{prefix + "_CA_FILE", "ca_file"},
		{prefix + "_CERT_FILE", "cert_file"},
		{prefix + "_KEY_FILE", "key_file"},
		{prefix + "_TLS_SERVER_NAME", "tls_server_name"},
	} {
		if v := os.Getenv(pair.env); v != "" {
			opts[pair.key] = v
		}
	}
}

func loadCAs(path string) (*x509.CertPool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ca_file: cannot read %s: %w", path, err)
	}
	defer wipe(b)
	pool := x509.NewCertPool()
	rest := b
	found := false
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("ca_file: %s has no PEM certificate", path)
		}
		pool.AddCert(cert)
		found = true
	}
	if !found {
		return nil, fmt.Errorf("ca_file: %s has no PEM certificate", path)
	}
	return pool, nil
}

func loadPair(certPath, keyPath string) (tls.Certificate, error) {
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("key_file: cannot read %s: %w", keyPath, err)
	}
	defer wipe(keyPEM)
	block, _ := pem.Decode(keyPEM)
	if block == nil || !strings.Contains(block.Type, "PRIVATE KEY") {
		return tls.Certificate{}, fmt.Errorf("key_file: %s has no PEM private key", keyPath)
	}
	if block.Type == "ENCRYPTED PRIVATE KEY" || strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED") {
		return tls.Certificate{}, fmt.Errorf("key_file: %s is an encrypted key, which is not supported", keyPath)
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("cert_file: cannot read %s: %w", certPath, err)
	}
	defer wipe(certPEM)
	cb, _ := pem.Decode(certPEM)
	if cb == nil || cb.Type != "CERTIFICATE" {
		return tls.Certificate{}, fmt.Errorf("cert_file: %s has no PEM certificate", certPath)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		// The library text can echo key bytes, so it is never returned.
		if strings.Contains(err.Error(), "does not match") {
			return tls.Certificate{}, errors.New("cert_file and key_file do not match")
		}
		return tls.Certificate{}, fmt.Errorf("key_file: %s has no PEM private key", keyPath)
	}
	// X509KeyPair keeps slices of the PEM buffer. Copy them so wiping the
	// file bytes does not erase the certificate.
	for i, der := range pair.Certificate {
		pair.Certificate[i] = bytes.Clone(der)
	}
	return pair, nil
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
