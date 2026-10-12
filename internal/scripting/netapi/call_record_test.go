package netapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol/ftp/ftptest"
	"github.com/vegaload/vegaload/internal/protocol/sftp/sftptest"
	"github.com/vegaload/vegaload/internal/report"
)

func TestCall_RecordsOneFinishedCall(t *testing.T) {
	s := ftptest.Start(t)
	s.Apply(ftptest.Config{Files: map[string][]byte{"/pub/data.bin": []byte("hello ftp")}})
	c := NewProtoClient(nil, 5*time.Second)
	defer c.Close()
	col := report.NewCollector()
	c.SetCallRecorder(col)

	reply, err := c.Call(context.Background(), "ftp.download", ProtoCall{
		URL:     s.URL(),
		Options: map[string]string{"path": "/pub/data.bin"},
	})
	if err != nil || !reply.OK {
		t.Fatalf("download: %v ok=%v err=%s", err, reply != nil && reply.OK, errText(reply))
	}
	res := col.Finish("fixed-vus", time.Second)
	p, ok := res.Protocol("ftp")
	if !ok || p.Total != 1 || p.Failed != 0 || len(p.Operations) != 1 || p.Operations[0].Name != "download" {
		t.Fatalf("protocol = %+v ok %v", p, ok)
	}
	if p.BytesReceived != reply.BytesReceived || p.Operations[0].BytesReceived != reply.BytesReceived {
		t.Fatalf("bytes %d, reply %d", p.BytesReceived, reply.BytesReceived)
	}

	denied, err := c.Call(context.Background(), "ftp.upload", ProtoCall{
		URL:     s.URL(),
		Options: map[string]string{"path": "/up-{id}"},
		Body:    []byte("x"),
	})
	if err != nil || denied.OK || !strings.Contains(denied.Error, "allow_writes") {
		t.Fatalf("denied: %v %+v", err, denied)
	}
	if got := col.Finish("fixed-vus", time.Second).Protocols[0].Total; got != 1 {
		t.Fatalf("refused upload was recorded, total %d", got)
	}

	if _, err := c.Call(context.Background(), "ftp.not-a-function", ProtoCall{URL: s.URL()}); err == nil {
		t.Fatal("unknown function returned no error")
	}
	if n := len(col.Finish("fixed-vus", time.Second).Protocols[0].Operations); n != 1 {
		t.Fatalf("unknown function was stored, %d operations", n)
	}
}

func TestCall_RunEndedIsNotRecorded(t *testing.T) {
	s := ftptest.Start(t)
	s.Apply(ftptest.Config{Files: map[string][]byte{"/pub/data.bin": []byte("hello ftp")}})
	c := NewProtoClient(nil, 5*time.Second)
	defer c.Close()
	col := report.NewCollector()
	c.SetCallRecorder(col)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = c.Call(ctx, "ftp.download", ProtoCall{
		URL:     s.URL(),
		Options: map[string]string{"path": "/pub/data.bin"},
	})
	if col.Finish("fixed-vus", time.Second).Protocols != nil {
		t.Fatal("a call cut off by the end of the run was recorded")
	}
}

func TestHTTP_RecordsAndSkipsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("abcdef"))
	}))
	defer srv.Close()

	col := report.NewCollector()
	client := NewHTTPClient(nil, time.Second)
	client.SetCallRecorder(col)
	resp, err := client.Do(context.Background(), "get", srv.URL, []byte("hi"), Options{})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("do: %v %+v", err, resp)
	}
	p, ok := col.Finish("fixed-vus", time.Second).Protocol("http")
	if !ok || p.Total != 1 || p.Failed != 0 || p.Operations[0].Name != "GET" || p.BytesSent != 2 || p.BytesReceived != 6 {
		t.Fatalf("%+v ok %v", p, ok)
	}

	blocked := NewHTTPClient(func(string) error {
		return errors.New("host is not localhost or allowlisted")
	}, time.Second)
	blocked.SetCallRecorder(col)
	if _, err := blocked.Do(context.Background(), "POST", "http://example.com/", nil, Options{}); err == nil {
		t.Fatal("blocked host was allowed")
	}
	if got := col.Finish("fixed-vus", time.Second).Protocols[0].Total; got != 1 {
		t.Fatalf("blocked call was recorded, total %d", got)
	}

	other := NewHTTPClient(nil, time.Second)
	fresh := report.NewCollector()
	other.SetCallRecorder(fresh)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = other.Do(ctx, "GET", srv.URL, nil, Options{})
	if fresh.Finish("fixed-vus", time.Second).Protocols != nil {
		t.Fatal("an HTTP call cut off by the end of the run was recorded")
	}

	verbs := NewHTTPClient(nil, time.Second)
	verbs.SetCallRecorder(fresh)
	_, err = verbs.Do(context.Background(), "TRACE", srv.URL, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	op := fresh.Finish("fixed-vus", time.Second).Protocols[0].Operations[0].Name
	if op != "OTHER" {
		t.Fatalf("verb = %s, want OTHER", op)
	}
}

