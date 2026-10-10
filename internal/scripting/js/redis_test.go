package js

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
	must(t, nil, assertFn+`
		const r = redis.command("`+s.URL()+`", {body: "GET user"});
		assert(r.ok, r.error);
		assert(r.value === "ann", JSON.stringify(r.value));
		assert(r.rowCount === 1, String(r.rowCount));
		assert(r.values.length === 1 && r.values[0] === "ann", JSON.stringify(r.values));
		assert(r.rows === undefined && r.commandTag === undefined && r.lastInsertId === undefined, "no SQL fields");
		const many = redis.command("`+s.URL()+`", {body: "MGET a\nGET user"});
		assert(many.ok, many.error);
		assert(many.values.length === 2 && many.values[0][0] === "a" && many.value === "ann", JSON.stringify(many.values));
		const miss = redis.command("`+s.URL()+`", {body: "GET ?", args: ["missing"]});
		assert(miss.ok && miss.value === null, JSON.stringify(miss));
		const denied = redis.command("`+s.URL()+`", {body: "SET a b"});
		assert(denied.ok === false && denied.error.indexOf("allow_writes") >= 0, denied.error);
		const set = redis.command("`+s.URL()+`", {allow_writes: true, body: "SET a b"});
		assert(set.ok, set.error);
	`)
}

func TestRedis_OneConnectionForManyCalls(t *testing.T) {
	s := redistest.Start(t, redisValue)
	must(t, nil, assertFn+`
		for (let i = 0; i < 4; i++) {
			const r = redis.command("`+s.URL()+`", {body: "GET user"});
			assert(r.ok, r.error);
		}
	`)
	if n := s.ConnCount(); n != 1 {
		t.Errorf("%d connections for 4 calls, want 1", n)
	}
}

func TestRedis_SetupMistakesThrow(t *testing.T) {
	for name, body := range map[string]string{
		"no command":     `redis.command("redis://127.0.0.1:9");`,
		"unknown option": `redis.command("redis://127.0.0.1:9", {body: "GET a", bogus: 1});`,
		"args not list":  `redis.command("redis://127.0.0.1:9", {body: "GET a", args: 5});`,
		"password_env":   `redis.command("redis://127.0.0.1:9", {body: "GET a", password_env: "X"});`,
	} {
		if err := runScript(t, nil, body); err == nil {
			t.Errorf("%s: want the script to throw", name)
		}
	}
}

func TestRedis_PasswordFromEnv(t *testing.T) {
	s := redistest.StartWithPassword(t, redisValue, "from-env")
	path := writeScript(t, "s.js", "export default function () {\n"+assertFn+`
		const r = redis.command("`+s.URL()+`", {username: "app", password: env.REDIS_PASSWORD, body: "GET user"});
		assert(r.ok, r.error);
	}
	`)
	script, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	in := inputs.New(map[string]string{"REDIS_PASSWORD": "from-env"}, nil)
	vu, err := script.NewVU(nil, 3*time.Second, WithInputs(in))
	if err != nil {
		t.Fatal(err)
	}
	defer vu.Close()
	if err := vu.Iteration(context.Background()); err != nil {
		t.Fatalf("script failed: %v", err)
	}
}

func TestRedis_SafetyCheckRefusesTheHost(t *testing.T) {
	check := func(h string) error {
		if h == "db.example.com" {
			return errors.New("host not allowed")
		}
		return nil
	}
	if err := runScript(t, netapi.SafetyCheck(check), `redis.command("redis://db.example.com", {body: "GET a"});`); err == nil || !strings.Contains(err.Error(), "host not allowed") {
		t.Errorf("err = %v", err)
	}
}
