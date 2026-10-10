package python

import (
	"errors"
	"strings"
	"testing"

	"github.com/vegaload/vegaload/internal/protocol/ftp/ftptest"
	"github.com/vegaload/vegaload/internal/scripting/netapi"
)

func TestFTP_ListDownloadAndRoundtrip(t *testing.T) {
	s := ftptest.Start(t)
	s.Apply(ftptest.Config{Files: map[string][]byte{"/pub/data.bin": []byte("hello ftp")}})
	must(t, nil, `
url = "`+s.URL()+`"
listed = ftp.list(url, path="/pub")
assert listed.ok, listed.error
assert listed.total == 1 and listed.entries[0].name == "data.bin", listed.entries
assert listed.entries[0].time == "2020-01-02T15:04:05Z", listed.entries[0].time
assert "value" not in listed and "records" not in listed
got = ftp.download(url, path="/pub/data.bin", expect_size="9")
assert got.ok and got.size == 9, got.error or got.size
denied = ftp.upload(url, path="/up-{id}", body="x")
assert denied.ok is False and "pass allow_writes: true" in denied.error, denied.error
rt = ftp.roundtrip(url, path="/rt-{id}", size="8", allow_writes=True)
assert rt.ok and rt.size == 8, rt.error or rt.size
`)
}

func TestFTP_OnePoolForManyCalls(t *testing.T) {
	s := ftptest.Start(t)
	s.Apply(ftptest.Config{Files: map[string][]byte{"/pub/data.bin": []byte("hello ftp")}})
	must(t, nil, `
url = "`+s.URL()+`"
for i in range(4):
    r = ftp.download(url, path="/pub/data.bin")
    assert r.ok, r.error
`)
	if n := s.ConnCount(); n != 1 {
		t.Fatalf("connections %d, want 1", n)
	}
}

func TestFTP_RefusesModeAndPasswordEnv(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"unknown option": {`ftp.download("ftp://127.0.0.1:9", path="/", bogus=1)`, "unknown"},
		"mode":           {`ftp.download("ftp://127.0.0.1:9", path="/", mode="list")`, "function you called sets it"},
		"password_env":   {`ftp.download("ftp://127.0.0.1:9", path="/", password_env="X")`, "pass password: env.NAME"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := runIteration(t, nil, tc.src)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatal(err)
			}
		})
	}
}

func TestFTP_HostNotAllowed(t *testing.T) {
	check := func(host string) error { return errors.New("host not allowed") }
	if err := runIteration(t, netapi.SafetyCheck(check), `ftp.download("ftp://files.example.com", path="/")`); err == nil || !strings.Contains(err.Error(), "host not allowed") {
		t.Fatal(err)
	}
}
