package sftp

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/protocol/sftp/sftptest"
)

func TestConnections_SingleFlightAndSpread(t *testing.T) {
	s := srv(t)
	s.Apply(sftptest.Config{Files: map[string][]byte{"/pub/a.bin": []byte("hello")}})
	if err := s.Mkdir("/pub/sub"); err != nil {
		t.Fatal(err)
	}
	modes := []struct {
		mode string
		opt  map[string]string
		body string
	}{
		{modeDownload, map[string]string{"path": "/pub/a.bin"}, ""},
		{modeUpload, map[string]string{"path": "/up-{id}.bin", "size": "8", "allow_writes": "true"}, ""},
		{modeList, map[string]string{"path": "/pub"}, ""},
		{modeStat, map[string]string{"path": "/pub/a.bin"}, ""},
		{modeDelete, map[string]string{"path": "/auto/{id}", "allow_writes": "true", "allow_admin": "true"}, ""},
		{modeRoundtrip, map[string]string{"path": "/rt-{id}.bin", "size": "8", "allow_writes": "true"}, ""},
		{modeConnect, nil, ""},
	}
	for _, tc := range modes {
		t.Run(tc.mode, func(t *testing.T) {
			s.ClearFaults()
			opt := copyOpt(tc.opt)
			opt["connections"] = "1"
			d := newDrv(t, s, tc.mode, opt, tc.body, 5*time.Second)
			start := s.ConnCount()
			err := together(t, 16, func() error {
				res, _ := d.Run(context.Background())
				if !res.Success {
					return res.Err
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			got := s.ConnCount() - start
			if tc.mode == modeConnect {
				if got != 16 {
					t.Fatalf("connect opened %d connections, want 16", got)
				}
				return
			}
			if got != 1 {
				t.Fatalf("opened %d connections, want 1", got)
			}
		})
	}
	s.ClearFaults()
	s.Delay(50 * time.Millisecond)
	d := newDrv(t, s, modeDownload, map[string]string{"path": "/pub/a.bin", "connections": "4"}, "", 5*time.Second)
	start := s.ConnCount()
	if err := together(t, 16, func() error {
		res, _ := d.Run(context.Background())
		if !res.Success {
			return res.Err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n := s.ConnCount() - start; n > 4 || n < 1 {
		t.Fatalf("opened %d connections, want 1 to 4", n)
	}
}

func TestConnection_PerCallCloses(t *testing.T) {
	s := srv(t)
	s.Apply(sftptest.Config{Files: map[string][]byte{"/pub/a.bin": []byte("hello")}})
	start := s.ConnCount()
	for i := 0; i < 3; i++ {
		res, _ := run(t, s, modeDownload, map[string]string{"path": "/pub/a.bin", "connection": "per_call"}, "", 0)
		if !res.Success {
			t.Fatal(res.Err)
		}
		waitOpen(t, s, 0)
	}
	if s.ConnCount()-start != 3 {
		t.Fatalf("conn count grew by %d, want 3", s.ConnCount()-start)
	}
}

func TestDial_FailedAttemptIsNotStored(t *testing.T) {
	s := srv(t)
	s.RefuseConnections(true)
	d := newDrv(t, s, modeConnect, map[string]string{"connections": "1"}, "", time.Second)
	if err := together(t, 16, func() error {
		res, _ := d.Run(context.Background())
		if res.Success {
			return errString("a refused connection succeeded")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.RefuseConnections(false)
	res, _ := d.Run(context.Background())
	if !res.Success {
		t.Fatal(res.Err)
	}
}

func TestNoRetry_DropThenNextCall(t *testing.T) {
	s := srv(t)
	s.Apply(sftptest.Config{Files: map[string][]byte{"/pub/a.bin": bytesOf(64 * 1024)}})
	s.DropOn("read")
	res, _ := run(t, s, modeDownload, map[string]string{"path": "/pub/a.bin", "connections": "1"}, "", time.Second)
	if res.Success || !strings.Contains(res.Err.Error(), "closed while this call was running") {
		t.Fatal(res.Err)
	}
	reads := 0
	for _, op := range s.Ops() {
		if strings.Contains(op, " read ") {
			reads++
		}
	}
	if reads != 1 {
		t.Fatalf("read ops = %v", s.Ops())
	}
	s.ClearFaults()
	res, _ = run(t, s, modeDownload, map[string]string{"path": "/pub/a.bin", "connections": "1"}, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
}

func TestIdle_NextCallDialsAgain(t *testing.T) {
	s := srv(t)
	s.Apply(sftptest.Config{Files: map[string][]byte{"/pub/a.bin": []byte("hello")}})
	res, _ := run(t, s, modeDownload, map[string]string{"path": "/pub/a.bin", "connections": "1"}, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	s.IdleTimeout(30 * time.Millisecond)
	waitOpen(t, s, 0)
	s.ClearFaults()
	before := len(s.Ops())
	res, _ = run(t, s, modeDownload, map[string]string{"path": "/pub/a.bin", "connections": "1"}, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	var saw2 bool
	for _, op := range s.Ops()[before:] {
		if strings.HasPrefix(op, "1 ") {
			t.Fatalf("the dead connection was used: %s", op)
		}
		if strings.HasPrefix(op, "2 ") {
			saw2 = true
		}
	}
	if !saw2 {
		t.Fatalf("ops = %v", s.Ops())
	}
}

func TestTimeout_EveryStall(t *testing.T) {
	s := srv(t)
	s.Apply(sftptest.Config{Files: map[string][]byte{"/pub/a.bin": bytesOf(1024)}})
	points := []struct {
		op, mode string
		opt      map[string]string
		body     string
	}{
		{"open", modeDownload, map[string]string{"path": "/pub/a.bin"}, ""},
		{"read", modeDownload, map[string]string{"path": "/pub/a.bin"}, ""},
		{"write", modeUpload, map[string]string{"path": "/up-{id}", "size": "32", "allow_writes": "true"}, ""},
		{"close", modeUpload, map[string]string{"path": "/up-{id}", "size": "32", "allow_writes": "true"}, ""},
		{"readdir", modeList, map[string]string{"path": "/pub"}, ""},
		{"stat", modeStat, map[string]string{"path": "/pub/a.bin"}, ""},
		{"remove", modeDelete, map[string]string{"path": "/pub/a.bin", "allow_writes": "true", "allow_admin": "true"}, ""},
	}
	for _, pt := range points {
		t.Run(pt.op, func(t *testing.T) {
			s.ClearFaults()
			s.StallOn(pt.op)
			before := runtime.NumGoroutine()
			start := time.Now()
			res, _ := run(t, s, pt.mode, pt.opt, pt.body, 200*time.Millisecond)
			if res.Success || !strings.Contains(res.Err.Error(), "timed out after") {
				t.Fatal(res.Err)
			}
			if time.Since(start) > time.Second {
				t.Fatalf("took %s", time.Since(start))
			}
			s.ClearFaults()
			waitIdle(before + 12)
			res, _ = run(t, s, modeStat, map[string]string{"path": "/pub/a.bin"}, "", 0)
			if !res.Success {
				t.Fatal(res.Err)
			}
		})
	}
}

func TestTimeout_HandshakeAndAuth(t *testing.T) {
	s := srv(t)
	s.HandshakeDelay(5 * time.Second)
	start := time.Now()
	before := runtime.NumGoroutine()
	res, _ := run(t, s, modeConnect, nil, "", 200*time.Millisecond)
	if res.Success || !strings.Contains(res.Err.Error(), "timed out after") {
		t.Fatal(res.Err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("handshake took %s", time.Since(start))
	}
	s.ClearFaults()
	waitIdle(before + 12)
	s.AuthDelay(5 * time.Second)
	start = time.Now()
	res, _ = run(t, s, modeConnect, nil, "", 200*time.Millisecond)
	if res.Success || !strings.Contains(res.Err.Error(), "timed out after") {
		t.Fatal(res.Err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("auth took %s", time.Since(start))
	}
	s.ClearFaults()
	res, _ = run(t, s, modeConnect, nil, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
}

func TestParentCancel(t *testing.T) {
	s := srv(t)
	s.Apply(sftptest.Config{Files: map[string][]byte{"/pub/a.bin": []byte("hello")}})
	d := newDrv(t, s, modeDownload, map[string]string{"path": "/pub/a.bin"}, "", time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, _ := d.Run(ctx)
	if res.Err == nil || res.Err.Error() != context.Canceled.Error() {
		t.Fatal(res.Err)
	}
}

func TestUnreachable(t *testing.T) {
	t.Setenv("SFTP_PW", "secret")
	d, err := New(protocol.Target{
		URL: "sftp://load@127.0.0.1:1", InsecureSkipVerify: true,
		Options: map[string]string{"mode": "connect", "password_env": "SFTP_PW"},
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	res, err := d.Do(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || !strings.Contains(res.Err.Error(), "could not connect") {
		t.Fatal(res.Err)
	}
}

func TestSharedTimeout_FailsTheOtherCall(t *testing.T) {
	s := srv(t)
	s.Apply(sftptest.Config{Files: map[string][]byte{"/pub/a.bin": bytesOf(1024), "/pub/b.bin": bytesOf(1024)}})
	d := newDrv(t, s, modeConnect, map[string]string{"connections": "1"}, "", 5*time.Second)
	// Open the shared connection before the two downloads.
	if res, _ := d.Run(context.Background()); !res.Success {
		t.Fatal(res.Err)
	}
	s.StallOn("read")
	a, err := d.Call(map[string]string{"mode": "download", "path": "/pub/a.bin"}, nil, 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.Call(map[string]string{"mode": "download", "path": "/pub/b.bin"}, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var ra, rb protocol.Result
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		ra, _ = a.Run(context.Background())
	}()
	go func() {
		defer wg.Done()
		rb, _ = b.Run(context.Background())
	}()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the two calls did not finish")
	}
	s.ClearFaults()
	timed := 0
	closed := 0
	for _, res := range []protocol.Result{ra, rb} {
		if res.Err == nil {
			t.Fatal("a call succeeded while the read was stalled")
		}
		if strings.Contains(res.Err.Error(), "timed out after") {
			timed++
		}
		if strings.Contains(res.Err.Error(), "closed while this call was running") {
			closed++
		}
	}
	if timed != 1 || closed != 1 {
		t.Fatalf("timed=%d closed=%d a=%v b=%v", timed, closed, ra.Err, rb.Err)
	}
	c, err := d.Call(map[string]string{"mode": "stat", "path": "/pub/a.bin"}, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := c.Run(context.Background())
	if !res.Success {
		t.Fatal(res.Err)
	}
}

func TestLimits_MaxAndRefuse(t *testing.T) {
	s := srv(t)
	s.MaxConnections(1)
	res, _ := run(t, s, modeConnect, map[string]string{"connection": "per_call"}, "", 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	res, _ = run(t, s, modeConnect, map[string]string{"connection": "per_call"}, "", time.Second)
	if res.Success || !strings.Contains(res.Err.Error(), "MaxStartups") {
		t.Fatal(res.Err)
	}
	s.ClearFaults()
	s.RefuseConnections(true)
	res, _ = run(t, s, modeConnect, map[string]string{"connection": "per_call"}, "", time.Second)
	if res.Success || res.Err == nil || !strings.Contains(res.Err.Error(), "sftp:") {
		t.Fatal(res.Err)
	}
}

func TestCall_KeepsParentFlags(t *testing.T) {
	s := srv(t)
	t.Setenv("SFTP_PW", "secret")
	d, err := New(protocol.Target{
		URL: s.URL(), InsecureSkipVerify: true,
		Options: map[string]string{
			"mode": "connect", "password_env": "SFTP_PW", "connections": "4",
			"connection": "per_call", "allow_writes": "true", "allow_admin": "true",
			"host_key": s.HostKeyFingerprint(),
		},
	}, time.Second)
	if err == nil {
		t.Fatal("host_key with -insecure was accepted")
	}
	d, err = New(protocol.Target{
		URL: s.URL(),
		Options: map[string]string{
			"mode": "connect", "password_env": "SFTP_PW", "connections": "4",
			"connection": "per_call", "allow_writes": "true", "allow_admin": "true",
			"host_key": s.HostKeyFingerprint(),
		},
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	c, err := d.Call(map[string]string{"mode": "stat", "path": "/pub"}, nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if c.shared != d.shared || !c.perCall || c.conns != 4 || !c.allowWrites || !c.allowAdmin || c.insecure || c.username != "load" || len(c.hostKeys) != 1 || c.password != d.password || c.owns {
		t.Fatalf("call dropped a flag: perCall=%v conns=%d writes=%v admin=%v insecure=%v user=%s keys=%d owns=%v",
			c.perCall, c.conns, c.allowWrites, c.allowAdmin, c.insecure, c.username, len(c.hostKeys), c.owns)
	}
}

func newDrv(t *testing.T, s *sftptest.Server, mode string, opt map[string]string, body string, timeout time.Duration) *Driver {
	t.Helper()
	t.Setenv("SFTP_PW", "secret")
	opt = copyOpt(opt)
	opt["mode"] = mode
	if opt["password_env"] == "" && opt["key_file"] == "" {
		opt["password_env"] = "SFTP_PW"
	}
	d, err := New(protocol.Target{
		URL: s.URL(), Body: []byte(body), Options: opt, InsecureSkipVerify: true,
	}, timeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func copyOpt(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func together(t *testing.T, n int, fn func() error) error {
	t.Helper()
	var wg sync.WaitGroup
	errc := make(chan error, n)
	ready := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ready
			if err := fn(); err != nil {
				errc <- err
			}
		}()
	}
	close(ready)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock")
	}
	select {
	case err := <-errc:
		return err
	default:
		return nil
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func waitOpen(t *testing.T, s *sftptest.Server, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.OpenConns() <= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("open connections = %d, want <= %d", s.OpenConns(), n)
}

func waitIdle(max int) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && runtime.NumGoroutine() > max {
		time.Sleep(20 * time.Millisecond)
		runtime.Gosched()
	}
}
