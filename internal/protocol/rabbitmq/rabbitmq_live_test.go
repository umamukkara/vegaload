package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
)

func liveBase(t *testing.T) (string, map[string]string) {
	t.Helper()
	raw := os.Getenv("VEGALOAD_TEST_RABBITMQ")
	if raw == "" {
		t.Skip("VEGALOAD_TEST_RABBITMQ is not set")
	}
	opts := map[string]string{}
	if user := os.Getenv("VEGALOAD_TEST_RABBITMQ_USER"); user != "" {
		opts["username"] = user
	}
	if name := os.Getenv("VEGALOAD_TEST_RABBITMQ_PASSWORD_ENV"); name != "" {
		opts["password_env"] = name
	}
	return raw, opts
}

func liveCopy(in map[string]string, extra ...string) map[string]string {
	out := make(map[string]string, len(in)+len(extra)/2)
	for k, v := range in {
		out[k] = v
	}
	for i := 0; i+1 < len(extra); i += 2 {
		out[extra[i]] = extra[i+1]
	}
	return out
}

func liveRun(t *testing.T, url, body string, opts map[string]string, timeout time.Duration) (protocol.Result, Reply) {
	t.Helper()
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	d, err := New(protocol.Target{URL: url, Body: []byte(body), Options: opts}, timeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d.Run(context.Background())
}

func liveQueue(t *testing.T, url string, base map[string]string, name string, extra ...string) {
	t.Helper()
	opts := liveCopy(base, "mode", "admin", "action", "queue_declare", "queue", name, "allow_writes", "true", "durable", "false")
	for i := 0; i+1 < len(extra); i += 2 {
		opts[extra[i]] = extra[i+1]
	}
	res, rep := liveRun(t, url, "", opts, 0)
	if !res.Success {
		t.Fatalf("declare %s: %v %s", name, res.Err, rep.Text)
	}
	t.Cleanup(func() {
		del := liveCopy(base, "mode", "admin", "action", "queue_delete", "queue", name, "allow_writes", "true", "allow_admin", "true")
		d, err := New(protocol.Target{URL: url, Options: del}, 5*time.Second)
		if err != nil {
			t.Errorf("cleanup %s: %v", name, err)
			return
		}
		defer d.Close()
		res, _ := d.Run(context.Background())
		if !res.Success {
			t.Errorf("cleanup %s: %v", name, res.Err)
		}
	})
}

func TestLive_PublishConsumeRoundtrip(t *testing.T) {
	url, base := liveBase(t)
	q := "vegaload.test.pub." + fmt.Sprint(time.Now().UnixNano())
	liveQueue(t, url, base, q)

	res, _ := liveRun(t, url, "hello", liveCopy(base, "routing_key", q), 0)
	if !res.Success || res.BytesSent != 5 {
		t.Fatalf("publish %+v", res.Err)
	}
	res, rep := liveRun(t, url, "", liveCopy(base, "mode", "consume", "queue", q, "expect", "hello"), 0)
	if !res.Success || len(rep.Messages) != 1 || rep.Messages[0].Body != "hello" || rep.Messages[0].Redelivered {
		t.Fatalf("peek %+v %+v", res.Err, rep.Messages)
	}
	res, rep = liveRun(t, url, "", liveCopy(base, "mode", "consume", "queue", q), 0)
	if !res.Success || len(rep.Messages) != 1 || !rep.Messages[0].Redelivered {
		t.Fatalf("requeue %+v %+v", res.Err, rep.Messages)
	}
	res, rep = liveRun(t, url, "", liveCopy(base, "mode", "consume", "queue", q, "ack", "ack", "allow_writes", "true"), 0)
	if !res.Success || len(rep.Messages) != 1 {
		t.Fatalf("ack %+v %+v", res.Err, rep.Messages)
	}
	res, rep = liveRun(t, url, "", liveCopy(base, "mode", "admin", "action", "queue_info", "queue", q), 0)
	if !res.Success || !strings.Contains(rep.Text, "messages=0") {
		t.Fatalf("info %+v %s", res.Err, rep.Text)
	}

	for _, persistent := range []string{"false", "true"} {
		res, rep = liveRun(t, url, "rt", liveCopy(base,
			"mode", "roundtrip", "exchange", "amq.topic",
			"routing_key", "vegaload.test.{id}", "persistent", persistent,
		), 0)
		if !res.Success || len(rep.Messages) != 1 || rep.Messages[0].Body != "rt" {
			t.Fatalf("persistent %s: %+v %+v", persistent, res.Err, rep.Messages)
		}
	}

	res, _ = liveRun(t, url, "props", liveCopy(base,
		"routing_key", q, "content_type", "text/plain", "priority", "1",
		"expiration", "30s", "headers", `{"a":"b","n":1}`, "persistent", "true",
	), 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	res, _ = liveRun(t, url, "", liveCopy(base, "mode", "consume", "queue", q, "ack", "ack", "allow_writes", "true"), 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
}

func TestLive_BodiesAndRouting(t *testing.T) {
	url, base := liveBase(t)
	q := "vegaload.test.body." + fmt.Sprint(time.Now().UnixNano())
	liveQueue(t, url, base, q)
	for _, n := range []int{300 * 1024, 5 * 1024 * 1024} {
		body := strings.Repeat("a", n)
		res, _ := liveRun(t, url, body, liveCopy(base, "routing_key", q), 30*time.Second)
		if !res.Success || res.BytesSent != int64(n) {
			t.Fatalf("%d bytes: %v sent %d", n, res.Err, res.BytesSent)
		}
		res, _ = liveRun(t, url, "", liveCopy(base, "mode", "consume", "queue", q, "ack", "ack", "allow_writes", "true"), 30*time.Second)
		if !res.Success || res.BytesReceived != int64(n) {
			t.Fatalf("consume %d: %v got %d", n, res.Err, res.BytesReceived)
		}
	}
	start := time.Now()
	res, _ := liveRun(t, url, "x", liveCopy(base, "exchange", "amq.topic", "routing_key", "vegaload.test.no.match"), 2*time.Second)
	if res.Success || !strings.Contains(res.Err.Error(), "not routed") || time.Since(start) > time.Second {
		t.Fatalf("not routed %v after %s", res.Err, time.Since(start))
	}
	res, _ = liveRun(t, url, "x", liveCopy(base, "exchange", "vegaload.test.missing.exchange", "routing_key", "k"), 0)
	if res.Success || !strings.Contains(res.Err.Error(), "NOT_FOUND") {
		t.Fatal(res.Err)
	}
}

func TestLive_MissingQueueTimeoutAndFlags(t *testing.T) {
	url, base := liveBase(t)
	missing := "vegaload.test.missing." + fmt.Sprint(time.Now().UnixNano())
	d, err := New(protocol.Target{URL: url, Options: liveCopy(base, "mode", "consume", "queue", missing, "allow_writes", "true", "allow_admin", "true")}, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	res, _ := d.Run(context.Background())
	if res.Success || !strings.Contains(res.Err.Error(), "NOT_FOUND") {
		t.Fatal(res.Err)
	}
	declared, err := d.Call(liveCopy(base, "mode", "admin", "action", "queue_declare", "queue", missing, "allow_writes", "true", "durable", "false"), nil, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res, _ = declared.Run(context.Background()); !res.Success {
		t.Fatal(res.Err)
	}
	t.Cleanup(func() {
		del := liveCopy(base, "mode", "admin", "action", "queue_delete", "queue", missing, "allow_writes", "true", "allow_admin", "true")
		c, err := d.Call(del, nil, 5*time.Second)
		if err != nil {
			t.Errorf("cleanup: %v", err)
			return
		}
		if res, _ := c.Run(context.Background()); !res.Success {
			t.Errorf("cleanup %s: %v", missing, res.Err)
		}
	})

	empty := "vegaload.test.empty." + fmt.Sprint(time.Now().UnixNano())
	liveQueue(t, url, base, empty)
	start := time.Now()
	res, _ = liveRun(t, url, "", liveCopy(base, "mode", "consume", "queue", empty), 500*time.Millisecond)
	if res.Success || !strings.Contains(res.Err.Error(), "timed out") || time.Since(start) > 2*time.Second {
		t.Fatalf("empty %v after %s", res.Err, time.Since(start))
	}
	res, _ = liveRun(t, url, "", liveCopy(base, "mode", "admin", "action", "queue_info", "queue", empty), 0)
	if !res.Success {
		t.Fatal(res.Err)
	}

	res, _ = liveRun(t, url, "", liveCopy(base, "mode", "admin", "action", "queue_delete", "queue", empty), 0)
	if res.Success || !strings.Contains(res.Err.Error(), "allow_admin") {
		t.Fatal(res.Err)
	}
	res, _ = liveRun(t, url, "", liveCopy(base, "mode", "admin", "action", "queue_purge", "queue", empty, "allow_writes", "true"), 0)
	if res.Success || !strings.Contains(res.Err.Error(), "allow_admin") {
		t.Fatal(res.Err)
	}

	life := "vegaload.test.life.{id}"
	res, rep := liveRun(t, url, "", liveCopy(base, "mode", "admin", "action", "queue_lifecycle", "queue", life, "allow_writes", "true", "durable", "false"), 0)
	if !res.Success || !strings.Contains(rep.Text, "vegaload.test.life.") {
		t.Fatalf("lifecycle %v %s", res.Err, rep.Text)
	}
}

func TestLive_LoginPerCallQuorumAndLoad(t *testing.T) {
	url, base := liveBase(t)
	secret := "wrong-secret-value"
	user := base["username"]
	if user == "" {
		user = "guest"
	}
	opts := liveCopy(base, "username", user, "mandatory", "false")
	delete(opts, "password_env")
	bad, err := NewConn(protocol.Target{URL: url, Options: opts}, 8*time.Second, &secret)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bad.Close() })
	call, err := bad.Call(map[string]string{"mandatory": "false"}, []byte("x"), 8*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, err := call.Do(context.Background())
	if err != nil || res.Success || !strings.Contains(res.Err.Error(), "refused the login") || strings.Contains(res.Err.Error(), secret) {
		t.Fatalf("err %v res %v", err, res.Err)
	}

	q := "vegaload.test.percall." + fmt.Sprint(time.Now().UnixNano())
	liveQueue(t, url, base, q)
	for i := 0; i < 2; i++ {
		res, _ = liveRun(t, url, "x", liveCopy(base, "routing_key", q, "connection", "per_call"), 0)
		if !res.Success {
			t.Fatal(res.Err)
		}
	}

	quorum := "vegaload.test.quorum." + fmt.Sprint(time.Now().UnixNano())
	liveQueue(t, url, base, quorum, "queue_type", "quorum", "durable", "true")
	res, _ = liveRun(t, url, "q", liveCopy(base, "routing_key", quorum), 0)
	if !res.Success {
		t.Fatal(res.Err)
	}

	stream := "vegaload.test.stream." + fmt.Sprint(time.Now().UnixNano())
	liveQueue(t, url, base, stream, "queue_type", "stream", "durable", "true")

	parent, err := NewConn(protocol.Target{URL: url, Options: liveCopy(base)}, 5*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	before := map[string]bool{}
	if mgmt := os.Getenv("VEGALOAD_TEST_RABBITMQ_MGMT"); mgmt != "" {
		for _, name := range liveQueues(t, mgmt, base) {
			before[name] = true
		}
	}
	for i := 0; i < 100; i++ {
		c, err := parent.Call(map[string]string{
			"mode": "roundtrip", "exchange": "amq.topic", "routing_key": "vegaload.test.{id}",
		}, []byte("n"), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if res, _ = c.Run(context.Background()); !res.Success {
			t.Fatalf("roundtrip %d: %v", i, res.Err)
		}
	}
	if mgmt := os.Getenv("VEGALOAD_TEST_RABBITMQ_MGMT"); mgmt != "" {
		for _, name := range liveQueues(t, mgmt, base) {
			if !before[name] && strings.HasPrefix(name, "amq.gen-") {
				t.Errorf("leftover queue %s", name)
			}
		}
	}

	batch := "vegaload.test.batch." + fmt.Sprint(time.Now().UnixNano())
	liveQueue(t, url, base, batch)
	for i := 0; i < 10; i++ {
		res, _ = liveRun(t, url, "m", liveCopy(base, "routing_key", batch, "count", "100"), 15*time.Second)
		if !res.Success {
			t.Fatalf("batch %d: %v", i, res.Err)
		}
	}
	res, rep := liveRun(t, url, "", liveCopy(base, "mode", "admin", "action", "queue_info", "queue", batch), 0)
	if !res.Success || !strings.Contains(rep.Text, "messages=1000") {
		t.Fatalf("batch depth %v %s", res.Err, rep.Text)
	}

	d, err := New(protocol.Target{URL: url, Body: []byte("m"), Options: liveCopy(base, "routing_key", batch, "channels", "1", "mandatory", "false")}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var wg sync.WaitGroup
	errc := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, _ := d.Run(context.Background())
			if res.Err != nil {
				errc <- res.Err
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("16 callers on channels=1 did not finish")
	}
	close(errc)
	for err := range errc {
		t.Fatal(err)
	}
}

func liveQueues(t *testing.T, mgmt string, base map[string]string) []string {
	t.Helper()
	user := base["username"]
	if user == "" {
		user = "guest"
	}
	pass := "guest"
	if name := base["password_env"]; name != "" {
		pass = os.Getenv(name)
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(mgmt, "/")+"/api/queues/%2F", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(user, pass)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("management api status %d", resp.StatusCode)
	}
	var rows []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(rows))
	for i, row := range rows {
		names[i] = row.Name
	}
	return names
}
