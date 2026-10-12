package sftptest

import (
	"bytes"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func TestServer_FilesSinkAndGenerated(t *testing.T) {
	s := Start(t)
	s.Apply(Config{Files: map[string][]byte{"/pub/a.bin": []byte("hello")}})
	s.GenFile("/gen/big.bin", 1000)
	cli := dial(t, s)
	got, err := readFile(cli, "/pub/a.bin")
	if err != nil || string(got) != "hello" {
		t.Fatalf("%v %q", err, got)
	}
	gen, err := readFile(cli, "/gen/big.bin")
	if err != nil || int64(len(gen)) != 1000 || gen[0] != GenByte(0) || gen[999] != GenByte(999) {
		t.Fatalf("gen %v len %d", err, len(gen))
	}
	f, err := cli.OpenFile("/sink/out.bin", os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte("abc"), 100)
	if _, err := f.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	n, sum, ok := s.SinkHash("/sink/out.bin")
	if !ok || n != int64(len(body)) || sum == "" {
		t.Fatalf("sink %d %s %v", n, sum, ok)
	}
}

func TestServer_RefusesExecShellAndPty(t *testing.T) {
	s := Start(t)
	conn := sshClient(t, s)
	for _, kind := range []string{"exec", "shell", "pty-req"} {
		sess, err := conn.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		ok, err := sess.SendRequest(kind, true, sshString("true"))
		_ = sess.Close()
		if err != nil || ok {
			t.Fatalf("%s ok=%v err=%v", kind, ok, err)
		}
	}
	got := strings.Join(s.Requests(), ",")
	for _, kind := range []string{"exec", "shell", "pty-req"} {
		if !strings.Contains(got, kind) {
			t.Fatalf("requests %q, missing %s", got, kind)
		}
	}
}

func TestServer_Faults(t *testing.T) {
	s := Start(t)
	s.Apply(Config{Files: map[string][]byte{"/pub/a.bin": bytes.Repeat([]byte("x"), 64)}})
	cli := dial(t, s)

	s.TruncateReadAfter(4)
	got, err := readFile(cli, "/pub/a.bin")
	if err == nil || len(got) > 4 {
		t.Fatalf("truncate %v len %d", err, len(got))
	}
	s.ClearFaults()

	s.CorruptByte(0)
	got, err = readFile(cli, "/pub/a.bin")
	if err != nil || len(got) == 0 || got[0] == 'x' {
		t.Fatalf("corrupt %v %q", err, got[:1])
	}
	s.ClearFaults()

	s.ReplyOn("stat", 4, "gone")
	_, err = cli.Stat("/pub/a.bin")
	if err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatal(err)
	}
	s.ClearFaults()
	if _, err := cli.Stat("/pub/a.bin"); err != nil {
		t.Fatal(err)
	}

	s.DropOn("read")
	_, err = readFile(cli, "/pub/a.bin")
	if err == nil {
		t.Fatal("dropped read succeeded")
	}
	s.ClearFaults()

	s.Apply(Config{NoSubsystem: true})
	if _, err := dialErr(s); err == nil || !strings.Contains(strings.ToLower(err.Error()), "subsystem") {
		t.Fatal(err)
	}
}

func TestServer_Limits(t *testing.T) {
	s := Start(t)
	s.MaxConnections(1)
	cli := dial(t, s)
	if _, err := dialErr(s); err == nil {
		t.Fatal("a second connection was accepted")
	}
	_ = cli.Close()
	s.ClearFaults()
	s.RefuseConnections(true)
	if _, err := dialErr(s); err == nil {
		t.Fatal("a refused connection was accepted")
	}
}

func dial(t *testing.T, s *Server) *sftp.Client {
	t.Helper()
	cli, err := dialErr(s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

func dialErr(s *Server) (*sftp.Client, error) {
	raw, err := net.DialTimeout("tcp", s.Addr(), 2*time.Second)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            "load",
		Auth:            []ssh.AuthMethod{ssh.Password("secret")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         2 * time.Second,
	}
	c, chans, reqs, err := ssh.NewClientConn(raw, s.Addr(), cfg)
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	sc, err := sftp.NewClient(ssh.NewClient(c, chans, reqs))
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	return sc, nil
}

func sshClient(t *testing.T, s *Server) *ssh.Client {
	t.Helper()
	raw, err := net.DialTimeout("tcp", s.Addr(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ClientConfig{
		User:            "load",
		Auth:            []ssh.AuthMethod{ssh.Password("secret")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	c, chans, reqs, err := ssh.NewClientConn(raw, s.Addr(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	cli := ssh.NewClient(c, chans, reqs)
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

func readFile(cli *sftp.Client, path string) ([]byte, error) {
	f, err := cli.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func sshString(s string) []byte {
	b := make([]byte, 4+len(s))
	b[0] = byte(len(s) >> 24)
	b[1] = byte(len(s) >> 16)
	b[2] = byte(len(s) >> 8)
	b[3] = byte(len(s))
	copy(b[4:], s)
	return b
}
