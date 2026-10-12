package netapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol/sftp/sftptest"
	"golang.org/x/crypto/ssh"
)

func TestSFTP_ConnectionsAndReplyShape(t *testing.T) {
	s := sftptest.Start(t)
	s.Apply(sftptest.Config{Files: map[string][]byte{"/pub/data.bin": []byte("hello sftp")}})
	pw := "secret"
	c := NewProtoClient(nil, 5*time.Second)
	defer c.Close()
	call := ProtoCall{
		URL: s.URL(), Insecure: true, Password: &pw,
		Options: map[string]string{"path": "/pub/data.bin", "expect_size": "10"},
	}
	for i := 0; i < 4; i++ {
		r, err := c.SFTP(context.Background(), "download", call)
		if err != nil || !r.OK || r.XferSize != 10 {
			t.Fatalf("call %d: %v ok=%v size=%d err=%s", i, err, r.OK, r.XferSize, r.Error)
		}
	}
	if n := s.ConnCount(); n != 1 {
		t.Errorf("%d connections for 4 calls, want 1", n)
	}
	if len(c.sftpConns) != 1 {
		t.Errorf("%d clients kept, want 1", len(c.sftpConns))
	}
	for k := range c.sftpConns {
		if strings.Contains(k, pw) {
			t.Fatal("the connection key contains the password")
		}
	}
	r, err := c.Call(context.Background(), "sftp.list", ProtoCall{
		URL: s.URL(), Insecure: true, Password: &pw,
		Options: map[string]string{"path": "/pub"},
	})
	if err != nil {
		t.Fatal(err)
	}
	f := r.Fields()
	ents := f["entries"].([]any)
	e := ents[0].(map[string]any)
	if f["ok"] != true || e["name"] != "data.bin" || e["type"] != "file" || f["total"] != 1 {
		t.Fatalf("fields = %#v", f)
	}
	if _, ok := f["value"]; ok {
		t.Fatal("an sftp reply must not grow redis fields")
	}
	if _, ok := f["records"]; ok {
		t.Fatal("an sftp reply must not grow kafka fields")
	}
	ftpFields := ftpFieldKeys(t)
	if strings.Join(fieldKeys(f), ",") != strings.Join(ftpFields, ",") {
		t.Fatalf("sftp fields %v, ftp fields %v", fieldKeys(f), ftpFields)
	}
	missing, err := c.SFTP(context.Background(), "download", ProtoCall{
		URL: s.URL(), Insecure: true, Password: &pw,
		Options: map[string]string{"path": "/missing"},
	})
	if err != nil || missing.OK || !strings.Contains(missing.Error, "no such file") {
		t.Fatalf("a missing file must be a reply: %v %+v", err, missing)
	}
	denied, err := c.SFTP(context.Background(), "upload", ProtoCall{
		URL: s.URL(), Insecure: true, Password: &pw, Body: []byte("x"),
		Options: map[string]string{"path": "/up-{id}"},
	})
	if err != nil || denied.OK || !strings.Contains(denied.Error, "pass allow_writes: true") {
		t.Fatalf("%v %+v", err, denied)
	}
	rt, err := c.SFTP(context.Background(), "roundtrip", ProtoCall{
		URL: s.URL(), Insecure: true, Password: &pw,
		Options: map[string]string{"path": "/rt-{id}", "size": "8", "allow_writes": "true"},
	})
	if err != nil || !rt.OK || rt.XferSize != 8 {
		t.Fatalf("%v %+v", err, rt)
	}
	c.Close()
	if len(c.sftpConns) != 0 {
		t.Error("Close must release the clients")
	}
}

