package js

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/inputs"
	"github.com/vegaload/vegaload/internal/protocol/kafka/kafkatest"
	"github.com/vegaload/vegaload/internal/protocol/mqtt/mqtttest"
	"github.com/vegaload/vegaload/internal/protocol/postgres/postgrestest"
	"github.com/vegaload/vegaload/internal/scripting/netapi"
)

// runScript runs body as one iteration and returns the error it ended with.
func runScript(t *testing.T, check netapi.SafetyCheck, body string, opts ...Option) error {
	t.Helper()
	path := writeScript(t, "s.js", "export default function () {\n"+body+"\n}\n")
	script, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	vu, err := script.NewVU(check, 3*time.Second, opts...)
	if err != nil {
		t.Fatalf("NewVU: %v", err)
	}
	defer vu.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return vu.Iteration(ctx)
}

// must fails the test if the script threw, which includes a failed assert.
func must(t *testing.T, check netapi.SafetyCheck, body string) {
	t.Helper()
	if err := runScript(t, check, body); err != nil {
		t.Fatalf("script failed: %v", err)
	}
}

const assertFn = `function assert(c, m) { if (!c) throw new Error("assert: " + m); }`

// tcpServer answers every connection by reading one line and sending back
// "echo:" plus that line.
func tcpServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				line, err := bufio.NewReader(c).ReadString('\n')
				if err == nil {
					_, _ = c.Write([]byte("echo:" + line))
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func TestTCP_SendReadsTheReply(t *testing.T) {
	addr := tcpServer(t)
	must(t, nil, assertFn+`
		const r = tcp.send("tcp://`+addr+`", {body: "hello\n", until: "\n"});
		assert(r.ok, "ok: " + r.error);
		assert(r.error === "", "error is empty");
		assert(r.body === "echo:hello\n", "body: " + JSON.stringify(r.body));
		assert(r.bytesSent === 6 && r.bytesReceived === 11, "bytes");
	`)
}

func TestTCP_BareHostPortAndBackslashIsNotAnEscape(t *testing.T) {
	addr := tcpServer(t)
	// "\\n" in the script is a backslash and an n. It must be sent as is.
	must(t, nil, assertFn+`
		const r = tcp.send("`+addr+`", {body: "a\\nb\n", until: "\n"});
		assert(r.ok, r.error);
		assert(r.body === "echo:a\\nb\n", JSON.stringify(r.body));
	`)
}

func TestTCP_FailureIsAReplyNotAnException(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close() // nothing listens here now
	must(t, nil, assertFn+`
		const r = tcp.send("tcp://`+addr+`", {body: "x\n"});
		assert(r.ok === false, "ok must be false");
		assert(r.error.length > 0, "error text");
	`)
}

func TestTCP_ExpectMismatchKeepsTheBytesRead(t *testing.T) {
	addr := tcpServer(t)
	must(t, nil, assertFn+`
		const r = tcp.send("tcp://`+addr+`", {body: "hi\n", until: "\n", expect: "nope"});
		assert(!r.ok, "must fail");
		assert(r.body === "echo:hi\n", "partial reply kept: " + JSON.stringify(r.body));
	`)
}

func TestTCP_TimeoutOptionAcceptsNumberAndText(t *testing.T) {
	// A server that accepts and never answers.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	for _, tv := range []string{`150`, `"150ms"`} {
		must(t, nil, assertFn+`
			const r = tcp.send("tcp://`+ln.Addr().String()+`", {body: "x", read: 1, timeout: `+tv+`});
			assert(!r.ok && r.error.indexOf("timeout") >= 0, r.error);
		`)
	}
}

func TestUDP_SendAndReply(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(append([]byte("pong:"), buf[:n]...), from)
		}
	}()
	must(t, nil, assertFn+`
		const r = udp.send("udp://`+pc.LocalAddr().String()+`", {body: "ping", reply: true});
		assert(r.ok, r.error);
		assert(r.body === "pong:ping", r.body);
	`)
}

