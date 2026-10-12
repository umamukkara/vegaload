package sftp

import (
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"github.com/vegaload/vegaload/internal/protocol/sftp/sftptest"
	"github.com/vegaload/vegaload/internal/protocol/xfercommon"
	"golang.org/x/crypto/ssh"
)

// TestThroughput_Log measures 100 MiB transfers. Set VEGALOAD_SFTP_BENCH=1
// to run it.
func TestThroughput_Log(t *testing.T) {
	if os.Getenv("VEGALOAD_SFTP_BENCH") == "" {
		t.Skip("VEGALOAD_SFTP_BENCH is not set")
	}
	s := srv(t)
	s.GenFile("/pub/huge.bin", 100<<20)
	start := time.Now()
	res, rep := run(t, s, modeDownload, map[string]string{"path": "/pub/huge.bin"}, "", time.Minute)
	if !res.Success || rep.Size != 100<<20 {
		t.Fatal(res.Err, rep.Size)
	}
	t.Logf("driver download 100MiB %s", miB(100<<20, time.Since(start)))

	start = time.Now()
	res, rep = run(t, s, modeUpload, map[string]string{
		"path": "/sink/huge.bin", "size": "100MiB", "allow_writes": "true", "allow_admin": "true",
	}, "", time.Minute)
	if !res.Success || rep.Size != 100<<20 {
		t.Fatal(res.Err, rep.Size)
	}
	t.Logf("driver upload 100MiB %s", miB(100<<20, time.Since(start)))

	start = time.Now()
	res, rep = run(t, s, modeUpload, map[string]string{
		"path": "/sink/huge-cw.bin", "size": "100MiB", "allow_writes": "true", "allow_admin": "true",
		"concurrent_writes": "true",
	}, "", time.Minute)
	if !res.Success || rep.Size != 100<<20 {
		t.Fatal(res.Err, rep.Size)
	}
	t.Logf("driver upload concurrent_writes 100MiB %s", miB(100<<20, time.Since(start)))

	down, up := directTransfer(t, s, nil)
	t.Logf("library default download %s upload %s", down, up)
	down, up = directTransfer(t, s, []sftp.ClientOption{sftp.UseConcurrentWrites(true)})
	t.Logf("library concurrent writes download %s upload %s", down, up)
}

func directTransfer(t *testing.T, s *sftptest.Server, opts []sftp.ClientOption) (string, string) {
	t.Helper()
	raw, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ClientConfig{
		User: "load", Auth: []ssh.AuthMethod{ssh.Password("secret")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	c, chans, reqs, err := ssh.NewClientConn(raw, s.Addr(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	cli, err := sftp.NewClient(ssh.NewClient(c, chans, reqs), opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	start := time.Now()
	f, err := cli.Open("/pub/huge.bin")
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, f)
	_ = f.Close()
	if err != nil || n != 100<<20 {
		t.Fatalf("download %v %d", err, n)
	}
	down := time.Since(start)

	body, err := xfercommon.FillReader("zero", 100<<20)
	if err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	w, err := cli.OpenFile("/sink/tuned.bin", os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		t.Fatal(err)
	}
	n, err = io.Copy(w, body)
	cerr := w.Close()
	if err != nil || cerr != nil || n != 100<<20 {
		t.Fatalf("upload copy %v close %v n %d", err, cerr, n)
	}
	return miB(100<<20, down), miB(100<<20, time.Since(start))
}

func miB(n int64, d time.Duration) string {
	secs := d.Seconds()
	if secs <= 0 {
		secs = 0.001
	}
	return fmt.Sprintf("%s (%.1f MiB/s)", d.Round(time.Millisecond), float64(n)/1024/1024/secs)
}
