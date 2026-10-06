package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// NFR-01: `vegaload run` cold start, with the binary already installed,
// completes scenario startup in under 2 seconds for a scenario with up
// to 100 virtual users.
//
// "Startup complete" is the moment all 100 virtual users are running an
// iteration of the scenario at the same time. The scenario makes one
// request per iteration, and the test server holds each request until
// 100 of them are in flight at once. The clock starts just before the
// process is started and stops when the 100th request arrives, so it
// covers process start, reading and compiling the scenario, creating
// the 100 virtual users, and running their first iterations. The test
// fails if that takes more than the PRD's 2 seconds.
const (
	nfr01VUs   = 100
	nfr01Limit = 2 * time.Second
)

func TestNFR01_ColdStartWith100VUsWithin2Seconds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the 100-VU cold start check in -short mode")
	}
	bin := buildVegaload(t)

	var (
		mu       sync.Mutex
		inFlight int
		ready    = make(chan time.Time)
		once     sync.Once
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		full := inFlight == nfr01VUs
		mu.Unlock()
		if full {
			once.Do(func() { ready <- time.Now() })
		}
		// Hold the request until every VU has arrived, or the test ends.
		select {
		case <-time.After(15 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	scenario := filepath.Join(dir, "startup.vl.js")
	script := `export default function () { http.get("` + srv.URL + `/hold", {}); }` + "\n"
	if err := os.WriteFile(scenario, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "run",
		"-vus", "100", "-duration", "20s", "-timeout", "30s",
		"-no-report", "-no-open", "-audit-log", filepath.Join(dir, "audit.log"),
		scenario)
	started := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting vegaload: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	select {
	case at := <-ready:
		elapsed := at.Sub(started)
		t.Logf("100 virtual users running after %s (limit %s)", elapsed.Round(time.Millisecond), nfr01Limit)
		if elapsed > nfr01Limit {
			t.Errorf("NFR-01: scenario startup for %d virtual users took %s, the PRD limit is %s", nfr01VUs, elapsed, nfr01Limit)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("NFR-01: %d virtual users were not all running after 30s; the PRD limit is %s", nfr01VUs, nfr01Limit)
	}
}
