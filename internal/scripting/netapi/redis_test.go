package netapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol/redis/redistest"
)

func redisValue(args []string) redistest.Value {
	switch strings.ToUpper(args[0]) {
	case "GET":
		return redistest.BulkString("ann")
	case "MGET":
		return redistest.ArrayOf(redistest.BulkString("a"), redistest.BulkString("b"))
	case "SET":
		return redistest.SimpleString("OK")
	default:
		return redistest.ErrorString("ERR no such command")
	}
}

func TestRedis_ClientIsKeptPerConnectionSetup(t *testing.T) {
	s := redistest.Start(t, redisValue)
	c := NewProtoClient(nil, 5*time.Second)
	defer c.Close()
	opts := map[string]string{}
	for i := 0; i < 5; i++ {
		r, err := c.Redis(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("GET user"), Options: cloneOptions(opts)})
		if err != nil || !r.OK {
			t.Fatalf("call %d: %v %+v", i, err, r)
		}
	}
	if n := s.ConnCount(); n != 1 {
		t.Errorf("%d connections for 5 calls, want the connection reused", n)
	}
	if len(c.redisConns) != 1 {
		t.Errorf("%d clients kept, want 1", len(c.redisConns))
	}
	o2 := cloneOptions(opts)
	o2["min_rows"] = "1"
	if r, err := c.Redis(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("GET user"), Options: o2}); err != nil || !r.OK {
		t.Fatalf("%v %+v", err, r)
	}
	if len(c.redisConns) != 1 {
		t.Errorf("a new job made a new client: %d", len(c.redisConns))
	}
	o3 := cloneOptions(opts)
	o3["username"] = "other"
	if _, err := c.Redis(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("GET user"), Options: o3}); err != nil {
		t.Fatal(err)
	}
	if len(c.redisConns) != 2 {
		t.Errorf("%d clients kept after another user, want 2", len(c.redisConns))
	}
	c.Close()
	if len(c.redisConns) != 0 {
		t.Error("Close must release the clients")
	}
}

func TestRedis_ReplyFields(t *testing.T) {
	s := redistest.Start(t, redisValue)
	c := NewProtoClient(nil, 5*time.Second)
	defer c.Close()
	r, err := c.Call(context.Background(), "redis.command", ProtoCall{URL: s.URL(), Body: []byte("GET user\nMGET a")})
	if err != nil {
		t.Fatal(err)
	}
	f := r.Fields()
	if f["ok"] != true || f["value"] == nil || f["rowCount"] != 2 {
		t.Errorf("fields = %v", f)
	}
	vals := f["values"].([]any)
	if vals[0] != "ann" || len(vals[1].([]any)) != 2 {
		t.Errorf("values = %#v", vals)
	}
	for _, k := range []string{"rows", "columns", "rowsAffected", "commandTag", "lastInsertId"} {
		if _, ok := f[k]; ok {
			t.Errorf("a Redis reply has %s", k)
		}
	}
}

func TestRedis_FailureIsAReplyAndSetupMistakesAreErrors(t *testing.T) {
	s := redistest.Start(t, redisValue)
	c := NewProtoClient(nil, 5*time.Second)
	defer c.Close()
	r, err := c.Redis(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("SET a b")})
	if err != nil {
		t.Fatalf("a refused write must be a reply: %v", err)
	}
	if r.OK || !strings.Contains(r.Error, "allow_writes") {
		t.Errorf("reply = %+v", r)
	}
	if _, err := c.Redis(context.Background(), ProtoCall{URL: s.URL()}); err == nil {
		t.Error("no command must be an error")
	}
	if _, err := c.Redis(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("GET a"), Options: map[string]string{"bogus": "1"}}); err == nil {
		t.Error("an unknown option must be an error")
	}
	if _, err := c.Redis(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("GET a"), Options: map[string]string{"password_env": "X"}}); err == nil || !strings.Contains(err.Error(), "password: env.NAME") {
		t.Errorf("password_env: %v", err)
	}
}

func TestRedis_PasswordFromAScript(t *testing.T) {
	s := redistest.StartWithPassword(t, redisValue, "pw!")
	c := NewProtoClient(nil, 5*time.Second)
	defer c.Close()
	pw := "pw!"
	r, err := c.Redis(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("GET user"), Password: &pw, Options: map[string]string{"username": "ann"}})
	if err != nil || !r.OK {
		t.Fatalf("%v %+v", err, r)
	}
}

func TestRedis_SafetyCheckSeesTheHost(t *testing.T) {
	var seen string
	c := NewProtoClient(func(h string) error { seen = h; return context.Canceled }, time.Second)
	defer c.Close()
	if _, err := c.Redis(context.Background(), ProtoCall{URL: "redis://db.example.com", Body: []byte("GET a")}); err == nil {
		t.Fatal("want the check's error")
	}
	if seen != "db.example.com" {
		t.Errorf("check saw %q", seen)
	}
}

func TestProtoCallFromArgs_RedisArgs(t *testing.T) {
	pc, err := ProtoCallFromArgs("redis.command", "redis://h", map[string]any{
		"body": "GET ?", "args": []any{float64(1), "x"}, "database": "2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if pc.Options["args"] != `[1,"x"]` || string(pc.Body) != "GET ?" || pc.Options["database"] != "2" {
		t.Errorf("pc = %+v", pc)
	}
}

func TestProtoFunctions_ListsRedis(t *testing.T) {
	if f := ProtoFunctions()["redis"]; len(f) != 1 || f[0] != "command" {
		t.Errorf("redis functions = %v", f)
	}
}
