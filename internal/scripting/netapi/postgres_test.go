package netapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol/postgres/postgrestest"
)

func rows(string) []postgrestest.Result {
	return []postgrestest.Result{{
		Columns: []postgrestest.Column{postgrestest.Int("id"), postgrestest.Text("name")},
		Rows:    [][]any{{"1", "ann"}, {"2", nil}},
	}}
}

func TestPostgres_PoolIsKeptPerConnectionSetup(t *testing.T) {
	s := postgrestest.Start(t, rows)
	c := NewProtoClient(nil, 5*time.Second)
	defer c.Close()
	opts := map[string]string{"sslmode": "disable", "username": "app"}
	for i := 0; i < 5; i++ {
		r, err := c.Postgres(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("select 1"), Options: cloneOptions(opts)})
		if err != nil || !r.OK {
			t.Fatalf("call %d: %v %+v", i, err, r)
		}
	}
	if n := s.ConnCount(); n != 1 {
		t.Errorf("%d connections for 5 calls, want the connection reused", n)
	}
	if len(c.pgConns) != 1 {
		t.Errorf("%d pools kept, want 1", len(c.pgConns))
	}
	// A different statement or arguments are a different job, not a different pool.
	o2 := cloneOptions(opts)
	o2["min_rows"] = "1"
	if r, err := c.Postgres(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("select 2"), Options: o2}); err != nil || !r.OK {
		t.Fatalf("%v %+v", err, r)
	}
	if len(c.pgConns) != 1 {
		t.Errorf("a new job made a new pool: %d", len(c.pgConns))
	}
	// A different login or setting is a different pool.
	o3 := cloneOptions(opts)
	o3["username"] = "other"
	if _, err := c.Postgres(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("select 1"), Options: o3}); err != nil {
		t.Fatal(err)
	}
	if len(c.pgConns) != 2 {
		t.Errorf("%d pools kept after another user, want 2", len(c.pgConns))
	}
	c.Close()
	if len(c.pgConns) != 0 {
		t.Error("Close must release the pools")
	}
}

func TestPostgres_ReplyFields(t *testing.T) {
	s := postgrestest.Start(t, rows)
	c := NewProtoClient(nil, 5*time.Second)
	defer c.Close()
	r, err := c.Call(context.Background(), "postgres.query", ProtoCall{URL: s.URL(), Body: []byte("select * from t"), Options: map[string]string{"sslmode": "disable"}})
	if err != nil {
		t.Fatal(err)
	}
	f := r.Fields()
	if f["ok"] != true || f["rowCount"] != 2 || f["commandTag"] != "SELECT 2" {
		t.Errorf("fields = %v", f)
	}
	rs := f["rows"].([]any)
	first := rs[0].(map[string]any)
	if first["id"] != int32(1) || first["name"] != "ann" || rs[1].(map[string]any)["name"] != nil {
		t.Errorf("rows = %v", rs)
	}
	if cols := f["columns"].([]any); len(cols) != 2 || cols[0] != "id" || cols[1] != "name" {
		t.Errorf("columns = %v", cols)
	}
	// The other protocols' replies do not get row fields.
	if _, ok := (&ProtoReply{}).Fields()["rows"]; ok {
		t.Error("a non-postgres reply has rows")
	}
}

func TestPostgres_FailureIsAReplyAndSetupMistakesAreErrors(t *testing.T) {
	s := postgrestest.Start(t, func(string) []postgrestest.Result {
		return []postgrestest.Result{{ErrCode: "23505", ErrMessage: "duplicate key"}}
	})
	c := NewProtoClient(nil, 5*time.Second)
	defer c.Close()
	r, err := c.Postgres(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("insert"), Options: map[string]string{"sslmode": "disable"}})
	if err != nil {
		t.Fatalf("a server error must be a reply: %v", err)
	}
	if r.OK || !strings.Contains(r.Error, "23505") {
		t.Errorf("reply = %+v", r)
	}
	if _, err := c.Postgres(context.Background(), ProtoCall{URL: s.URL(), Options: map[string]string{"sslmode": "disable"}}); err == nil {
		t.Error("no SQL must be an error")
	}
	if _, err := c.Postgres(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("x"), Options: map[string]string{"bogus": "1"}}); err == nil {
		t.Error("an unknown option must be an error")
	}
	if _, err := c.Postgres(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("x"), Options: map[string]string{"password_env": "X"}}); err == nil || !strings.Contains(err.Error(), "password: env.NAME") {
		t.Errorf("password_env: %v", err)
	}
}

func TestPostgres_PasswordFromAScript(t *testing.T) {
	s := postgrestest.StartWithPassword(t, rows, "pw!")
	c := NewProtoClient(nil, 5*time.Second)
	defer c.Close()
	pw := "pw!"
	r, err := c.Postgres(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("select 1"), Password: &pw, Options: map[string]string{"sslmode": "disable", "username": "app"}})
	if err != nil || !r.OK {
		t.Fatalf("%v %+v", err, r)
	}
}

func TestPostgres_SafetyCheckSeesTheHost(t *testing.T) {
	var seen string
	c := NewProtoClient(func(h string) error { seen = h; return context.Canceled }, time.Second)
	defer c.Close()
	if _, err := c.Postgres(context.Background(), ProtoCall{URL: "postgres://db.example.com/app", Body: []byte("select 1")}); err == nil {
		t.Fatal("want the check's error")
	}
	if seen != "db.example.com" {
		t.Errorf("check saw %q", seen)
	}
}

func TestProtoCallFromArgs_PostgresArgs(t *testing.T) {
	pc, err := ProtoCallFromArgs("postgres.query", "postgres://h/db", map[string]any{
		"body": "select $1", "args": []any{float64(1), "x", nil}, "sslmode": "disable",
	})
	if err != nil {
		t.Fatal(err)
	}
	if pc.Options["args"] != `[1,"x",null]` || string(pc.Body) != "select $1" || pc.Options["sslmode"] != "disable" {
		t.Errorf("pc = %+v", pc)
	}
	pc, err = ProtoCallFromArgs("postgres.query", "postgres://h/db", map[string]any{"args": `[1]`})
	if err != nil || pc.Options["args"] != "[1]" {
		t.Errorf("JSON text args: %+v %v", pc, err)
	}
	if _, err := ProtoCallFromArgs("postgres.query", "postgres://h/db", map[string]any{"args": 5}); err == nil {
		t.Error("args must be a list")
	}
	// Only postgres takes a list of args. Other calls keep the old error.
	if _, err := ProtoCallFromArgs("kafka.produce", "kafka://h", map[string]any{"args": []any{1}}); err == nil {
		t.Error("kafka does not take a list")
	}
	if _, err := ProtoCallFromArgs("postgres.query", "postgres://h/db", map[string]any{"value": "x"}); err == nil {
		t.Error("value is kafka only")
	}
}

func TestProtoFunctions_ListsPostgres(t *testing.T) {
	if f := ProtoFunctions()["postgres"]; len(f) != 1 || f[0] != "query" {
		t.Errorf("postgres functions = %v", f)
	}
}
