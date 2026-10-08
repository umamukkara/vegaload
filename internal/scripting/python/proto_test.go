package python

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/inputs"
	"github.com/vegaload/vegaload/internal/protocol/kafka/kafkatest"
	"github.com/vegaload/vegaload/internal/protocol/mqtt/mqtttest"
	"github.com/vegaload/vegaload/internal/protocol/postgres/postgrestest"
	"github.com/vegaload/vegaload/internal/scripting/netapi"
)

// runIteration runs body as iteration() once and returns its error.
func runIteration(t *testing.T, check netapi.SafetyCheck, body string, opts ...Option) error {
	t.Helper()
	skipIfNoPython(t)
	src := "def iteration():\n"
	for _, l := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		src += "    " + l + "\n"
	}
	script, err := Load(writeScript(t, src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	vu, err := script.NewVU(check, 3*time.Second, opts...)
	if err != nil {
		t.Fatalf("NewVU: %v", err)
	}
	defer vu.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return vu.Iteration(ctx)
}

func must(t *testing.T, check netapi.SafetyCheck, body string, opts ...Option) {
	t.Helper()
	if err := runIteration(t, check, body, opts...); err != nil {
		t.Fatalf("script failed: %v", err)
	}
}

// tcpServer answers a line with "echo:" and that line.
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
				if line, err := bufio.NewReader(c).ReadString('\n'); err == nil {
					_, _ = c.Write([]byte("echo:" + line))
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func TestTCP_SendReadsTheReply(t *testing.T) {
	addr := tcpServer(t)
	must(t, nil, `
r = tcp.send("tcp://`+addr+`", body="hello\n", until="\n")
assert r.ok, r.error
assert r["ok"] is True
assert r.error == ""
assert r.body == "echo:hello\n", repr(r.body)
assert r.bytesSent == 6 and r.bytesReceived == 11
`)
}

func TestTCP_FailureIsAReplyNotAnException(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	must(t, nil, `
r = tcp.send("tcp://`+addr+`", body="x\n")
assert r.ok is False
assert len(r.error) > 0
`)
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
	must(t, nil, `
r = udp.send("udp://`+pc.LocalAddr().String()+`", body="ping", reply=True)
assert r.ok, r.error
assert r.body == "pong:ping"
`)
}

func TestProto_ConfigMistakesRaise(t *testing.T) {
	cases := map[string]string{
		"unknown option":  `tcp.send("tcp://127.0.0.1:9", bogus=1)`,
		"read and until":  `tcp.send("tcp://127.0.0.1:9", read=1, until="x")`,
		"object option":   `tcp.send("tcp://127.0.0.1:9", until={"a": 1})`,
		"bad timeout":     `tcp.send("tcp://127.0.0.1:9", timeout="soon")`,
		"mqtt no topic":   `mqtt.publish("mqtt://127.0.0.1:9", body="x")`,
		"mqtt mode":       `mqtt.publish("mqtt://127.0.0.1:9", topic="t", mode="subscribe")`,
		"kafka no topic":  `kafka.produce("kafka://127.0.0.1:9", value="x")`,
		"kafka both":      `kafka.produce("kafka://127.0.0.1:9", topic="t", body="a", value="b")`,
		"value not kafka": `mqtt.publish("mqtt://127.0.0.1:9", topic="t", value="b")`,
		"missing url":     `tcp.send()`,
	}
	for name, call := range cases {
		if err := runIteration(t, nil, call); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestProto_SafetyCheckRunsPerCall(t *testing.T) {
	check := func(h string) error { return errors.New("refused " + h) }
	for _, call := range []string{
		`tcp.send("tcp://example.invalid:80", body="x")`,
		`udp.send("udp://example.invalid:80", body="x")`,
		`mqtt.publish("mqtt://example.invalid", topic="t")`,
		`kafka.produce("kafka://example.invalid", topic="t")`,
	} {
		err := runIteration(t, check, call)
		if err == nil || !strings.Contains(err.Error(), "refused example.invalid") {
			t.Errorf("%s: want the safety refusal, got %v", call, err)
		}
	}
}

func TestProto_NotAvailableWhileLoading(t *testing.T) {
	skipIfNoPython(t)
	script, err := Load(writeScript(t, "tcp.send('tcp://127.0.0.1:9', body='x')\ndef iteration():\n    pass\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := script.NewVU(nil, time.Second); err == nil || !strings.Contains(err.Error(), "not available while the script is loading") {
		t.Fatalf("want a loading error, got %v", err)
	}
}

func TestMQTT_PublishRoundtripAndPassword(t *testing.T) {
	b := mqtttest.Start(t)
	b.User, b.Pass = "alice", "s3cret"
	in := inputs.New(map[string]string{"MQ_PW": "s3cret"}, nil)
	must(t, nil, `
auth = dict(username="alice", password=env.MQ_PW)
p = mqtt.publish("`+b.URL()+`", topic="t/1", body="hello", qos=1, **auth)
assert p.ok, p.error
assert p.messages == []
rt = mqtt.roundtrip("`+b.URL()+`", topic="rt/{id}", body="order {id}", **auth)
assert rt.ok, rt.error
assert len(rt.messages) == 1
assert rt.messages[0].body.startswith("order vegaload-"), rt.messages[0].body
assert rt.messages[0]["topic"].startswith("rt/vegaload-")
bad = mqtt.publish("`+b.URL()+`", topic="t", body="x", username="alice", password="wrong")
assert not bad.ok and len(bad.error) > 0
`, WithInputs(in))
	if got := b.Published(); len(got) < 1 || got[0] != "t/1=hello" {
		t.Fatalf("broker saw %v", got)
	}
}

func TestKafka_ProduceConsumeRoundtripAdmin(t *testing.T) {
	b := kafkatest.Start(t)
	b.AddTopic("orders", 2)
	must(t, nil, `
url = "`+b.URL()+`"
p = kafka.produce(url, topic="orders", key="k1", value="hello")
assert p.ok, p.error
rec = p.records[0]
assert rec.topic == "orders" and rec.key == "k1" and rec.value == "hello", rec
assert rec.offset >= 0 and rec.partition >= 0
c = kafka.consume(url, topic="orders", expect="hell")
assert c.ok, c.error
assert len(c.records) == 1 and c.records[0].value == "hello"
r = kafka.roundtrip(url, topic="orders", value="ping {id}", count=2)
assert r.ok, r.error
assert len(r.records) == 2 and r.records[0].value.startswith("ping vegaload-")
a = kafka.admin(url, action="list_topics")
assert a.ok and "orders" in a.text, a.error
assert a.records == []
`)
}

func TestKafka_ReservedWordGetsATrailingUnderscore(t *testing.T) {
	b := kafkatest.Start(t)
	b.AddTopic("t", 1)
	must(t, nil, `
assert kafka.produce("`+b.URL()+`", topic="t", value="old").ok
r = kafka.consume("`+b.URL()+`", topic="t", from_="end", timeout=700)
assert not r.ok, "from end sees nothing old, so it must time out"
r2 = kafka.consume("`+b.URL()+`", topic="t", from_="start")
assert r2.ok and r2.records[0].value == "old", r2.error
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
	must(t, nil, `
r = postgres.query("`+s.URL()+`", sslmode="disable", username="app", body="select id, name from t where id > $1", args=[0])
assert r.ok, r.error
assert r.rowCount == 2 and len(r.rows) == 2, r
assert r.rows[0].id == 1 and r.rows[0].name == "ann", r.rows[0]
assert r.rows[1]["name"] is None
assert r.columns == ["id", "name"], r.columns
assert r.commandTag == "SELECT 2", r.commandTag
`)
	if q := s.Queries(); len(q) != 1 || !strings.Contains(q[0], "id >  0 ") {
		t.Errorf("queries = %q", q)
	}
}

func TestPostgres_OneConnectionForManyCalls(t *testing.T) {
	s := postgrestest.Start(t, pgRows)
	must(t, nil, `
for i in range(4):
    r = postgres.query("`+s.URL()+`", sslmode="disable", body="select 1")
    assert r.ok, r.error
`)
	if n := s.ConnCount(); n != 1 {
		t.Errorf("%d connections for 4 calls, want 1", n)
	}
}

func TestPostgres_FailureIsAReplyNotAnException(t *testing.T) {
	s := postgrestest.Start(t, func(string) []postgrestest.Result {
		return []postgrestest.Result{{ErrCode: "42P01", ErrMessage: "no such table"}}
	})
	must(t, nil, `
r = postgres.query("`+s.URL()+`", sslmode="disable", body="select * from nosuch")
assert r.ok is False
assert "42P01" in r.error, r.error
assert r.rows == []
`)
}

func TestPostgres_SetupMistakesRaise(t *testing.T) {
	for name, body := range map[string]string{
		"no sql":         `postgres.query("postgres://127.0.0.1:9/db", sslmode="disable")`,
		"unknown option": `postgres.query("postgres://127.0.0.1:9/db", body="select 1", bogus=1)`,
		"args not list":  `postgres.query("postgres://127.0.0.1:9/db", body="select 1", args=5)`,
	} {
		if err := runIteration(t, nil, body); err == nil {
			t.Errorf("%s: want an exception", name)
		}
	}
}
