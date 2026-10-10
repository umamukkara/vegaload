package js

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
	must(t, nil, assertFn+`
		const url = "`+s.URL()+`";
		const listed = ftp.list(url, {path: "/pub"});
		assert(listed.ok, listed.error);
		assert(listed.total === 1 && listed.entries[0].name === "data.bin", JSON.stringify(listed.entries));
		assert(listed.entries[0].time === "2020-01-02T15:04:05Z", listed.entries[0].time);
		assert(listed.value === undefined && listed.records === undefined, "no other protocol fields");
		const got = ftp.download(url, {path: "/pub/data.bin", expect_size: "9"});
		assert(got.ok && got.size === 9, got.error || String(got.size));
		const denied = ftp.upload(url, {path: "/up-{id}", body: "x"});
		assert(denied.ok === false && denied.error.indexOf("pass allow_writes: true") >= 0, denied.error);
		const rt = ftp.roundtrip(url, {path: "/rt-{id}", size: "8", allow_writes: true});
		assert(rt.ok && rt.size === 8, rt.error || String(rt.size));
	`)
	if n := s.ConnCount(); n < 1 {
		t.Fatalf("connections %d", n)
	}
}

func TestFTP_OnePoolForManyCalls(t *testing.T) {
	s := ftptest.Start(t)
	s.Apply(ftptest.Config{Files: map[string][]byte{"/pub/data.bin": []byte("hello ftp")}})
	must(t, nil, assertFn+`
		const url = "`+s.URL()+`";
		for (let i = 0; i < 4; i++) {
			const r = ftp.download(url, {path: "/pub/data.bin"});
			assert(r.ok, r.error);
		}
	`)
	if n := s.ConnCount(); n != 1 {
		t.Fatalf("connections %d, want 1", n)
	}
}

func TestFTP_RefusesModeAndPasswordEnv(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"unknown option", `ftp.download("ftp://127.0.0.1:9", {path: "/", bogus: 1});`, "unknown"},
		{"mode", `ftp.download("ftp://127.0.0.1:9", {path: "/", mode: "list"});`, "function you called sets it"},
		{"password_env", `ftp.download("ftp://127.0.0.1:9", {path: "/", password_env: "X"});`, "pass password: env.NAME"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runScript(t, nil, tc.src)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatal(err)
			}
		})
	}
}

func TestFTP_HostNotAllowed(t *testing.T) {
	check := func(host string) error { return errors.New("host not allowed") }
	if err := runScript(t, netapi.SafetyCheck(check), `ftp.download("ftp://files.example.com", {path: "/"});`); err == nil || !strings.Contains(err.Error(), "host not allowed") {
		t.Fatal(err)
	}
}
