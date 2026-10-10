package netapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol/ftp/ftptest"
)

func TestFTP_PoolAndReplyShape(t *testing.T) {
	s := ftptest.Start(t)
	s.Apply(ftptest.Config{Files: map[string][]byte{"/pub/data.bin": []byte("hello ftp")}})
	c := NewProtoClient(nil, 5*time.Second)
	defer c.Close()
	for i := 0; i < 4; i++ {
		r, err := c.FTP(context.Background(), "download", ProtoCall{
			URL: s.URL(), Options: map[string]string{"path": "/pub/data.bin", "expect_size": "9"},
		})
		if err != nil || !r.OK || r.FTPSize != 9 {
			t.Fatalf("call %d: %v ok=%v size=%d err=%s", i, err, r.OK, r.FTPSize, r.Error)
		}
	}
	if n := s.ConnCount(); n != 1 {
		t.Errorf("%d connections for 4 calls, want 1", n)
	}
	if len(c.ftpConns) != 1 {
		t.Errorf("%d clients kept, want 1", len(c.ftpConns))
	}
	r, err := c.Call(context.Background(), "ftp.list", ProtoCall{
		URL: s.URL(), Options: map[string]string{"path": "/pub"},
	})
	if err != nil {
		t.Fatal(err)
	}
	f := r.Fields()
	ents := f["entries"].([]any)
	e := ents[0].(map[string]any)
	if f["ok"] != true || e["name"] != "data.bin" || e["time"] != "2020-01-02T15:04:05Z" || f["total"] != 1 {
		t.Fatalf("fields = %#v", f)
	}
	if _, ok := f["value"]; ok {
		t.Fatal("an ftp reply must not grow redis fields")
	}
	if _, ok := f["records"]; ok {
		t.Fatal("an ftp reply must not grow kafka fields")
	}
	denied, err := c.FTP(context.Background(), "upload", ProtoCall{
		URL: s.URL(), Body: []byte("x"), Options: map[string]string{"path": "/up-{id}"},
	})
	if err != nil || denied.OK || !strings.Contains(denied.Error, "pass allow_writes: true") {
		t.Fatalf("%v %+v", err, denied)
	}
	rt, err := c.FTP(context.Background(), "roundtrip", ProtoCall{
		URL: s.URL(), Options: map[string]string{"path": "/rt-{id}", "size": "8", "allow_writes": "true"},
	})
	if err != nil || !rt.OK || rt.FTPSize != 8 {
		t.Fatalf("%v %+v", err, rt)
	}
	c.Close()
	if len(c.ftpConns) != 0 {
		t.Error("Close must release the clients")
	}
}

func TestFTP_RefusesModeAndPasswordEnv(t *testing.T) {
	c := NewProtoClient(nil, time.Second)
	_, err := c.FTP(context.Background(), "download", ProtoCall{
		URL: "ftp://127.0.0.1:1", Options: map[string]string{"mode": "list", "path": "/"},
	})
	if err == nil || !strings.Contains(err.Error(), "function you called sets it") {
		t.Fatal(err)
	}
	_, err = c.FTP(context.Background(), "download", ProtoCall{
		URL: "ftp://127.0.0.1:1", Options: map[string]string{"password_env": "X", "path": "/"},
	})
	if err == nil || !strings.Contains(err.Error(), "pass password: env.NAME") {
		t.Fatal(err)
	}
}

func TestProtoFunctions_ListsFTP(t *testing.T) {
	f := ProtoFunctions()["ftp"]
	want := []string{"connect", "download", "upload", "list", "stat", "delete", "roundtrip"}
	if len(f) != len(want) {
		t.Fatal(f)
	}
	for i := range want {
		if f[i] != want[i] {
			t.Fatal(f)
		}
	}
}
