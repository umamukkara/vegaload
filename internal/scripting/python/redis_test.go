package python

import (
	"strings"
	"testing"

	"github.com/vegaload/vegaload/internal/inputs"
	"github.com/vegaload/vegaload/internal/protocol/redis/redistest"
	"github.com/vegaload/vegaload/internal/scripting/netapi"
)

func redisValue(args []string) redistest.Value {
	switch strings.ToUpper(args[0]) {
	case "GET":
		if len(args) > 1 && args[1] == "missing" {
			return redistest.NilValue()
		}
		return redistest.BulkString("ann")
	case "MGET":
		return redistest.ArrayOf(redistest.BulkString("a"), redistest.BulkString("b"))
	case "SET":
		return redistest.SimpleString("OK")
	default:
		return redistest.ErrorString("ERR no")
	}
}

func TestRedis_CommandReturnsValue(t *testing.T) {
	s := redistest.Start(t, redisValue)
	must(t, nil, `
r = redis.command("`+s.URL()+`", body="GET user")
assert r.ok, r.error
assert r.value == "ann", r.value
assert r.rowCount == 1, r.rowCount
assert r.values == ["ann"], r.values
assert "rows" not in r and "commandTag" not in r and "lastInsertId" not in r
many = redis.command("`+s.URL()+`", body="MGET a\nGET user")
assert many.ok, many.error
assert len(many.values) == 2 and many.values[0][0] == "a" and many.value == "ann", many.values
miss = redis.command("`+s.URL()+`", body="GET ?", args=["missing"])
assert miss.ok and miss.value is None, miss
denied = redis.command("`+s.URL()+`", body="SET a b")
assert denied.ok is False and "allow_writes" in denied.error, denied.error
stored = redis.command("`+s.URL()+`", allow_writes=True, body="SET a b")
assert stored.ok, stored.error
`)
}

func TestRedis_OneConnectionForManyCalls(t *testing.T) {
	s := redistest.Start(t, redisValue)
	must(t, nil, `
for i in range(4):
    r = redis.command("`+s.URL()+`", body="GET user")
    assert r.ok, r.error
`)
	if n := s.ConnCount(); n != 1 {
		t.Errorf("%d connections for 4 calls, want 1", n)
	}
}

func TestRedis_SetupMistakesRaise(t *testing.T) {
	for name, body := range map[string]string{
		"no command":     `redis.command("redis://127.0.0.1:9")`,
		"unknown option": `redis.command("redis://127.0.0.1:9", body="GET a", bogus=1)`,
		"args not list":  `redis.command("redis://127.0.0.1:9", body="GET a", args=5)`,
		"password_env":   `redis.command("redis://127.0.0.1:9", body="GET a", password_env="X")`,
	} {
		if err := runIteration(t, nil, body); err == nil {
			t.Errorf("%s: want the script to raise", name)
		}
	}
}

func TestRedis_PasswordFromEnv(t *testing.T) {
	s := redistest.StartWithPassword(t, redisValue, "from-env")
	in := inputs.New(map[string]string{"REDIS_PASSWORD": "from-env"}, nil)
	must(t, nil, `
r = redis.command("`+s.URL()+`", username="app", password=env.REDIS_PASSWORD, body="GET user")
assert r.ok, r.error
`, WithInputs(in))
}

func TestRedis_SafetyCheckRefusesTheHost(t *testing.T) {
	check := func(h string) error {
		if h == "db.example.com" {
			return errString("host not allowed")
		}
		return nil
	}
	if err := runIteration(t, netapi.SafetyCheck(check), `redis.command("redis://db.example.com", body="GET a")`); err == nil || !strings.Contains(err.Error(), "host not allowed") {
		t.Errorf("err = %v", err)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