func TestProto_ConfigMistakesThrow(t *testing.T) {
	cases := map[string]string{
		"unknown option":    `tcp.send("tcp://127.0.0.1:9", {bogus: 1})`,
		"read and until":    `tcp.send("tcp://127.0.0.1:9", {read: 1, until: "x"})`,
		"no url":            `tcp.send()`,
		"object option":     `tcp.send("tcp://127.0.0.1:9", {until: {a: 1}})`,
		"udp without body":  `udp.send("udp://127.0.0.1:9", {})`,
		"bad timeout":       `tcp.send("tcp://127.0.0.1:9", {timeout: "soon"})`,
		"mqtt no topic":     `mqtt.publish("mqtt://127.0.0.1:9", {body: "x"})`,
		"mqtt mode option":  `mqtt.publish("mqtt://127.0.0.1:9", {topic: "t", mode: "subscribe"})`,
		"mqtt wrong scheme": `mqtt.publish("http://127.0.0.1:9", {topic: "t"})`,
		"roundtrip no {id}": `mqtt.roundtrip("mqtt://127.0.0.1:9", {topic: "t", body: "x"})`,
		"password no user":  `mqtt.publish("mqtt://127.0.0.1:9", {topic: "t", password: "p"})`,
		"password_env too":  `mqtt.publish("mqtt://127.0.0.1:9", {topic: "t", username: "u", password: "p", password_env: "X"})`,
	}
	for name, call := range cases {
		err := runScript(t, nil, call)
		if err == nil {
			t.Errorf("%s: want a thrown error, got none", name)
		}
	}
}

func TestProto_SafetyCheckRunsPerCall(t *testing.T) {
	var mu sync.Mutex
	var hosts []string
	check := func(h string) error {
		mu.Lock()
		hosts = append(hosts, h)
		mu.Unlock()
		return errors.New("refused " + h)
	}
	for _, call := range []string{
		`tcp.send("tcp://example.invalid:80", {body: "x"})`,
		`udp.send("udp://example.invalid:80", {body: "x"})`,
		`mqtt.publish("mqtt://example.invalid", {topic: "t"})`,
		`mqtt.subscribe("mqtts://example.invalid", {topic: "t"})`,
		`mqtt.roundtrip("mqtt://example.invalid", {topic: "t", body: "{id}"})`,
		`tcp.send("example.invalid:80", {body: "x"})`,
	} {
		err := runScript(t, check, call)
		if err == nil || !strings.Contains(err.Error(), "refused example.invalid") {
			t.Errorf("%s: want the safety refusal, got %v", call, err)
		}
	}
	if len(hosts) != 6 {
		t.Errorf("safety check ran %d times, want 6: %v", len(hosts), hosts)
	}
}

func TestProto_OutsideAnIterationThrows(t *testing.T) {
	path := writeScript(t, "s.js", `tcp.send("tcp://127.0.0.1:9", {}); export default function () {}`)
	script, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := script.NewVU(nil, time.Second); err == nil || !strings.Contains(err.Error(), "outside of an iteration") {
		t.Fatalf("want an 'outside of an iteration' error, got %v", err)
	}
}

func TestMQTT_Publish(t *testing.T) {
	b := mqtttest.Start(t)
	must(t, nil, assertFn+`
		const r = mqtt.publish("`+b.URL()+`", {topic: "t/1", body: "hello", qos: 1});
		assert(r.ok, r.error);
		assert(r.messages.length === 0, "publish reads nothing");
	`)
	if got := b.Published(); len(got) != 1 || got[0] != "t/1=hello" {
		t.Fatalf("broker saw %v", got)
	}
}

func TestMQTT_RoundtripReturnsTheMessage(t *testing.T) {
	b := mqtttest.Start(t)
	must(t, nil, assertFn+`
		const r = mqtt.roundtrip("`+b.URL()+`", {topic: "rt/{id}", body: "order {id}"});
		assert(r.ok, r.error);
		assert(r.messages.length === 1, "one message");
		assert(r.messages[0].body.indexOf("order vegaload-") === 0, r.messages[0].body);
		assert(r.messages[0].topic.indexOf("rt/vegaload-") === 0, r.messages[0].topic);
	`)
}

