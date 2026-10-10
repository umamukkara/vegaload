package ftp

import (
	"context"
	"crypto/sha256"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/protocol/ftp/ftptest"
	"github.com/vegaload/vegaload/internal/protocol/xfercommon"
)

func srv(t *testing.T) *ftptest.Server {
	t.Helper()
	s := ftptest.Start(t)
	s.Apply(ftptest.Config{Files: map[string][]byte{
		"/pub/data.bin": []byte("hello ftp"),
		"/pub/.hidden":  []byte("secret"),
	}})
	return s
}

func tgt(raw, mode string, opt map[string]string, body string) protocol.Target {
	if opt == nil {
		opt = map[string]string{}
	}
	opt["mode"] = mode
	return protocol.Target{URL: raw, Options: opt, Body: []byte(body)}
}

func run(t *testing.T, raw string, mode string, opt map[string]string, body string, timeout time.Duration) (protocol.Result, Reply) {
	t.Helper()
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	d, err := New(tgt(raw, mode, opt, body), timeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d.Run(context.Background())
}

func TestNew_DoesNotConnect(t *testing.T) {
	s := srv(t)
	if _, err := New(tgt(s.URL(), modeDownload, map[string]string{"path": "/pub/data.bin"}, ""), time.Second); err != nil {
		t.Fatal(err)
	}
	if s.ConnCount() != 0 {
		t.Fatalf("connections %d", s.ConnCount())
	}
}

func TestOptions_RefusesSecretsAndBadURLs(t *testing.T) {
	s := srv(t)
	cases := []struct {
		name string
		tg   protocol.Target
		want string
	}{
		{"url password", protocol.Target{URL: "ftp://u:secret@127.0.0.1:21"}, "password"},
		{"query", protocol.Target{URL: "ftp://127.0.0.1:21?token=secret", Options: map[string]string{"mode": "connect"}}, "token"},
		{"path", protocol.Target{URL: "ftp://127.0.0.1:21/secret-path", Options: map[string]string{"mode": "connect"}}, "URL path"},
		{"line in path", tgt(s.URL(), modeDownload, map[string]string{"path": "a\r\nDELE b"}, ""), "line break"},
		{"nul path", tgt(s.URL(), modeDownload, map[string]string{"path": "a\x00b"}, ""), "NUL"},
		{"long path", tgt(s.URL(), modeDownload, map[string]string{"path": strings.Repeat("a", 5000)}, ""), "4096"},
		{"user break", tgt("ftp://127.0.0.1:21", modeConnect, map[string]string{"username": "a\nb"}, ""), "username"},
		{"size and body", tgt(s.URL(), modeUpload, map[string]string{"path": "/x-{id}", "size": "1", "allow_writes": "true"}, "hi"), "not both"},
		{"size in download", tgt(s.URL(), modeDownload, map[string]string{"path": "/pub/data.bin", "size": "1"}, ""), "not used in mode download"},
		{"bad unit", tgt(s.URL(), modeUpload, map[string]string{"path": "/x-{id}", "size": "10MB", "allow_writes": "true"}, ""), "unit"},
		{"bad sessions", tgt(s.URL(), modeConnect, map[string]string{"sessions": "0"}, ""), "sessions"},
		{"bad sha", tgt(s.URL(), modeDownload, map[string]string{"path": "/pub/data.bin", "expect_sha256": "zz"}, ""), "hex"},
		{"tls none on ftps", protocol.Target{URL: "ftps://127.0.0.1:990", Options: map[string]string{"tls": "none", "mode": "connect"}}, "tls=none"},
		{"admin without writes", tgt(s.URL(), modeConnect, map[string]string{"allow_admin": "true"}, ""), "allow_writes"},
		{"no host", protocol.Target{URL: "ftp://", Options: map[string]string{"mode": "connect"}}, "no host"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.tg, time.Second)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "DELE") {
				t.Fatalf("secret in error: %v", err)
			}
		})
	}
	if s.ConnCount() != 0 {
		t.Fatalf("a refused option connected: %d", s.ConnCount())
	}
}

