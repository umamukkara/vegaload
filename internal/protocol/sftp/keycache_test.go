package sftp

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
	"golang.org/x/crypto/ssh"
)

func TestKeyCache_OneParseForManyBuilds(t *testing.T) {
	path, _ := writeKey(t, ed25519Key(t), "phrase-secret")
	before := signerLoads.Load()
	target := protocol.Target{
		URL: "sftp://load@127.0.0.1:1",
		Options: map[string]string{
			"mode": "connect", "key_file": path, "key_passphrase_env": "SFTP_PP",
		},
	}
	t.Setenv("SFTP_PP", "phrase-secret")
	var wg sync.WaitGroup
	errc := make(chan error, 200)
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := New(target, time.Second); err != nil {
				errc <- err
			}
		}()
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Fatal(err)
	}
	if n := signerLoads.Load() - before; n != 1 {
		t.Fatalf("parsed %d times, want 1", n)
	}
	signerMu.Lock()
	for id := range signerCache {
		if id.path == path && (id.passSum == "phrase-secret" || strings.Contains(id.passSum, "phrase")) {
			t.Fatal("the cache holds the passphrase text")
		}
	}
	signerMu.Unlock()

	overwriteKey(t, path, "phrase-secret")
	if _, err := New(target, time.Second); err != nil {
		t.Fatal(err)
	}
	if n := signerLoads.Load() - before; n != 2 {
		t.Fatalf("parsed %d times after the file changed, want 2", n)
	}

	t.Setenv("SFTP_PP", "wrong-phrase")
	_, err := New(target, time.Second)
	if err == nil || !strings.Contains(err.Error(), "wrong passphrase") || strings.Contains(err.Error(), "wrong-phrase") {
		t.Fatal(err)
	}
	_, err = New(target, time.Second)
	if err == nil || !strings.Contains(err.Error(), "wrong passphrase") {
		t.Fatal(err)
	}
	if n := signerLoads.Load() - before; n != 4 {
		t.Fatalf("a failed passphrase was cached: parsed %d times, want 4", n)
	}
	t.Setenv("SFTP_PP", "phrase-secret")
	if _, err := New(target, time.Second); err != nil {
		t.Fatal(err)
	}
	if n := signerLoads.Load() - before; n != 4 {
		t.Fatalf("the right passphrase parsed again: %d times, want 4", n)
	}
}

func overwriteKey(t *testing.T, path, pass string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(pass))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	next := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, next, next); err != nil {
		t.Fatal(err)
	}
}