func TestMQTT_SubscribeAfterAPublishInTheSameIteration(t *testing.T) {
	// Subscribe waits for a message while another call publishes it: the
	// script cannot do both at once, so publish from outside.
	b := mqtttest.Start(t)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		pc := netapi.NewProtoClient(nil, 2*time.Second)
		for {
			select {
			case <-stop:
				return
			case <-time.After(60 * time.Millisecond):
			}
			_, _ = pc.MQTT(context.Background(), "publish", netapi.ProtoCall{
				URL: b.URL(), Body: []byte("tick"), Options: map[string]string{"topic": "feed"},
			})
		}
	}()
	defer func() { close(stop); wg.Wait() }()
	must(t, nil, assertFn+`
		const r = mqtt.subscribe("`+b.URL()+`", {topic: "feed", count: 2, expect: "tick", timeout: "5s"});
		assert(r.ok, r.error);
		assert(r.messages.length === 2, "two messages: " + r.messages.length);
		assert(r.messages[1].body === "tick" && r.messages[1].topic === "feed", "content");
	`)
}

func TestMQTT_PasswordFromEnvReachesTheBroker(t *testing.T) {
	b := mqtttest.Start(t)
	b.User, b.Pass = "alice", "s3cret"
	in := inputs.New(map[string]string{"MQ_PW": "s3cret"}, nil)
	if err := runScript(t, nil, assertFn+`
		const ok = mqtt.publish("`+b.URL()+`", {topic: "a", body: "x", username: "alice", password: env.MQ_PW});
		assert(ok.ok, ok.error);
		const bad = mqtt.publish("`+b.URL()+`", {topic: "a", body: "x", username: "alice", password: "wrong"});
		assert(!bad.ok && bad.error.length > 0, "wrong password must fail");
	`, WithInputs(in)); err != nil {
		t.Fatal(err)
	}
}

func TestMQTT_RefusedConnectionIsAReply(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	must(t, nil, assertFn+`
		const r = mqtt.publish("mqtt://`+addr+`", {topic: "t", body: "x"});
		assert(!r.ok && r.error.length > 0, "must fail with text");
	`)
}

func TestProto_EndOfRunStopsACall(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	path := writeScript(t, "s.js", `export default function () {
		tcp.send("tcp://`+ln.Addr().String()+`", {body: "x", read: 1, timeout: "30s"});
	}`)
	script, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	vu, err := script.NewVU(nil, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer vu.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_ = vu.Iteration(ctx)
	if time.Since(start) > 5*time.Second {
		t.Fatalf("call kept running after the run ended: %s", time.Since(start))
	}
}

func TestTCP_InsecureSkipsCertificateCheck(t *testing.T) {
	// A TLS server with a certificate no client trusts.
	hs := httptest.NewUnstartedServer(nil)
	hs.StartTLS()
	cert := hs.TLS.Certificates[0]
	hs.Close()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				line, err := bufio.NewReader(c).ReadString('\n')
				if err == nil {
					_, _ = c.Write([]byte("echo:" + line))
				}
			}()
		}
	}()
	addr := ln.Addr().String()
	must(t, nil, assertFn+`
		const strict = tcp.send("tcp://`+addr+`", {body: "hi\n", until: "\n", tls: true});
		assert(!strict.ok, "an untrusted certificate must fail without insecure");
		const r = tcp.send("tcp://`+addr+`", {body: "hi\n", until: "\n", tls: true, insecure: true});
		assert(r.ok, r.error);
		assert(r.body === "echo:hi\n", r.body);
	`)
}

func TestKafka_ProduceConsumeRoundtripAdmin(t *testing.T) {
	b := kafkatest.Start(t)
	b.AddTopic("orders", 2)
	must(t, nil, assertFn+`
		const url = "`+b.URL()+`";
		const p = kafka.produce(url, {topic: "orders", key: "k1", value: "hello"});
		assert(p.ok, p.error);
		assert(p.records.length === 1, "one record");
		const rec = p.records[0];
		assert(rec.topic === "orders" && rec.key === "k1" && rec.value === "hello", JSON.stringify(rec));
		assert(rec.offset >= 0 && rec.partition >= 0, "offset and partition are known");

		const c = kafka.consume(url, {topic: "orders", expect: "hell"});
		assert(c.ok, c.error);
		assert(c.records.length === 1 && c.records[0].value === "hello", JSON.stringify(c.records));

		const r = kafka.roundtrip(url, {topic: "orders", value: "ping {id}", count: 2});
		assert(r.ok, r.error);
		assert(r.records.length === 2 && r.records[0].value.indexOf("ping vegaload-") === 0, JSON.stringify(r.records));

		const a = kafka.admin(url, {action: "list_topics"});
		assert(a.ok && a.text.indexOf("orders") >= 0, a.error + a.text);
		assert(a.records.length === 0, "admin has no records");
	`)
}

