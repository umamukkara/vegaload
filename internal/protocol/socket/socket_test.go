package socket

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
)

// lineServer accepts TCP connections, reads one line, and answers with
// the reply func's result.
func lineServer(t *testing.T, reply func(line string) string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				line, err := bufio.NewReader(c).ReadString('\n')
				if err != nil {
					return
				}
				_, _ = io.WriteString(c, reply(line))
			}()
		}
	}()
	return l.Addr().String()
}

func tcpDo(t *testing.T, tg protocol.Target) protocol.Result {
	t.Helper()
	d, err := NewTCP(tg, 2*time.Second)
	if err != nil {
		t.Fatalf("NewTCP: %v", err)
	}
	res, err := d.Do(context.Background())
	if err != nil {
		t.Fatalf("Do returned a run-stopping error: %v", err)
	}
	return res
}

func TestTCP_ConnectOnly(t *testing.T) {
	addr := lineServer(t, func(string) string { return "" })
	res := tcpDo(t, protocol.Target{URL: "tcp://" + addr})
	if !res.Success {
		t.Fatalf("connect-only probe failed: %v", res.Err)
	}
}

func TestTCP_ConnectRefused(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	res := tcpDo(t, protocol.Target{URL: addr})
	if res.Success || res.Err == nil {
		t.Fatalf("closed port should fail: %+v", res)
	}
}

func TestTCP_SendAndReadExact(t *testing.T) {
	addr := lineServer(t, func(l string) string { return "echo:" + l })
	res := tcpDo(t, protocol.Target{
		URL: addr, Body: []byte(`hi\n`), Options: map[string]string{"read": "8"},
	})
	if !res.Success {
		t.Fatalf("failed: %v", res.Err)
	}
	if res.BytesSent != 3 || res.BytesReceived != 8 {
		t.Errorf("bytes sent/received = %d/%d, want 3/8", res.BytesSent, res.BytesReceived)
	}
}

func TestTCP_ShortReadFails(t *testing.T) {
	addr := lineServer(t, func(string) string { return "ok" })
	res := tcpDo(t, protocol.Target{URL: addr, Body: []byte(`x\n`), Options: map[string]string{"read": "10"}})
	if res.Success {
		t.Fatal("a reply shorter than read=10 must fail")
	}
}

func TestTCP_UntilAndExpect(t *testing.T) {
	addr := lineServer(t, func(string) string { return "220 ready\r\n" })

	res := tcpDo(t, protocol.Target{URL: addr, Body: []byte(`HELO\n`),
		Options: map[string]string{"until": `\r\n`, "expect": "220"}})
	if !res.Success {
		t.Fatalf("until+expect failed: %v", res.Err)
	}

	res = tcpDo(t, protocol.Target{URL: addr, Body: []byte(`HELO\n`),
		Options: map[string]string{"until": `\r\n`, "expect": "550"}})
	if res.Success {
		t.Fatal("a reply without the expected text must fail")
	}
}

func TestTCP_ExpectAloneReadsUntilFound(t *testing.T) {
	addr := lineServer(t, func(string) string { return "PONG\n" })
	res := tcpDo(t, protocol.Target{URL: addr, Body: []byte(`PING\n`), Options: map[string]string{"expect": "PONG"}})
	if !res.Success {
		t.Fatalf("failed: %v", res.Err)
	}
}

func TestTCP_ClosedBeforeDelimiter(t *testing.T) {
	addr := lineServer(t, func(string) string { return "partial" })
	res := tcpDo(t, protocol.Target{URL: addr, Body: []byte(`x\n`), Options: map[string]string{"until": "END"}})
	if res.Success || res.Err == nil {
		t.Fatalf("connection closed before delimiter must fail: %+v", res)
	}
}

func TestTCP_MaxReadStops(t *testing.T) {
	addr := lineServer(t, func(string) string { return strings.Repeat("a", 100000) })
	res := tcpDo(t, protocol.Target{URL: addr, Body: []byte(`x\n`), Options: map[string]string{"until": "never", "max": "1000"}})
	if res.Success || !strings.Contains(res.Err.Error(), "max=1000") {
		t.Fatalf("expected a max error, got %+v", res)
	}
}