func TestXferMeasures_NewConnectionAndSkippedPhase(t *testing.T) {
	rec := report.NewCollector()
	recordXferMeasures(rec, "ftp", "upload", XferTiming{UploadMs: 0, ConnectMs: 0, LoginMs: 0})
	recordXferMeasures(rec, "ftp", "list", XferTiming{ConnectMs: 4, LoginMs: 2})
	got := rec.Finish("fixed-vus", time.Second)
	// The list call above did not record a call, only measures. upload_time
	// is present at 0 because upload ran that phase. connect_time is not,
	// because 0 means the connection was reused.
	if len(got.Protocols) != 1 {
		t.Fatalf("%d protocols", len(got.Protocols))
	}
	names := map[string]int64{}
	for _, m := range got.Protocols[0].Measures {
		names[m.Name] = m.Count
	}
	if names["upload_time"] != 1 || names["connect_time"] != 1 || names["login_time"] != 1 {
		t.Fatalf("measures = %+v", names)
	}
	if _, ok := names["download_time"]; ok {
		t.Fatal("list recorded download_time")
	}

	checkConnMeasures(t, "ftp", func(conn string) (calls int, positive int, conns int) {
		s := ftptest.Start(t)
		s.Apply(ftptest.Config{Files: map[string][]byte{"/pub/data.bin": []byte("hello ftp")}})
		c := NewProtoClient(nil, 5*time.Second)
		t.Cleanup(c.Close)
		col := report.NewCollector()
		c.SetCallRecorder(col)
		for i := 0; i < 4; i++ {
			r, err := c.Call(context.Background(), "ftp.download", ProtoCall{
				URL: s.URL(),
				Options: map[string]string{
					"path": "/pub/data.bin", "connection": conn,
				},
			})
			if err != nil || !r.OK {
				t.Fatalf("%s call %d: %v %s", conn, i, err, errText(r))
			}
			calls++
			if r.XferTiming.ConnectMs > 0 {
				positive++
			}
		}
		p, _ := col.Finish("fixed-vus", time.Second).Protocol("ftp")
		return measureCount(p, "connect_time"), positive, s.ConnCount()
	})

	pw := "secret"
	checkConnMeasures(t, "sftp", func(conn string) (calls int, positive int, conns int) {
		s := sftptest.Start(t)
		s.Apply(sftptest.Config{Files: map[string][]byte{"/pub/data.bin": []byte("hello sftp")}})
		c := NewProtoClient(nil, 5*time.Second)
		t.Cleanup(c.Close)
		col := report.NewCollector()
		c.SetCallRecorder(col)
		for i := 0; i < 4; i++ {
			r, err := c.Call(context.Background(), "sftp.download", ProtoCall{
				URL: s.URL(), Insecure: true, Password: &pw,
				Options: map[string]string{
					"path": "/pub/data.bin", "connection": conn,
				},
			})
			if err != nil || !r.OK {
				t.Fatalf("%s call %d: %v %s", conn, i, err, errText(r))
			}
			calls++
			if r.XferTiming.ConnectMs > 0 {
				positive++
			}
		}
		p, _ := col.Finish("fixed-vus", time.Second).Protocol("sftp")
		return measureCount(p, "connect_time"), positive, s.ConnCount()
	})
}

func checkConnMeasures(t *testing.T, protocol string, run func(conn string) (measured, positive, conns int)) {
	t.Helper()
	shared, sharedPositive, sharedConns := run("shared")
	perCall, perPositive, perConns := run("per_call")
	if shared != sharedPositive {
		t.Errorf("%s shared: connect_time %d, replies with ConnectMs > 0: %d", protocol, shared, sharedPositive)
	}
	if shared > 1 {
		t.Errorf("%s shared recorded connect_time %d times, want at most 1", protocol, shared)
	}
	if perCall != perPositive {
		t.Errorf("%s per_call: connect_time %d, replies with ConnectMs > 0: %d", protocol, perCall, perPositive)
	}
	if perConns <= sharedConns {
		t.Errorf("%s per_call opened %d connections, shared opened %d", protocol, perConns, sharedConns)
	}
}

func measureCount(p report.ProtocolResult, name string) int {
	for _, m := range p.Measures {
		if m.Name == name {
			return int(m.Count)
		}
	}
	return 0
}

func errText(r *ProtoReply) string {
	if r == nil {
		return ""
	}
	return r.Error
}