func TestPasswordEnv_LineBreakNeverConnects(t *testing.T) {
	s := srv(t)
	t.Setenv("FTP_BAD", "pw\r\nQUIT")
	_, err := New(tgt(s.URL(), modeConnect, map[string]string{"username": "load", "password_env": "FTP_BAD"}, ""), time.Second)
	if err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatal(err)
	}
	if strings.Contains(err.Error(), "pw") || s.ConnCount() != 0 {
		t.Fatalf("err %v conns %d", err, s.ConnCount())
	}
}

func TestConnect_ReportsTimingAndSkipsThePool(t *testing.T) {
	s := srv(t)
	res, rep := run(t, s.URL(), modeConnect, nil, "", 0)
	if !res.Success || rep.Timing.ConnectMs < 0 || rep.Timing.LoginMs < 0 {
		t.Fatalf("%+v %+v", res, rep)
	}
	res, rep = run(t, s.URL(), modeConnect, nil, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	if s.ConnCount() < 2 {
		t.Fatalf("connect reused a session, conns %d", s.ConnCount())
	}
}

func TestDownload_BytesAndChecks(t *testing.T) {
	s := srv(t)
	sum := sha256.Sum256([]byte("hello ftp"))
	hexSum := xfercommon.HexDigest(sum[:])
	res, rep := run(t, s.URL(), modeDownload, map[string]string{
		"path": "/pub/data.bin", "expect_size": "9", "expect_sha256": hexSum, "expect": "hello",
	}, "", 0)
	if !res.Success || res.BytesReceived != 9 || rep.Size != 9 {
		t.Fatalf("%+v %+v", res, rep)
	}
	res, _ = run(t, s.URL(), modeDownload, map[string]string{"path": "/pub/data.bin", "expect_size": "3"}, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "want 3") {
		t.Fatal(res.Err)
	}
	res, _ = run(t, s.URL(), modeDownload, map[string]string{"path": "/missing"}, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "550") {
		t.Fatal(res.Err)
	}
	res, _ = run(t, s.URL(), modeDownload, map[string]string{"path": "/pub/data.bin"}, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
}

func TestDownload_Boundaries(t *testing.T) {
	s := srv(t)
	files := map[string][]byte{"/e": {}}
	for _, n := range []int{1, 64*1024 - 1, 64 * 1024, 64*1024 + 1, 1 << 20} {
		files["/n"+itoa(n)] = bytesOf(n)
	}
	s.Apply(ftptest.Config{Files: files})
	for name, body := range files {
		res, rep := run(t, s.URL(), modeDownload, map[string]string{"path": name, "expect_size": itoa(len(body))}, "", 0)
		if !res.Success || rep.Size != int64(len(body)) {
			t.Fatalf("%s: %+v %d", name, res.Err, rep.Size)
		}
	}
}

func TestUpload_FillAndID(t *testing.T) {
	s := srv(t)
	res, _ := run(t, s.URL(), modeUpload, map[string]string{"path": "/up-{id}.bin", "size": "0", "allow_writes": "true"}, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	before := s.ConnCount()
	res, _ = run(t, s.URL(), modeUpload, map[string]string{"path": "/up-{id}.bin"}, "abc", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "allow_writes") {
		t.Fatal(res.Err)
	}
	if s.ConnCount() != before {
		t.Fatalf("refused upload connected: %d to %d", before, s.ConnCount())
	}
	res, _ = run(t, s.URL(), modeUpload, map[string]string{"path": "/up-{id}.txt", "size": "32", "fill": "text", "allow_writes": "true"}, "", 0)
	if !res.Success || res.BytesSent != 32 {
		t.Fatalf("%+v", res)
	}
}

func TestRoundtrip_VerifiesAndDeletes(t *testing.T) {
	s := srv(t)
	opt := map[string]string{"path": "/rt-{id}.bin", "size": "100", "fill": "zero", "allow_writes": "true"}
	res, rep := run(t, s.URL(), modeRoundtrip, opt, "", 0)
	if !res.Success || !strings.Contains(rep.Text, "deleted") {
		t.Fatalf("%+v %s", res.Err, rep.Text)
	}
	if len(s.Files()) != 2 { // the seeded files only; roundtrip file is gone
		t.Fatalf("files left: %v", s.Files())
	}
	s.Apply(ftptest.Config{Files: map[string][]byte{"/pub/data.bin": []byte("hello ftp")}, CorruptDownloads: true})
	res, _ = run(t, s.URL(), modeRoundtrip, opt, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "not the file that was uploaded") {
		t.Fatal(res.Err)
	}
	for name := range s.Files() {
		if strings.Contains(name, "/rt-") {
			t.Fatalf("failed roundtrip left %s", name)
		}
	}
	_, err := New(tgt(s.URL(), modeRoundtrip, map[string]string{"path": "/fixed.bin", "size": "1", "allow_writes": "true"}, ""), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d, err := New(tgt(s.URL(), modeRoundtrip, map[string]string{"path": "/fixed.bin", "size": "1", "allow_writes": "true"}, ""), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, _ = d.Run(context.Background())
	_ = d.Close()
	if res.Success || !strings.Contains(res.Err.Error(), "{id}") {
		t.Fatal(res.Err)
	}
}

func TestListAndStat(t *testing.T) {
	s := srv(t)
	res, rep := run(t, s.URL(), modeList, map[string]string{"path": "/pub", "expect": "data.bin", "limit": "10"}, "", 0)
	if !res.Success || rep.Total < 1 {
		t.Fatalf("%+v %+v", res.Err, rep)
	}
	s.Apply(ftptest.Config{NoMLST: true, Files: map[string][]byte{
		"/pub/data.bin": []byte("hello ftp"),
		"/pub/.hidden":  []byte("secret"),
	}})
	res, rep = run(t, s.URL(), modeList, map[string]string{"path": "/pub"}, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	for _, e := range rep.Entries {
		if e.Name == ".hidden" {
			t.Fatal("dot file listed without hidden")
		}
	}
	res, rep = run(t, s.URL(), modeList, map[string]string{"path": "/pub", "hidden": "true"}, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	saw := false
	for _, e := range rep.Entries {
		if e.Name == ".hidden" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("hidden file missing: %+v", rep.Entries)
	}
	if _, err := New(tgt(s.URL(), modeList, map[string]string{"path": "-a"}, ""), time.Second); err == nil {
		t.Fatal("path -a was accepted")
	}
	res, rep = run(t, s.URL(), modeStat, map[string]string{"path": "/pub/data.bin"}, "", 0)
	if !res.Success || rep.Size != 9 {
		t.Fatalf("%+v %+v", res.Err, rep)
	}
	s.Apply(ftptest.Config{NoSIZE: true, Files: map[string][]byte{"/pub/data.bin": []byte("hello ftp")}})
	res, _ = run(t, s.URL(), modeStat, map[string]string{"path": "/pub/data.bin"}, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "SIZE") {
		t.Fatal(res.Err)
	}
}

func TestSafetyTable(t *testing.T) {
	s := srv(t)
	before := s.ConnCount()
	checks := []protocol.Target{
		tgt(s.URL(), modeUpload, map[string]string{"path": "/a-{id}"}, "x"),
		tgt(s.URL(), modeUpload, map[string]string{"path": "/fixed", "allow_writes": "true"}, "x"),
		tgt(s.URL(), modeDelete, map[string]string{"path": "/pub/data.bin", "allow_writes": "true"}, ""),
		tgt(s.URL(), modeRoundtrip, map[string]string{"path": "/a-{id}", "size": "1"}, ""),
	}
	for _, tg := range checks {
		d, err := New(tg, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		res, _ := d.Run(context.Background())
		_ = d.Close()
		if res.Success {
			t.Fatalf("allowed %#v", tg.Options)
		}
	}
	if s.ConnCount() != before {
		t.Fatalf("safety check connected: %d", s.ConnCount())
	}
}

func TestCall_CopiesFlags(t *testing.T) {
	parent, err := NewConn(protocol.Target{
		URL:     "ftp://load@127.0.0.1:21",
		Options: map[string]string{"allow_writes": "true", "allow_admin": "true", "sessions": "4", "connection": "per_call", "data_host": "announced", "epsv": "off", "tls": "explicit"},
	}, time.Second, strPtr("pw"))
	if err != nil {
		t.Fatal(err)
	}
	child, err := parent.Call(map[string]string{"mode": modeUpload, "path": "/a-{id}", "size": "1"}, nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !child.allowWrites || !child.allowAdmin || !child.script || child.sessions != 4 || !child.perCall || child.dataHostControl || !child.epsvOff || child.tlsMode != "explicit" || child.password != "pw" || child.owns {
		t.Fatalf("call dropped a flag: %+v", child)
	}
}

func TestPool_OneSessionManyCallers(t *testing.T) {
	s := srv(t)
	d, err := New(tgt(s.URL(), modeDownload, map[string]string{"path": "/pub/data.bin", "sessions": "1"}, ""), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	errc := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() {
			res, _ := d.Run(context.Background())
			errc <- res.Err
		}()
	}
	for i := 0; i < 16; i++ {
		select {
		case err := <-errc:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("deadlock")
		}
	}
	if n := s.ConnCount(); n != 1 {
		t.Fatalf("connections %d, want 1", n)
	}
}

func TestPool_PerCallDialsEachTime(t *testing.T) {
	s := srv(t)
	d, err := New(tgt(s.URL(), modeDownload, map[string]string{"path": "/pub/data.bin", "connection": "per_call"}, ""), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for i := 0; i < 2; i++ {
		res, _ := d.Run(context.Background())
		if !res.Success {
			t.Fatal(res.Err)
		}
	}
	if n := s.ConnCount(); n != 2 {
		t.Fatalf("connections %d, want 2", n)
	}
}

func TestNoRetry_DropOnRetr(t *testing.T) {
	s := srv(t)
	s.DropOn("RETR")
	before := s.ConnCount()
	res, _ := run(t, s.URL(), modeDownload, map[string]string{"path": "/pub/data.bin"}, "", 0)
	if res.Success {
		t.Fatal("dropped RETR succeeded")
	}
	if s.ConnCount() != before+1 {
		t.Fatalf("connections %d, want one attempt", s.ConnCount())
	}
	s.ClearFaults()
	res, _ = run(t, s.URL(), modeDownload, map[string]string{"path": "/pub/data.bin"}, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
}

func TestMaxConnections_NamesTheSessionCap(t *testing.T) {
	s := srv(t)
	s.MaxConnections(1)
	res, _ := run(t, s.URL(), modeDownload, map[string]string{"path": "/pub/data.bin"}, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	res, _ = run(t, s.URL(), modeDownload, map[string]string{"path": "/pub/data.bin", "sessions": "3"}, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "sessions is 3") {
		t.Fatal(res.Err)
	}
}

func TestIdle_ClosedSessionIsNotReused(t *testing.T) {
	s := srv(t)
	s.IdleTimeout(200 * time.Millisecond)
	d, err := New(tgt(s.URL(), modeDownload, map[string]string{"path": "/pub/data.bin"}, ""), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	res, _ := d.Run(context.Background())
	if !res.Success {
		t.Fatal(res.Err)
	}
	time.Sleep(400 * time.Millisecond)
	res, _ = d.Run(context.Background())
	if res.Success || !strings.Contains(res.Err.Error(), "closed by the server") {
		t.Fatal(res.Err)
	}
	res, _ = d.Run(context.Background())
	if !res.Success {
		t.Fatal(res.Err)
	}
}

func TestTruncate_FailsTheCall(t *testing.T) {
	s := srv(t)
	s.TruncateDataAfter(4)
	res, _ := run(t, s.URL(), modeDownload, map[string]string{"path": "/pub/data.bin"}, "", 0)
	if res.Success || !strings.Contains(res.Err.Error(), "426") {
		t.Fatal(res.Err)
	}
}

func TestDownload_LargeFileStaysFlat(t *testing.T) {
	s := srv(t)
	const n = 32 << 20
	s.Generate("/big", n)
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	res, rep := run(t, s.URL(), modeDownload, map[string]string{"path": "/big", "expect_size": itoa(n)}, "", 15*time.Second)
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if !res.Success || rep.Size != n {
		t.Fatalf("%v size %d", res.Err, rep.Size)
	}
	if after.HeapAlloc > before.HeapAlloc+16<<20 {
		t.Fatalf("heap grew by %d", after.HeapAlloc-before.HeapAlloc)
	}
}

func TestList_DOSFormat(t *testing.T) {
	s := srv(t)
	s.Apply(ftptest.Config{
		NoMLST: true, DOSList: true,
		Files: map[string][]byte{"/pub/data.bin": []byte("hello ftp")},
	})
	res, rep := run(t, s.URL(), modeList, map[string]string{"path": "/pub", "expect": "data.bin"}, "", 0)
	if !res.Success || rep.Total != 1 || rep.Entries[0].Size != 9 {
		t.Fatalf("%v %+v", res.Err, rep)
	}
}

func TestTLS_SessionReuseOnTheDataConnection(t *testing.T) {
	s := ftptest.StartTLS(t)
	s.Apply(ftptest.Config{
		RequireTLSSessionReuse: true,
		Files:                  map[string][]byte{"/pub/data.bin": []byte("hello ftp")},
	})
	tg := tgt(s.URL(), modeDownload, map[string]string{"path": "/pub/data.bin", "tls": "explicit"}, "")
	tg.InsecureSkipVerify = true
	d, err := New(tg, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	res, _ := d.Run(context.Background())
	if !res.Success {
		t.Fatal(res.Err)
	}
}

func TestUpload_EmptyFileOverTLS(t *testing.T) {
	for _, implicit := range []bool{false, true} {
		name := "explicit"
		if implicit {
			name = "implicit"
		}
		t.Run(name, func(t *testing.T) {
			var s *ftptest.Server
			if implicit {
				s = ftptest.StartImplicitTLS(t)
			} else {
				s = ftptest.StartTLS(t)
			}
			opt := map[string]string{"path": "/empty-{id}", "size": "0", "allow_writes": "true", "tls": "explicit"}
			if implicit {
				opt["tls"] = "implicit"
			}
			tg := tgt(s.URL(), modeUpload, opt, "")
			tg.InsecureSkipVerify = true
			d, err := New(tg, 3*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			res, rep := d.Run(context.Background())
			if !res.Success || rep.Size != 0 {
				t.Fatalf("%v %+v", res.Err, rep)
			}
		})
	}
}

func TestTimeout_StallEndsTheCall(t *testing.T) {
	s := srv(t)
	for _, cmd := range []string{"RETR", "STOR", "LIST", "PASV", "226"} {
		t.Run(cmd, func(t *testing.T) {
			s.ClearFaults()
			stall := cmd
			mode := modeDownload
			opt := map[string]string{"path": "/pub/data.bin", "epsv": "off"}
			switch cmd {
			case "STOR":
				mode = modeUpload
				opt["path"] = "/up-{id}"
				opt["size"] = "10"
				opt["allow_writes"] = "true"
			case "LIST":
				mode = modeList
				opt["path"] = "/pub"
				stall = "MLSD"
			}
			s.StallOn(stall)
			start := time.Now()
			before := runtime.NumGoroutine()
			res, _ := run(t, s.URL(), mode, opt, "", 200*time.Millisecond)
			if res.Success || !strings.Contains(res.Err.Error(), "timed out") {
				t.Fatal(res.Err)
			}
			if time.Since(start) > time.Second {
				t.Fatalf("took %s", time.Since(start))
			}
			waitIdle(before + 8)
		})
	}
}

func TestDataHost_ControlIgnoresTheAnnouncedAddress(t *testing.T) {
	s := srv(t)
	s.Apply(ftptest.Config{AnnounceHost: "255.255.255.255", Files: map[string][]byte{"/pub/data.bin": []byte("hello ftp")}})
	res, _ := run(t, s.URL(), modeDownload, map[string]string{"path": "/pub/data.bin", "epsv": "off", "data_host": "control"}, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	res, _ = run(t, s.URL(), modeDownload, map[string]string{"path": "/pub/data.bin", "epsv": "off", "data_host": "announced"}, "", time.Second)
	if res.Success || !strings.Contains(res.Err.Error(), "data connection") {
		t.Fatal(res.Err)
	}
}

func TestNoDebugOutputInSource(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "DialWithDebug"+"Output") {
			t.Fatalf("%s uses debug output", e.Name())
		}
	}
}

func strPtr(s string) *string { return &s }

func itoa(n int) string { return strconv.Itoa(n) }

func bytesOf(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

func waitIdle(max int) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && runtime.NumGoroutine() > max {
		time.Sleep(20 * time.Millisecond)
		runtime.Gosched()
	}
}
