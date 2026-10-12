package js

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/inputs"
	"github.com/vegaload/vegaload/internal/protocol/sftp/sftptest"
	"github.com/vegaload/vegaload/internal/scripting/netapi"
)

func TestSFTP_ListDownloadAndRoundtrip(t *testing.T) {
	s := sftptest.Start(t)
	s.Apply(sftptest.Config{Files: map[string][]byte{"/pub/data.bin": []byte("hello sftp")}})
	must(t, nil, assertFn+`
		const url = "`+s.URL()+`";
		const login = {password: "secret", insecure: true};
		const listed = sftp.list(url, {...login, path: "/pub"});
		assert(listed.ok, listed.error);
		assert(listed.total === 1 && listed.entries[0].name === "data.bin", JSON.stringify(listed.entries));
		assert(listed.entries[0].type === "file", listed.entries[0].type);
		assert(listed.value === undefined && listed.records === undefined, "no other protocol fields");
		const got = sftp.download(url, {...login, path: "/pub/data.bin", expect_size: "10"});
		assert(got.ok && got.size === 10, got.error || String(got.size));
		const missing = sftp.download(url, {...login, path: "/missing"});
		assert(missing.ok === false && missing.error.indexOf("no such file") >= 0, missing.error);
		const denied = sftp.upload(url, {...login, path: "/up-{id}", body: "x"});
		assert(denied.ok === false && denied.error.indexOf("pass allow_writes: true") >= 0, denied.error);
		const rt = sftp.roundtrip(url, {...login, path: "/rt-{id}", size: "8", allow_writes: true});
		assert(rt.ok && rt.size === 8, rt.error || String(rt.size));
	`)
}

func TestSFTP_OneSetOfConnections(t *testing.T) {
	s := sftptest.Start(t)
	s.Apply(sftptest.Config{Files: map[string][]byte{"/pub/data.bin": []byte("hello sftp")}})
	must(t, nil, assertFn+`
		const url = "`+s.URL()+`";
		for (let i = 0; i < 4; i++) {
			const r = sftp.download(url, {password: "secret", insecure: true, path: "/pub/data.bin"});
			assert(r.ok, r.error);
		}
	`)
	if n := s.ConnCount(); n != 1 {
		t.Fatalf("connections %d, want 1", n)
	}
}

func TestSFTP_PasswordFromEnv(t *testing.T) {
	s := sftptest.Start(t)
	path := writeScript(t, "s.js", "export default function () {\n"+assertFn+`
		const r = sftp.download("`+s.URL()+`", {password: env.SFTP_PASSWORD, insecure: true, path: "/pub/data.bin"});
		assert(r.ok && r.size === 10, r.error || String(r.size));
	}
	`)
	s.Apply(sftptest.Config{Files: map[string][]byte{"/pub/data.bin": []byte("hello sftp")}})
	script, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	in := inputs.New(map[string]string{"SFTP_PASSWORD": "secret"}, nil)
	vu, err := script.NewVU(nil, 3*time.Second, WithInputs(in))
	if err != nil {
		t.Fatal(err)
	}
	defer vu.Close()
	if err := vu.Iteration(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSFTP_RefusesModeAndPasswordEnv(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"unknown option", `sftp.download("sftp://load@127.0.0.1:9", {path: "/", password: "x", bogus: 1});`, "unknown"},
		{"mode", `sftp.download("sftp://load@127.0.0.1:9", {path: "/", password: "x", mode: "list"});`, "function you called sets it"},
		{"password_env", `sftp.download("sftp://load@127.0.0.1:9", {path: "/", password_env: "X"});`, "pass password: env.NAME"},
		{"key_passphrase_env", `sftp.download("sftp://load@127.0.0.1:9", {path: "/", key_passphrase_env: "X"});`, "pass key_passphrase: env.NAME"},
		{"tls option", `sftp.download("sftp://load@127.0.0.1:9", {path: "/", password: "x", ca_file: "ca.pem"});`, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runScript(t, nil, tc.src)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatal(err)
			}
		})
	}
}

func TestSFTP_HostNotAllowed(t *testing.T) {
	check := func(host string) error { return errors.New("host not allowed") }
	if err := runScript(t, netapi.SafetyCheck(check), `sftp.download("sftp://files.example.com", {path: "/"});`); err == nil || !strings.Contains(err.Error(), "host not allowed") {
		t.Fatal(err)
	}
}