func TestTCP_SlowServerTimesOut(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err == nil {
			time.Sleep(2 * time.Second)
			c.Close()
		}
	}()
	d, _ := NewTCP(protocol.Target{URL: l.Addr().String(), Body: []byte("x"), Options: map[string]string{"read": "1"}}, 150*time.Millisecond)
	start := time.Now()
	res, _ := d.Do(context.Background())
	if res.Success {
		t.Fatal("a silent server must time out")
	}
	if time.Since(start) > time.Second {
		t.Errorf("took %v, the 150ms timeout was not applied", time.Since(start))
	}
}

func TestTCP_RunEndCutsCallShort(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err == nil {
			time.Sleep(3 * time.Second)
			c.Close()
		}
	}()
	d, _ := NewTCP(protocol.Target{URL: l.Addr().String(), Options: map[string]string{"read": "1"}}, 10*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	res, _ := d.Do(ctx)
	if res.Success || time.Since(start) > time.Second {
		t.Errorf("cancel should end the call quickly: success=%v after %v", res.Success, time.Since(start))
	}
}

func TestTCP_TLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hello")) }))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "https://")

	res := tcpDo(t, protocol.Target{
		URL: addr, Body: []byte(`GET / HTTP/1.0\r\n\r\n`), InsecureSkipVerify: true,
		Options: map[string]string{"tls": "true", "expect": "200 OK"},
	})
	if !res.Success {
		t.Fatalf("TLS probe failed: %v", res.Err)
	}

	// Without -insecure, the test server's certificate is not trusted.
	res = tcpDo(t, protocol.Target{URL: addr, Options: map[string]string{"tls": "true"}})
	if res.Success {
		t.Fatal("an untrusted certificate must fail without -insecure")
	}
}

func TestTCP_PlainTextToTLSPortFails(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	res := tcpDo(t, protocol.Target{
		URL: strings.TrimPrefix(srv.URL, "https://"), Body: []byte(`GET / HTTP/1.0\r\n\r\n`),
		Options: map[string]string{"expect": "200 OK"},
	})
	if res.Success {
		t.Fatal("plain text to a TLS port must not look like success")
	}
}

