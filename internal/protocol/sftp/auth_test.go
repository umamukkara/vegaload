package sftp

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/protocol/sftp/sftptest"
	"golang.org/x/crypto/ssh"
)

func TestAuth_KeysAndPassword(t *testing.T) {
	s := srv(t)
	keys := []struct {
		name string
		key  crypto.Signer
	}{
		{"ed25519", ed25519Key(t)},
		{"rsa", rsaKey(t)},
		{"ecdsa", ecdsaKey(t)},
	}
	for _, tc := range keys {
		t.Run(tc.name, func(t *testing.T) {
			path, pub := writeKey(t, tc.key, "")
			s.Apply(sftptest.Config{
				AuthorizedKeys: map[string][]ssh.PublicKey{"load": {pub}},
				NoPassword:     true,
			})
			res := connectWith(t, s, map[string]string{"key_file": path})
			if !res.Success {
				t.Fatal(res.Err)
			}
		})
	}

	t.Run("encrypted", func(t *testing.T) {
		k := ed25519Key(t)
		path, pub := writeKey(t, k, "correct-horse")
		s.Apply(sftptest.Config{
			AuthorizedKeys: map[string][]ssh.PublicKey{"load": {pub}},
			NoPassword:     true,
		})
		res := connectWith(t, s, map[string]string{"key_file": path})
		if res.Success || !strings.Contains(res.Err.Error(), "set key_passphrase_env") || strings.Contains(res.Err.Error(), "PRIVATE") {
			t.Fatal(res.Err)
		}
		t.Setenv("SFTP_PP", "wrong-phrase")
		res = connectWith(t, s, map[string]string{"key_file": path, "key_passphrase_env": "SFTP_PP"})
		if res.Success || !strings.Contains(res.Err.Error(), "wrong passphrase") || strings.Contains(res.Err.Error(), "wrong-phrase") {
			t.Fatal(res.Err)
		}
		t.Setenv("SFTP_PP", "correct-horse")
		res = connectWith(t, s, map[string]string{"key_file": path, "key_passphrase_env": "SFTP_PP"})
		if !res.Success {
			t.Fatal(res.Err)
		}
	})

	t.Run("key then password", func(t *testing.T) {
		path, _ := writeKey(t, ed25519Key(t), "")
		s.Apply(sftptest.Config{
			Users:          map[string]string{"load": "secret"},
			AuthorizedKeys: map[string][]ssh.PublicKey{},
			NoPassword:     false,
		})
		t.Setenv("SFTP_PW", "secret")
		before := len(s.AuthAttempts())
		res := connectWith(t, s, map[string]string{"key_file": path, "password_env": "SFTP_PW"})
		if !res.Success {
			t.Fatal(res.Err)
		}
		sawKey, sawPw := false, false
		for _, a := range s.AuthAttempts()[before:] {
			if a == "publickey" {
				sawKey = true
			}
			if a == "password" {
				sawPw = true
			}
		}
		if !sawKey || !sawPw {
			t.Fatalf("auth attempts = %v", s.AuthAttempts())
		}
	})

	t.Run("wrong password", func(t *testing.T) {
		s.Apply(sftptest.Config{Users: map[string]string{"load": "secret"}, NoPassword: false})
		t.Setenv("SFTP_PW", "hunter2-secret")
		res := connectWith(t, s, map[string]string{"password_env": "SFTP_PW"})
		if res.Success || !strings.Contains(res.Err.Error(), "refused the login") || !strings.Contains(res.Err.Error(), "offered: password") || strings.Contains(res.Err.Error(), "hunter2-secret") {
			t.Fatal(res.Err)
		}
	})

	t.Run("bad key file", func(t *testing.T) {
		res := connectWith(t, s, map[string]string{"key_file": filepath.Join(t.TempDir(), "missing")})
		if res.Success || !strings.Contains(res.Err.Error(), "cannot read") {
			t.Fatal(res.Err)
		}
		bad := filepath.Join(t.TempDir(), "not-a-key")
		if err := os.WriteFile(bad, []byte("hello"), 0o600); err != nil {
			t.Fatal(err)
		}
		res = connectWith(t, s, map[string]string{"key_file": bad})
		if res.Success || !strings.Contains(res.Err.Error(), "not a private key") || strings.Contains(res.Err.Error(), "hello") {
			t.Fatal(res.Err)
		}
	})

	t.Run("no subsystem and no shell", func(t *testing.T) {
		s.Apply(sftptest.Config{NoSubsystem: true, Users: map[string]string{"load": "secret"}})
		t.Setenv("SFTP_PW", "secret")
		res := connectWith(t, s, map[string]string{"password_env": "SFTP_PW"})
		if res.Success || !strings.Contains(res.Err.Error(), "no sftp subsystem") {
			t.Fatal(res.Err)
		}
		s.Apply(sftptest.Config{NoSubsystem: false, Users: map[string]string{"load": "secret"}})
		res = connectWith(t, s, map[string]string{"password_env": "SFTP_PW"})
		if !res.Success {
			t.Fatal(res.Err)
		}
		for _, req := range s.Requests() {
			if req == "exec" || req == "shell" || req == "pty-req" {
				t.Fatalf("client sent %s", req)
			}
		}
	})
}

func connectWith(t *testing.T, s *sftptest.Server, opt map[string]string) protocol.Result {
	t.Helper()
	opt = copyOpt(opt)
	opt["mode"] = "connect"
	d, err := New(protocol.Target{URL: s.URL(), Options: opt, InsecureSkipVerify: true}, 5*time.Second)
	if err != nil {
		return protocol.Result{Err: err}
	}
	defer d.Close()
	res, _ := d.Run(context.Background())
	return res
}

func writeKey(t *testing.T, key crypto.Signer, pass string) (string, ssh.PublicKey) {
	t.Helper()
	var block *pem.Block
	var err error
	if pass == "" {
		block, err = ssh.MarshalPrivateKey(key, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(key, "", []byte(pass))
	}
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	pub, err := ssh.NewPublicKey(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	return path, pub
}

func ed25519Key(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func rsaKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func ecdsaKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
