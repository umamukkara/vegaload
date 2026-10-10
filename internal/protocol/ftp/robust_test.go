package ftp

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// A stall at any step of the login must end the call at the call timeout.
func TestTimeout_StallDuringLogin(t *testing.T) {
	s := srv(t)
	for _, cmd := range []string{"USER", "PASS", "FEAT", "TYPE", "OPTS"} {
		t.Run(cmd, func(t *testing.T) {
			s.ClearFaults()
			s.StallOn(cmd)
			before := runtime.NumGoroutine()
			start := time.Now()
			res, _ := run(t, s.URL(), modeConnect, nil, "", 300*time.Millisecond)
			if res.Success || res.Err == nil || !strings.Contains(res.Err.Error(), "timed out") {
				t.Fatalf("err %v", res.Err)
			}
			if time.Since(start) > 1500*time.Millisecond {
				t.Fatalf("took %s", time.Since(start))
			}
			waitIdle(before + 8)
		})
	}
}

// A data connection that stops in the middle of a big download.
func TestTimeout_StallMidDownload(t *testing.T) {
	s := srv(t)
	s.Generate("/pub/big.bin", 20<<20)
	s.StallDataAfter(1 << 20)
	start := time.Now()
	res, _ := run(t, s.URL(), modeDownload, map[string]string{"path": "/pub/big.bin"}, "", 500*time.Millisecond)
	if res.Success || res.Err == nil || !strings.Contains(res.Err.Error(), "timed out") {
		t.Fatalf("err %v", res.Err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("took %s", time.Since(start))
	}
}

// A refused STOR is a failed call, and the session still works afterwards.
func TestUpload_RefusedStoreKeepsTheSession(t *testing.T) {
	s := srv(t)
	d, err := New(tgt(s.URL(), modeUpload, map[string]string{"path": "/up-{id}", "size": "100", "allow_writes": "true"}, ""), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s.ReplyOn("STOR", 553, "not allowed")
	if r, _ := d.Run(context.Background()); r.Success || !strings.Contains(r.Err.Error(), "553") {
		t.Fatalf("first call: %+v", r)
	}
	s.ClearFaults()
	if r, _ := d.Run(context.Background()); !r.Success {
		t.Fatalf("second call: %v", r.Err)
	}
	if s.ConnCount() != 1 {
		t.Fatalf("conns %d, want 1", s.ConnCount())
	}
}

// 16 callers on one session: every mode finishes, none deadlocks.
func TestPool_SixteenCallersOneSessionEveryMode(t *testing.T) {
	s := srv(t)
	cases := []struct {
		mode string
		opt  map[string]string
	}{
		{modeConnect, map[string]string{}},
		{modeDownload, map[string]string{"path": "/pub/data.bin"}},
		{modeList, map[string]string{"path": "/pub"}},
		{modeStat, map[string]string{"path": "/pub/data.bin"}},
		{modeRoundtrip, map[string]string{"path": "/rt-{id}", "size": "2KiB", "allow_writes": "true"}},
	}
	for _, tc := range cases {
		tc.opt["sessions"] = "1"
		d, err := New(tgt(s.URL(), tc.mode, tc.opt, ""), 3*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		fails := make(chan string, 16)
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if r, _ := d.Run(context.Background()); !r.Success {
					fails <- r.Err.Error()
				}
			}()
		}
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: deadlock", tc.mode)
		}
		close(fails)
		for f := range fails {
			t.Errorf("%s: %s", tc.mode, f)
		}
		_ = d.Close()
	}
}