func TestKafka_BodyIsAnAliasOfValue(t *testing.T) {
	b := kafkatest.Start(t)
	b.AddTopic("t", 1)
	must(t, nil, assertFn+`
		const p = kafka.produce("`+b.URL()+`", {topic: "t", body: "via body"});
		assert(p.ok && p.records[0].value === "via body", p.error);
	`)
	if err := runScript(t, nil, `kafka.produce("kafka://127.0.0.1:9", {topic: "t", body: "a", value: "b"})`); err == nil {
		t.Error("body and value together must throw")
	}
	if err := runScript(t, nil, `mqtt.publish("mqtt://127.0.0.1:9", {topic: "t", value: "b"})`); err == nil {
		t.Error("value is only for kafka")
	}
}

func TestKafka_PasswordFromEnv(t *testing.T) {
	b := kafkatest.Start(t)
	b.User, b.Pass = "alice", "s3cret"
	b.AddTopic("t", 1)
	in := inputs.New(map[string]string{"KPW": "s3cret"}, nil)
	if err := runScript(t, nil, assertFn+`
		const ok = kafka.produce("`+b.URL()+`", {topic: "t", value: "x", sasl: "plain", username: "alice", password: env.KPW});
		assert(ok.ok, ok.error);
		const bad = kafka.produce("`+b.URL()+`", {topic: "t", value: "x", sasl: "plain", username: "alice", password: "wrong", timeout: 1500});
		assert(!bad.ok && bad.error.length > 0, "wrong password must fail");
	`, WithInputs(in)); err != nil {
		t.Fatal(err)
	}
}

func TestKafka_ConfigMistakesThrow(t *testing.T) {
	cases := map[string]string{
		"no topic":         `kafka.produce("kafka://127.0.0.1:9", {value: "x"})`,
		"unknown option":   `kafka.produce("kafka://127.0.0.1:9", {topic: "t", bogus: 1})`,
		"mode option":      `kafka.produce("kafka://127.0.0.1:9", {topic: "t", mode: "consume"})`,
		"password_env":     `kafka.produce("kafka://127.0.0.1:9", {topic: "t", sasl: "plain", username: "u", password_env: "X"})`,
		"password no sasl": `kafka.produce("kafka://127.0.0.1:9", {topic: "t", password: "p"})`,
		"admin no action":  `kafka.admin("kafka://127.0.0.1:9", {})`,
		"wrong scheme":     `kafka.produce("http://127.0.0.1:9", {topic: "t"})`,
	}
	for name, call := range cases {
		if err := runScript(t, nil, call); err == nil {
			t.Errorf("%s: want a thrown error", name)
		}
	}
}

