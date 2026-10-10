package ftptest

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jlaffaye/ftp"
)

func TestServer_LoginStoreListAndFetch(t *testing.T) {
	s := Start(t)
	c, err := ftp.Dial(s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Quit() })
	if err := c.Login("anonymous", "vegaload@"); err != nil {
		t.Fatal(err)
	}
	if err := c.Stor("/pub/a.bin", strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	entries, err := c.List("/pub")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "a.bin" || entries[0].Size != 5 {
		t.Fatalf("list %+v", entries)
	}
	r, err := c.Retr("/pub/a.bin")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil || string(body) != "hello" {
		t.Fatalf("retr %q %v", body, err)
	}
	n, err := c.FileSize("/pub/a.bin")
	if err != nil || n != 5 {
		t.Fatalf("size %d %v", n, err)
	}
	if err := c.Delete("/pub/a.bin"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.File("/pub/a.bin"); ok {
		t.Fatal("file still stored")
	}
}

func TestServer_PASVAnnouncesTheConfiguredHost(t *testing.T) {
	s := Start(t)
	s.Apply(Config{AnnounceHost: "10.255.255.1", NoMLST: true})
	c, err := ftp.Dial(s.Addr(), ftp.DialWithDisabledEPSV(true), ftp.DialWithTimeout(200*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Quit() })
	if err := c.Login("anonymous", "vegaload@"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.List("/"); err == nil {
		t.Fatal("PASV to 10.255.255.1 should fail")
	}
	joined := strings.Join(s.Commands(), "\n")
	if !strings.Contains(joined, "PASV") || strings.Contains(joined, "EPSV") {
		t.Fatalf("commands:\n%s", joined)
	}
}
