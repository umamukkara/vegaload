package python

import (
	"errors"
	"strings"
	"testing"

	"github.com/vegaload/vegaload/internal/inputs"
	"github.com/vegaload/vegaload/internal/protocol/sftp/sftptest"
	"github.com/vegaload/vegaload/internal/scripting/netapi"
)

func TestSFTP_ListDownloadAndRoundtrip(t *testing.T) {
	s := sftptest.Start(t)
	s.Apply(sftptest.Config{Files: map[string][]byte{"/pub/data.bin": []byte("hello sftp")}})
	must(t, nil, `
url = "`+s.URL()+`"
login = dict(password="secret", insecure=True)
listed = sftp.list(url, path="/pub", **login)
assert listed.ok, listed.error
assert listed.total == 1 and listed.entries[0].name == "data.bin", listed.entries
assert listed.entries[0].type == "file", listed.entries[0].type
assert "value" not in listed and "records" not in listed
got = sftp.download(url, path="/pub/data.bin", expect_size="10", **login)
assert got.ok and got.size == 10, got.error or got.size
missing = sftp.download(url, path="/missing", **login)
assert missing.ok is False and "no such file" in missing.error, missing.error
denied = sftp.upload(url, path="/up-{id}", body="x", **login)
assert denied.ok is False and "pass allow_writes: true" in denied.error, denied.error
rt = sftp.roundtrip(url, path="/rt-{id}", size="8", allow_writes=True, **login)
assert rt.ok and rt.size == 8, rt.error or rt.size
`)
}

func TestSFTP_OneSetOfConnections(t *testing.T) {
	s := sftptest.Start(t)
	s.Apply(sftptest.Config{Files: map[string][]byte{"/pub/data.bin": []byte("hello sftp")}})
	must(t, nil, `
url = "`+s.URL()+`"
for i in range(4):
    r = sftp.download(url, path="/pub/data.bin", password="secret", insecure=True)
    assert r.ok, r.error
`)
	if n := s.ConnCount(); n != 1 {
		t.Fatalf("connections %d, want 1", n)
	}
}

func TestSFTP_PasswordFromEnv(t *testing.T) {
	s := sftptest.Start(t)
	s.Apply(sftptest.Config{Files: map[string][]byte{"/pub/data.bin": []byte("hello sftp")}})
	in := inputs.New(map[string]string{"SFTP_PASSWORD": "secret"}, nil)
	must(t, nil, `
r = sftp.download("`+s.URL()+`", path="/pub/data.bin", password=env.SFTP_PASSWORD, insecure=True)
assert r.ok and r.size == 10, r.error or r.size
`, WithInputs(in))
}

func TestSFTP_RefusesModeAndPasswordEnv(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"unknown option":     {`sftp.download("sftp://load@127.0.0.1:9", path="/", password="x", bogus=1)`, "unknown"},
		"mode":               {`sftp.download("sftp://load@127.0.0.1:9", path="/", password="x", mode="list")`, "function you called sets it"},
		"password_env":       {`sftp.download("sftp://load@127.0.0.1:9", path="/", password_env="X")`, "pass password: env.NAME"},
		"key_passphrase_env": {`sftp.download("sftp://load@127.0.0.1:9", path="/", key_passphrase_env="X")`, "pass key_passphrase: env.NAME"},
		"tls option":         {`sftp.download("sftp://load@127.0.0.1:9", path="/", password="x", ca_file="ca.pem")`, "unknown"},
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

func TestSFTP_HostNotAllowed(t *testing.T) {
	check := func(host string) error { return errors.New("host not allowed") }
	if err := runIteration(t, netapi.SafetyCheck(check), `sftp.download("sftp://files.example.com", path="/")`); err == nil || !strings.Contains(err.Error(), "host not allowed") {
		t.Fatal(err)
	}
}