func TestKafka_SafetyCheckAndFailure(t *testing.T) {
	check := func(h string) error { return errors.New("refused " + h) }
	err := runScript(t, check, `kafka.produce("kafka://example.invalid:9092", {topic: "t"})`)
	if err == nil || !strings.Contains(err.Error(), "refused example.invalid") {
		t.Fatalf("want the safety refusal, got %v", err)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	must(t, nil, assertFn+`
		const r = kafka.produce("kafka://`+addr+`", {topic: "t", value: "x", timeout: 1500});
		assert(!r.ok && r.error.length > 0, "refused broker is a reply");
		assert(r.records.length === 0, "no records");
	`)
}

func TestMQTT_MessagesIsARealArray(t *testing.T) {
	b := mqtttest.Start(t)
	must(t, nil, assertFn+`
		const r = mqtt.roundtrip("`+b.URL()+`", {topic: "rt/{id}", body: "x {id}"});
		assert(Array.isArray(r.messages), "messages is an array");
		assert(r.messages.map((m) => m.topic).length === 1, "map works");
		assert(r.messages.length === 1 && JSON.stringify(r.messages[0]).indexOf("topic") > 0, "plain objects");
	`)
}

func pgRows(string) []postgrestest.Result {
	return []postgrestest.Result{{
		Columns: []postgrestest.Column{postgrestest.Int("id"), postgrestest.Text("name")},
		Rows:    [][]any{{"1", "ann"}, {"2", nil}},
	}}
}

func TestPostgres_QueryReturnsRows(t *testing.T) {
	s := postgrestest.Start(t, pgRows)
	must(t, nil, assertFn+`
		const r = postgres.query("`+s.URL()+`", {sslmode: "disable", username: "app", body: "select id, name from t where id > $1", args: [0]});
		assert(r.ok, r.error);
		assert(r.rowCount === 2 && r.rows.length === 2, "rows");
		assert(r.rows[0].id === 1 && r.rows[0].name === "ann", JSON.stringify(r.rows[0]));
		assert(r.rows[1].name === null, "NULL is null");
		assert(r.columns.join() === "id,name", "columns");
		assert(r.commandTag === "SELECT 2", r.commandTag);
	`)
	if q := s.Queries(); len(q) != 1 || !strings.Contains(q[0], "'0'") {
		t.Errorf("queries = %q", q)
	}
}

func TestPostgres_OneConnectionForManyCalls(t *testing.T) {
	s := postgrestest.Start(t, pgRows)
	must(t, nil, assertFn+`
		for (let i = 0; i < 4; i++) {
			const r = postgres.query("`+s.URL()+`", {sslmode: "disable", body: "select 1"});
			assert(r.ok, r.error);
		}
	`)
	if n := s.ConnCount(); n != 1 {
		t.Errorf("%d connections for 4 calls, want 1", n)
	}
}

func TestPostgres_FailureIsAReplyNotAnException(t *testing.T) {
	s := postgrestest.Start(t, func(string) []postgrestest.Result {
		return []postgrestest.Result{{ErrCode: "42P01", ErrMessage: "no such table"}}
	})
	must(t, nil, assertFn+`
		const r = postgres.query("`+s.URL()+`", {sslmode: "disable", body: "select * from nosuch"});
		assert(r.ok === false, "ok is false");
		assert(r.error.indexOf("42P01") >= 0, r.error);
		assert(r.rows.length === 0, "no rows");
	`)
}

func TestPostgres_SetupMistakesThrow(t *testing.T) {
	for name, body := range map[string]string{
		"no sql":         `postgres.query("postgres://127.0.0.1:9/db", {sslmode: "disable"});`,
		"unknown option": `postgres.query("postgres://127.0.0.1:9/db", {body: "select 1", bogus: 1});`,
		"args not list":  `postgres.query("postgres://127.0.0.1:9/db", {body: "select 1", args: 5});`,
		"password_env":   `postgres.query("postgres://127.0.0.1:9/db", {body: "select 1", password_env: "X"});`,
	} {
		if err := runScript(t, nil, body); err == nil {
			t.Errorf("%s: want the script to throw", name)
		}
	}
}

func TestPostgres_PasswordFromEnv(t *testing.T) {
	s := postgrestest.StartWithPassword(t, pgRows, "from-env")
	path := writeScript(t, "s.js", "export default function () {\n"+assertFn+`
		const r = postgres.query("`+s.URL()+`", {sslmode: "disable", username: "app", password: env.DB_PASSWORD, body: "select 1"});
		assert(r.ok, r.error);
	}
	`)
	script, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	in := inputs.New(map[string]string{"DB_PASSWORD": "from-env"}, nil)
	vu, err := script.NewVU(nil, 3*time.Second, WithInputs(in))
	if err != nil {
		t.Fatal(err)
	}
	defer vu.Close()
	if err := vu.Iteration(context.Background()); err != nil {
		t.Fatalf("script failed: %v", err)
	}
}

func TestPostgres_SafetyCheckRefusesTheHost(t *testing.T) {
	check := func(h string) error {
		if h == "db.example.com" {
			return errors.New("host not allowed")
		}
		return nil
	}
	if err := runScript(t, check, `postgres.query("postgres://db.example.com/app", {body: "select 1"});`); err == nil || !strings.Contains(err.Error(), "host not allowed") {
		t.Errorf("err = %v", err)
	}
}