func TestNew_ConfigErrors(t *testing.T) {
	cases := []struct {
		name string
		tcp  bool
		tg   protocol.Target
		want string
	}{
		{"tcp wrong scheme", true, protocol.Target{URL: "http://h:1"}, "scheme"},
		{"tcp no port", true, protocol.Target{URL: "tcp://h"}, "port"},
		{"tcp unknown option", true, protocol.Target{URL: "h:1", Options: map[string]string{"raed": "1"}}, "unknown option raed"},
		{"tcp bad read", true, protocol.Target{URL: "h:1", Options: map[string]string{"read": "x"}}, "whole number"},
		{"tcp read and until", true, protocol.Target{URL: "h:1", Options: map[string]string{"read": "1", "until": "a"}}, "not both"},
		{"tcp read over max", true, protocol.Target{URL: "h:1", Options: map[string]string{"read": "10", "max": "5"}}, "more than max"},
		{"tcp bad escape", true, protocol.Target{URL: "h:1", Body: []byte(`\q`)}, "unknown escape"},
		{"tcp bad tls", true, protocol.Target{URL: "h:1", Options: map[string]string{"tls": "maybe"}}, "true or false"},
		{"udp no body", false, protocol.Target{URL: "h:1"}, "-body"},
		{"udp tcp-only option", false, protocol.Target{URL: "h:1", Body: []byte("x"), Options: map[string]string{"read": "1"}}, "unknown option read"},
		{"udp wrong scheme", false, protocol.Target{URL: "tcp://h:1", Body: []byte("x")}, "scheme"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var err error
			if c.tcp {
				_, err = NewTCP(c.tg, time.Second)
			} else {
				_, err = NewUDP(c.tg, time.Second)
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func udpEcho(t *testing.T, reply func([]byte) []byte) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if r := reply(buf[:n]); r != nil {
				_, _ = pc.WriteTo(r, from)
			}
		}
	}()
	return pc.LocalAddr().String()
}

func udpDo(t *testing.T, tg protocol.Target, timeout time.Duration) protocol.Result {
	t.Helper()
	d, err := NewUDP(tg, timeout)
	if err != nil {
		t.Fatalf("NewUDP: %v", err)
	}
	res, err := d.Do(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestUDP_FireAndForget(t *testing.T) {
	got := make(chan string, 1)
	addr := udpEcho(t, func(b []byte) []byte { got <- string(b); return nil })
	res := udpDo(t, protocol.Target{URL: "udp://" + addr, Body: []byte(`<13>hello\n`)}, time.Second)
	if !res.Success || res.BytesSent != 10 {
		t.Fatalf("send failed: %+v", res)
	}
	select {
	case m := <-got:
		if m != "<13>hello\n" {
			t.Errorf("server saw %q", m)
		}
	case <-time.After(time.Second):
		t.Fatal("server never saw the datagram")
	}
}

func TestUDP_ReplyAndExpect(t *testing.T) {
	addr := udpEcho(t, func(b []byte) []byte { return append([]byte("re:"), b...) })

	res := udpDo(t, protocol.Target{URL: addr, Body: []byte("ping"), Options: map[string]string{"reply": "true"}}, time.Second)
	if !res.Success || res.BytesReceived != 7 {
		t.Fatalf("reply failed: %+v", res)
	}
	res = udpDo(t, protocol.Target{URL: addr, Body: []byte("ping"), Options: map[string]string{"expect": "re:ping"}}, time.Second)
	if !res.Success {
		t.Fatalf("expect failed: %v", res.Err)
	}
	res = udpDo(t, protocol.Target{URL: addr, Body: []byte("ping"), Options: map[string]string{"expect": "nope"}}, time.Second)
	if res.Success {
		t.Fatal("wrong reply must fail")
	}
}

func TestUDP_NoReplyTimesOut(t *testing.T) {
	addr := udpEcho(t, func([]byte) []byte { return nil })
	start := time.Now()
	res := udpDo(t, protocol.Target{URL: addr, Body: []byte("x"), Options: map[string]string{"reply": "true"}}, 150*time.Millisecond)
	if res.Success {
		t.Fatal("no reply must fail when reply=true")
	}
	if time.Since(start) > time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

func TestUnescape(t *testing.T) {
	good := map[string]string{
		`plain`:    "plain",
		`a\nb`:     "a\nb",
		`\r\n`:     "\r\n",
		`\t\0`:     "\t\x00",
		`\\n`:      `\n`,
		`\x41\x7f`: "A\x7f",
		``:         "",
	}
	for in, want := range good {
		got, err := Unescape(in)
		if err != nil || string(got) != want {
			t.Errorf("Unescape(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{`\`, `\q`, `\x4`, `\xZZ`, `abc\x`} {
		if _, err := Unescape(bad); err == nil {
			t.Errorf("Unescape(%q): want an error", bad)
		}
	}
}

func TestEscapeFalseKeepsBackslashes(t *testing.T) {
	d, err := NewTCP(protocol.Target{URL: "h:1", Body: []byte(`a\nb`), Options: map[string]string{"escape": "false"}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if string(d.body) != `a\nb` {
		t.Errorf("body = %q", d.body)
	}
}

func TestName(t *testing.T) {
	tcp, _ := NewTCP(protocol.Target{URL: "h:1"}, time.Second)
	udp, _ := NewUDP(protocol.Target{URL: "h:1", Body: []byte("x")}, time.Second)
	if tcp.Name() != "tcp" || udp.Name() != "udp" {
		t.Errorf("names = %s, %s", tcp.Name(), udp.Name())
	}
	if tcp.Close() != nil || udp.Close() != nil {
		t.Error("Close should do nothing")
	}
}
