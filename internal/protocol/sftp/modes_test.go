package sftp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/protocol/sftp/sftptest"
)

func TestDownload_SizesAndChecks(t *testing.T) {
	s := srv(t)
	s.Apply(sftptest.Config{Files: map[string][]byte{
		"/pub/empty": {},
		"/pub/one":   []byte("Z"),
		"/pub/data":  []byte("hello sftp"),
	}})
	for _, n := range []int{32767, 32768, 32769} {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i)
		}
		s.Apply(sftptest.Config{Files: map[string][]byte{"/pub/n": b}})
		res, rep := run(t, s, modeDownload, map[string]string{"path": "/pub/n"}, "", 0)
		if !res.Success || rep.Size != int64(n) || res.BytesReceived != int64(n) {
			t.Fatalf("n=%d success=%v size=%d err=%v", n, res.Success, rep.Size, res.Err)
		}
	}
	res, rep := run(t, s, modeDownload, map[string]string{"path": "/pub/empty"}, "", 0)
	if !res.Success || rep.Size != 0 {
		t.Fatal(res.Err, rep.Size)
	}
	res, _ = run(t, s, modeDownload, map[string]string{"path": "/pub/data", "expect_size": "10"}, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	res, _ = run(t, s, modeDownload, map[string]string{"path": "/pub/data", "expect_size": "3"}, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "want 3") {
		t.Fatal(res.Err)
	}
	sum := sha256.Sum256([]byte("hello sftp"))
	res, _ = run(t, s, modeDownload, map[string]string{"path": "/pub/data", "expect_sha256": hex.EncodeToString(sum[:])}, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	res, _ = run(t, s, modeDownload, map[string]string{"path": "/pub/data", "expect_sha256": strings.Repeat("ab", 32)}, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "SHA-256") {
		t.Fatal(res.Err)
	}
	res, _ = run(t, s, modeDownload, map[string]string{"path": "/pub/data", "expect": "sftp"}, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	res, _ = run(t, s, modeDownload, map[string]string{"path": "/pub/data", "expect": "missing"}, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "does not contain") {
		t.Fatal(res.Err)
	}
	res, _ = run(t, s, modeDownload, map[string]string{"path": "/pub/nope"}, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "no such file") {
		t.Fatal(res.Err)
	}
	res, _ = run(t, s, modeDownload, map[string]string{"path": "/pub/data"}, "", 0)
	if !res.Success {
		t.Fatal("the connection was not usable after a missing file:", res.Err)
	}
	if err := s.Mkdir("/pub/dir"); err != nil {
		t.Fatal(err)
	}
	res, _ = run(t, s, modeDownload, map[string]string{"path": "/pub/dir"}, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "is a directory") {
		t.Fatal(res.Err)
	}
	s.Apply(sftptest.Config{Files: map[string][]byte{"/pub/cut": bytesOf(100)}})
	s.TruncateReadAfter(10)
	res, _ = run(t, s, modeDownload, map[string]string{"path": "/pub/cut", "expect_size": "100"}, "", 0)
	if res.Success {
		t.Fatal("a short read was accepted")
	}
	s.ClearFaults()
}

func TestDownload_TwentyMiB(t *testing.T) {
	s := srv(t)
	const n = 20 << 20
	s.GenFile("/pub/big.bin", n)
	sum := genSum(n)
	res, rep := run(t, s, modeDownload, map[string]string{"path": "/pub/big.bin", "expect_sha256": sum}, "", 30*time.Second)
	if !res.Success || rep.Size != n {
		t.Fatal(res.Err, rep.Size)
	}
}

func TestDownload_MemoryStaysFlat(t *testing.T) {
	s := srv(t)
	const n = 200 << 20
	s.GenFile("/pub/huge.bin", n)
	before := heapUsed()
	var peak atomic.Uint64
	peak.Store(before)
	stop := make(chan struct{})
	go sampleHeap(&peak, stop)
	res, rep := run(t, s, modeDownload, map[string]string{"path": "/pub/huge.bin"}, "", time.Minute)
	close(stop)
	if !res.Success || rep.Size != n {
		t.Fatal(res.Err, rep.Size)
	}
	if grew := peak.Load() - before; grew > 32<<20 {
		t.Fatalf("heap grew by %d bytes", grew)
	}
}

func TestUpload_BodySizeFillAndID(t *testing.T) {
	s := srv(t)
	res, _ := run(t, s, modeUpload, map[string]string{"path": "/up-{id}.txt", "allow_writes": "true"}, "hello {id}", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	var bodies [][]byte
	for _, b := range s.Files() {
		bodies = append(bodies, b)
	}
	if len(bodies) != 1 || !strings.Contains(string(bodies[0]), "hello ") || strings.Contains(string(bodies[0]), "{id}") {
		t.Fatalf("files = %q", bodies)
	}
	res, _ = run(t, s, modeUpload, map[string]string{"path": "/up-{id}.bin", "size": "0", "allow_writes": "true"}, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	for _, fill := range []string{"zero", "text", "random"} {
		res, rep := run(t, s, modeUpload, map[string]string{"path": "/fill-{id}.bin", "size": "32", "fill": fill, "allow_writes": "true"}, "", 0)
		if !res.Success || rep.Size != 32 {
			t.Fatalf("%s: %v %+v", fill, res.Err, rep)
		}
	}
	before := len(s.Files())
	res, _ = run(t, s, modeUpload, map[string]string{"path": "/up-{id}.bin", "size": "8", "allow_writes": "true"}, "", 0)
	res2, _ := run(t, s, modeUpload, map[string]string{"path": "/up-{id}.bin", "size": "8", "allow_writes": "true"}, "", 0)
	if !res.Success || !res2.Success {
		t.Fatal(res.Err, res2.Err)
	}
	if len(s.Files()) != before+2 {
		t.Fatalf("two uploads made %d new files", len(s.Files())-before)
	}
}

func TestUpload_TwentyMiBAndCloseError(t *testing.T) {
	s := srv(t)
	res, rep := run(t, s, modeUpload, map[string]string{"path": "/big-{id}.bin", "size": "20MiB", "allow_writes": "true"}, "", 30*time.Second)
	if !res.Success || rep.Size != 20<<20 {
		t.Fatal(res.Err, rep.Size)
	}
	s.ReplyOn("close", 4, "disk full")
	res, _ = run(t, s, modeUpload, map[string]string{"path": "/x-{id}.bin", "size": "4", "allow_writes": "true"}, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "disk full") {
		t.Fatal(res.Err)
	}
	s.ClearFaults()
	s.ReplyOn("open", 3, "no")
	res, _ = run(t, s, modeUpload, map[string]string{"path": "/no-{id}.bin", "size": "4", "allow_writes": "true"}, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "permission denied") {
		t.Fatal(res.Err)
	}
	s.ClearFaults()
	res, _ = run(t, s, modeDownload, map[string]string{"path": "/pub/data"}, "", 0)
	// /pub/data may not exist. A stat of a known file proves the connection.
	s.Apply(sftptest.Config{Files: map[string][]byte{"/pub/data": []byte("hello sftp")}})
	res, _ = run(t, s, modeDownload, map[string]string{"path": "/pub/data"}, "", 0)
	if !res.Success {
		t.Fatal("connection was not usable after a permission error:", res.Err)
	}
}

func TestUpload_MemoryStaysFlat(t *testing.T) {
	s := srv(t)
	const n = 200 << 20
	before := heapUsed()
	var peak atomic.Uint64
	peak.Store(before)
	stop := make(chan struct{})
	go sampleHeap(&peak, stop)
	res, rep := run(t, s, modeUpload, map[string]string{"path": "/sink/huge.bin", "size": "200MiB", "allow_writes": "true", "allow_admin": "true"}, "", time.Minute)
	close(stop)
	if !res.Success || rep.Size != n {
		t.Fatal(res.Err, rep.Size)
	}
	got, _, ok := s.SinkHash("/sink/huge.bin")
	if !ok || got != n {
		t.Fatalf("sink = %d ok=%v", got, ok)
	}
	if grew := peak.Load() - before; grew > 32<<20 {
		t.Fatalf("heap grew by %d bytes", grew)
	}
}

func TestListStatDeleteRoundtrip(t *testing.T) {
	s := srv(t)
	s.Apply(sftptest.Config{Files: map[string][]byte{
		"/pub/b.txt":    []byte("b"),
		"/pub/a.txt":    []byte("a"),
		"/pub/.hidden":  []byte("h"),
		"/pub/gone.bin": []byte("bye"),
	}})
	if err := s.Mkdir("/pub/sub"); err != nil {
		t.Fatal(err)
	}
	if err := s.Symlink("/pub/a.txt", "/pub/link.txt"); err != nil {
		t.Fatal(err)
	}
	res, rep := run(t, s, modeList, map[string]string{"path": "/pub", "expect": "a.txt"}, "", 0)
	if !res.Success || res.BytesReceived != 0 {
		t.Fatal(res.Err, res.BytesReceived)
	}
	if rep.Total < 4 {
		t.Fatalf("total %d, want the visible names", rep.Total)
	}
	names := map[string]string{}
	for _, e := range rep.Entries {
		names[e.Name] = e.Type
		if !e.Time.Equal(e.Time.UTC()) {
			t.Fatalf("time not UTC: %v", e.Time)
		}
	}
	if _, ok := names[".hidden"]; ok {
		t.Fatal("hidden name was returned")
	}
	if names["a.txt"] != "file" || names["sub"] != "dir" || names["link.txt"] != "link" {
		t.Fatalf("types = %v", names)
	}
	res, rep = run(t, s, modeList, map[string]string{"path": "/pub", "hidden": "true", "limit": "2"}, "", 0)
	if !res.Success || rep.Total < 5 || len(rep.Entries) != 2 {
		t.Fatalf("success=%v total=%d entries=%d err=%v", res.Success, rep.Total, len(rep.Entries), res.Err)
	}
	if rep.Entries[0].Name > rep.Entries[1].Name {
		t.Fatal("entries are not sorted")
	}
	res, _ = run(t, s, modeList, map[string]string{"path": "/missing"}, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "no such file") {
		t.Fatal(res.Err)
	}
	res, _ = run(t, s, modeList, map[string]string{"path": "/pub/a.txt"}, "", 0)
	if res.Success {
		t.Fatal("list of a file succeeded")
	}
	res, _ = run(t, s, modeList, map[string]string{"path": "-odd"}, "", 0)
	if res.Err != nil && strings.Contains(res.Err.Error(), "must not start with -") {
		t.Fatal("a dash path was refused at setup")
	}
	res, rep = run(t, s, modeStat, map[string]string{"path": "/pub/a.txt"}, "", 0)
	if !res.Success || rep.Size != 1 || !strings.Contains(rep.Text, "type file") {
		t.Fatal(res.Err, rep.Text, rep.Size)
	}
	res, rep = run(t, s, modeStat, map[string]string{"path": "/pub/sub"}, "", 0)
	if !res.Success || !strings.Contains(rep.Text, "type dir") {
		t.Fatal(res.Err, rep.Text)
	}
	res, _ = run(t, s, modeStat, map[string]string{"path": "/pub/missing"}, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "no such file") {
		t.Fatal(res.Err)
	}
	res, _ = run(t, s, modeDelete, map[string]string{"path": "/pub/sub", "allow_writes": "true", "allow_admin": "true"}, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "files only") {
		t.Fatal(res.Err)
	}
	res, _ = run(t, s, modeDelete, map[string]string{"path": "/pub/gone.bin", "allow_writes": "true", "allow_admin": "true"}, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	if _, ok := s.File("/pub/gone.bin"); ok {
		t.Fatal("the file is still there")
	}
	res, _ = run(t, s, modeRoundtrip, map[string]string{"path": "/rt-{id}.bin", "size": "64", "allow_writes": "true"}, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	s.CorruptByte(0)
	res, _ = run(t, s, modeRoundtrip, map[string]string{"path": "/rt-{id}.bin", "size": "64", "allow_writes": "true"}, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "not the file that was uploaded") {
		t.Fatal(res.Err)
	}
	s.ClearFaults()
	for i := 0; i < 100; i++ {
		opt := map[string]string{"path": "/rt-{id}.bin", "size": "8", "allow_writes": "true"}
		if i%2 == 0 {
			s.CorruptByte(0)
		} else {
			s.ClearFaults()
		}
		_, _ = run(t, s, modeRoundtrip, opt, "", 0)
	}
	s.ClearFaults()
	for name := range s.Files() {
		if strings.Contains(name, "/rt-") {
			t.Fatalf("left over %s", name)
		}
	}
	res, _ = run(t, s, modeRoundtrip, map[string]string{"path": "/fixed.bin", "size": "8", "allow_writes": "true"}, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "{id}") {
		t.Fatal(res.Err)
	}
}

func TestRoundtrip_LeavesEmptyDirectory(t *testing.T) {
	s := srv(t)
	t.Setenv("SFTP_PW", "secret")
	d, err := New(protocol.Target{
		URL: s.URL(), InsecureSkipVerify: true,
		Options: map[string]string{
			"mode": "roundtrip", "path": "/{id}", "size": "8",
			"allow_writes": "true", "password_env": "SFTP_PW",
		},
	}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	dir := "/" + d.shared.ids.Pending()
	if err := s.Mkdir(dir); err != nil {
		t.Fatal(err)
	}
	res, rep := d.Run(context.Background())
	if res.Success || !strings.Contains(rep.Text, "left on the server") {
		t.Fatalf("success=%v text=%q err=%v", res.Success, rep.Text, res.Err)
	}
	res, rep = run(t, s, modeStat, map[string]string{"path": dir}, "", 0)
	if !res.Success || !strings.Contains(rep.Text, "type dir") {
		t.Fatal("empty directory was removed", res.Err, rep.Text)
	}
}

func TestSafety(t *testing.T) {
	s := srv(t)
	before := s.ConnCount()
	res, _ := run(t, s, modeUpload, map[string]string{"path": "/up-{id}"}, "x", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "use -opt allow_writes=true") {
		t.Fatal(res.Err)
	}
	res, _ = run(t, s, modeUpload, map[string]string{"path": "/fixed", "allow_writes": "true"}, "x", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "allow_admin=true") {
		t.Fatal(res.Err)
	}
	res, _ = run(t, s, modeDelete, map[string]string{"path": "/a"}, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "allow_admin=true") {
		t.Fatal(res.Err)
	}
	res, _ = run(t, s, modeRoundtrip, map[string]string{"path": "/no-id", "allow_writes": "true"}, "x", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "{id}") {
		t.Fatal(res.Err)
	}
	if s.ConnCount() != before {
		t.Fatal("a refused call sent traffic")
	}
	t.Setenv("SFTP_PW", "secret")
	_, err := New(protocol.Target{
		URL: s.URL(), InsecureSkipVerify: true,
		Options: map[string]string{"mode": "connect", "password_env": "SFTP_PW", "allow_admin": "true"},
	}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "allow_writes") {
		t.Fatal(err)
	}
}

func bytesOf(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

func genSum(n int64) string {
	h := sha256.New()
	buf := make([]byte, 32*1024)
	var off int64
	for off < n {
		m := int64(len(buf))
		if off+m > n {
			m = n - off
		}
		for i := int64(0); i < m; i++ {
			buf[i] = sftptest.GenByte(off + i)
		}
		_, _ = h.Write(buf[:m])
		off += m
	}
	return hex.EncodeToString(h.Sum(nil))
}

func heapUsed() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

func sampleHeap(peak *atomic.Uint64, stop <-chan struct{}) {
	var m runtime.MemStats
	for {
		select {
		case <-stop:
			return
		default:
		}
		runtime.ReadMemStats(&m)
		for {
			old := peak.Load()
			if m.HeapAlloc <= old || peak.CompareAndSwap(old, m.HeapAlloc) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
}
