package netapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol/mysql/mysqltest"
)

func myRows(sql string) []mysqltest.Result {
	if strings.Contains(strings.ToLower(sql), "insert") {
		return []mysqltest.Result{{Affected: 1, LastInsertID: 7}}
	}
	return []mysqltest.Result{{
		Columns: []mysqltest.Column{mysqltest.Int("id"), mysqltest.Text("name")},
		Rows:    [][]any{{"1", "ann"}, {"2", nil}},
	}}
}

func TestMySQL_PoolIsKeptPerConnectionSetup(t *testing.T) {
	s := mysqltest.Start(t, myRows)
	c := NewProtoClient(nil, 5*time.Second)
	defer c.Close()
	opts := map[string]string{"tls": "false", "username": "app"}
	for i := 0; i < 5; i++ {
		r, err := c.MySQL(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("select 1"), Options: cloneOptions(opts)})
		if err != nil || !r.OK {
			t.Fatalf("call %d: %v %+v", i, err, r)
		}
	}
	if n := s.ConnCount(); n != 1 {
		t.Errorf("%d connections for 5 calls, want the connection reused", n)
	}
	if len(c.myConns) != 1 {
		t.Errorf("%d pools kept, want 1", len(c.myConns))
	}
	o2 := cloneOptions(opts)
	o2["min_rows"] = "1"
	if r, err := c.MySQL(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("select 2"), Options: o2}); err != nil || !r.OK {
		t.Fatalf("%v %+v", err, r)
	}
	if len(c.myConns) != 1 {
		t.Errorf("a new job made a new pool: %d", len(c.myConns))
	}
	o3 := cloneOptions(opts)
	o3["username"] = "other"
	if _, err := c.MySQL(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("select 1"), Options: o3}); err != nil {
		t.Fatal(err)
	}
	if len(c.myConns) != 2 {
		t.Errorf("%d pools kept after another user, want 2", len(c.myConns))
	}
	c.Close()
	if len(c.myConns) != 0 {
		t.Error("Close must release the pools")
	}
}

func TestMySQL_ReplyFields(t *testing.T) {
	s := mysqltest.Start(t, myRows)
	c := NewProtoClient(nil, 5*time.Second)
	defer c.Close()
	r, err := c.Call(context.Background(), "mysql.query", ProtoCall{URL: s.URL(), Body: []byte("select * from t"), Options: map[string]string{"tls": "false"}})
	if err != nil {
		t.Fatal(err)
	}
	f := r.Fields()
	if f["ok"] != true || f["rowCount"] != 2 || f["rowsAffected"] != int64(0) || f["lastInsertId"] != int64(0) {
		t.Errorf("fields = %v", f)
	}
	if _, ok := f["commandTag"]; ok {
		t.Error("a MySQL reply has commandTag")
	}
	rs := f["rows"].([]any)
	first := rs[0].(map[string]any)
	if first["id"] != int64(1) || first["name"] != "ann" || rs[1].(map[string]any)["name"] != nil {
		t.Errorf("rows = %v", rs)
	}
	ins, err := c.Call(context.Background(), "mysql.query", ProtoCall{
		URL: s.URL(), Body: []byte("insert into t (name) values ('ann')"),
		Options: map[string]string{"tls": "false", "allow_writes": "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := ins.Fields()
	if got["ok"] != true || got["rowsAffected"] != int64(1) || got["lastInsertId"] != int64(7) {
		t.Errorf("insert fields = %v", got)
	}
	if _, ok := got["commandTag"]; ok {
		t.Error("an insert reply has commandTag")
	}
}

func TestMySQL_FailureIsAReplyAndSetupMistakesAreErrors(t *testing.T) {
	s := mysqltest.Start(t, func(string) []mysqltest.Result {
		return []mysqltest.Result{{ErrNumber: 1146, ErrState: "42S02", ErrMessage: "no such table"}}
	})
	c := NewProtoClient(nil, 5*time.Second)
	defer c.Close()
	r, err := c.MySQL(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("select * from nosuch"), Options: map[string]string{"tls": "false"}})
	if err != nil {
		t.Fatalf("a server error must be a reply: %v", err)
	}
	if r.OK || !strings.Contains(r.Error, "1146") || !strings.Contains(r.Error, "42S02") {
		t.Errorf("reply = %+v", r)
	}
	if _, err := c.MySQL(context.Background(), ProtoCall{URL: s.URL(), Options: map[string]string{"tls": "false"}}); err == nil {
		t.Error("no SQL must be an error")
	}
	if _, err := c.MySQL(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("x"), Options: map[string]string{"bogus": "1"}}); err == nil {
		t.Error("an unknown option must be an error")
	}
	if _, err := c.MySQL(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("x"), Options: map[string]string{"password_env": "X"}}); err == nil || !strings.Contains(err.Error(), "password: env.NAME") {
		t.Errorf("password_env: %v", err)
	}
}

func TestMySQL_PasswordFromAScript(t *testing.T) {
	s := mysqltest.StartWithPassword(t, myRows, "pw!")
	c := NewProtoClient(nil, 5*time.Second)
	defer c.Close()
	pw := "pw!"
	r, err := c.MySQL(context.Background(), ProtoCall{URL: s.URL(), Body: []byte("select 1"), Password: &pw, Options: map[string]string{"tls": "false", "username": "app"}})
	if err != nil || !r.OK {
		t.Fatalf("%v %+v", err, r)
	}
}

func TestMySQL_SafetyCheckSeesTheHost(t *testing.T) {
	var seen string
	c := NewProtoClient(func(h string) error { seen = h; return context.Canceled }, time.Second)
	defer c.Close()
	if _, err := c.MySQL(context.Background(), ProtoCall{URL: "mysql://db.example.com/app", Body: []byte("select 1")}); err == nil {
		t.Fatal("want the check's error")
	}
	if seen != "db.example.com" {
		t.Errorf("check saw %q", seen)
	}
}

func TestProtoCallFromArgs_MySQLArgs(t *testing.T) {
	pc, err := ProtoCallFromArgs("mysql.query", "mysql://h/db", map[string]any{
		"body": "select ?", "args": []any{float64(1), "x", nil}, "tls": "false",
	})
	if err != nil {
		t.Fatal(err)
	}
	if pc.Options["args"] != `[1,"x",null]` || string(pc.Body) != "select ?" || pc.Options["tls"] != "false" {
		t.Errorf("pc = %+v", pc)
	}
	if _, err := ProtoCallFromArgs("kafka.produce", "kafka://h", map[string]any{"args": []any{1}}); err == nil {
		t.Error("kafka does not take a list")
	}
}

func TestProtoFunctions_ListsMySQL(t *testing.T) {
	if f := ProtoFunctions()["mysql"]; len(f) != 1 || f[0] != "query" {
		t.Errorf("mysql functions = %v", f)
	}
}