func TestSFTP_RefusesModeAndSecrets(t *testing.T) {
	c := NewProtoClient(nil, time.Second)
	_, err := c.SFTP(context.Background(), "download", ProtoCall{
		URL: "sftp://load@127.0.0.1:1", Options: map[string]string{"mode": "list", "path": "/"},
	})
	if err == nil || !strings.Contains(err.Error(), "function you called sets it") {
		t.Fatal(err)
	}
	_, err = c.SFTP(context.Background(), "download", ProtoCall{
		URL: "sftp://load@127.0.0.1:1", Options: map[string]string{"password_env": "X", "path": "/"},
	})
	if err == nil || !strings.Contains(err.Error(), "pass password: env.NAME") {
		t.Fatal(err)
	}
	_, err = c.SFTP(context.Background(), "download", ProtoCall{
		URL: "sftp://load@127.0.0.1:1", Options: map[string]string{"key_passphrase_env": "X", "path": "/"},
	})
	if err == nil || !strings.Contains(err.Error(), "pass key_passphrase: env.NAME") {
		t.Fatal(err)
	}
	_, err = ProtoCallFromArgs("ftp.download", "ftp://127.0.0.1", map[string]any{"key_passphrase": "x"})
	if err == nil || !strings.Contains(err.Error(), "unknown option key_passphrase") {
		t.Fatal(err)
	}
}

func TestSFTP_PassphraseIsHashedInTheKey(t *testing.T) {
	s := sftptest.Start(t)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("phrase-secret"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatal(err)
	}
	s.Apply(sftptest.Config{AuthorizedKeys: map[string][]ssh.PublicKey{"load": {pub}}, NoPassword: true})
	phrase := "phrase-secret"
	c := NewProtoClient(nil, 5*time.Second)
	defer c.Close()
	r, err := c.SFTP(context.Background(), "connect", ProtoCall{
		URL: s.URL(), Insecure: true, KeyPassphrase: &phrase,
		Options: map[string]string{"key_file": path},
	})
	if err != nil || !r.OK {
		t.Fatalf("%v %+v", err, r)
	}
	if len(c.sftpConns) != 1 {
		t.Fatalf("clients %d", len(c.sftpConns))
	}
	for k := range c.sftpConns {
		if strings.Contains(k, phrase) {
			t.Fatal("the connection key contains the passphrase")
		}
	}
}

func TestSFTP_HostNotAllowed(t *testing.T) {
	c := NewProtoClient(func(host string) error { return errString("host not allowed") }, time.Second)
	_, err := c.SFTP(context.Background(), "download", ProtoCall{URL: "sftp://files.example.com", Options: map[string]string{"path": "/"}})
	if err == nil || !strings.Contains(err.Error(), "host not allowed") {
		t.Fatal(err)
	}
}

func TestXferFields_ExactKeys(t *testing.T) {
	r := &ProtoReply{
		OK: true, IsXfer: true,
		XferEntries: []XferEntry{{Name: "a", Type: "file", Size: 3, Time: time.Date(2020, 1, 2, 15, 4, 5, 0, time.UTC)}},
		XferSize:    3, XferTotal: 1, XferText: "ok",
		XferTiming: XferTiming{ConnectMs: 1, LoginMs: 2, TransferMs: 3, UploadMs: 4, DownloadMs: 5, DeleteMs: 6},
	}
	f := r.Fields()
	got := fieldKeys(f)
	want := []string{"body", "bytesReceived", "bytesSent", "entries", "error", "messages", "ok", "size", "text", "timing", "total"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("keys = %v", got)
	}
	when := f["entries"].([]any)[0].(map[string]any)["time"]
	if when != "2020-01-02T15:04:05Z" {
		t.Fatal(when)
	}
	timing := f["timing"].(map[string]any)
	for _, k := range []string{"connectMs", "loginMs", "transferMs", "uploadMs", "downloadMs", "deleteMs"} {
		if _, ok := timing[k]; !ok {
			t.Fatal(k)
		}
	}
}

func ftpFieldKeys(t *testing.T) []string {
	t.Helper()
	r := &ProtoReply{IsXfer: true}
	return fieldKeys(r.Fields())
}

func fieldKeys(f map[string]any) []string {
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type errString string

func (e errString) Error() string { return string(e) }
