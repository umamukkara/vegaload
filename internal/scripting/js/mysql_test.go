package js

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/inputs"
	"github.com/vegaload/vegaload/internal/protocol/mysql/mysqltest"
	"github.com/vegaload/vegaload/internal/scripting/netapi"
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

func TestMySQL_QueryReturnsRowsAndInsertId(t *testing.T) {
	s := mysqltest.Start(t, myRows)
	must(t, nil, assertFn+`
		const r = mysql.query("`+s.URL()+`", {tls: "false", username: "app", body: "select id, name from t where id > ?", args: [0]});
		assert(r.ok, r.error);
		assert(r.rowCount === 2 && r.rows.length === 2, "rows");
		assert(r.rows[0].id === 1 && r.rows[0].name === "ann", JSON.stringify(r.rows[0]));
		assert(r.rows[1].name === null, "NULL is null");
		assert(r.columns.join() === "id,name", "columns");
		assert(r.commandTag === undefined, "no command tag");
		assert(r.lastInsertId === 0 && r.rowsAffected === 0, "a select has no insert id");
		const ins = mysql.query("`+s.URL()+`", {tls: "false", username: "app", allow_writes: true, body: "insert into t (name) values (?)", args: ["ann"]});
		assert(ins.ok, ins.error);
		assert(ins.lastInsertId === 7, String(ins.lastInsertId));
		assert(ins.rowsAffected === 1, String(ins.rowsAffected));
		assert(ins.commandTag === undefined, "an insert has no command tag");
	`)
	var sawSelect, sawInsert bool
	for _, q := range s.Queries() {
		if strings.Contains(q, "id >  0 ") {
			sawSelect = true
		}
		if strings.Contains(q, "values (") && strings.Contains(q, "ann") {
			sawInsert = true
		}
	}
	if !sawSelect || !sawInsert {
		t.Errorf("queries = %q", s.Queries())
	}
}

func TestMySQL_OneConnectionForManyCalls(t *testing.T) {
	s := mysqltest.Start(t, myRows)
	must(t, nil, assertFn+`
		for (let i = 0; i < 4; i++) {
			const r = mysql.query("`+s.URL()+`", {tls: "false", body: "select 1"});
			assert(r.ok, r.error);
		}
	`)
	if n := s.ConnCount(); n != 1 {
		t.Errorf("%d connections for 4 calls, want 1", n)
	}
}

func TestMySQL_FailureIsAReplyNotAnException(t *testing.T) {
	s := mysqltest.Start(t, func(string) []mysqltest.Result {
		return []mysqltest.Result{{ErrNumber: 1146, ErrState: "42S02", ErrMessage: "no such table"}}
	})
	must(t, nil, assertFn+`
		const r = mysql.query("`+s.URL()+`", {tls: "false", body: "select * from nosuch"});
		assert(r.ok === false, "ok is false");
		assert(r.error.indexOf("1146") >= 0 && r.error.indexOf("42S02") >= 0, r.error);
		assert(r.rows.length === 0, "no rows");
	`)
}

func TestMySQL_SetupMistakesThrow(t *testing.T) {
	for name, body := range map[string]string{
		"no sql":         `mysql.query("mysql://127.0.0.1:9/db", {tls: "false"});`,
		"unknown option": `mysql.query("mysql://127.0.0.1:9/db", {body: "select 1", bogus: 1});`,
		"args not list":  `mysql.query("mysql://127.0.0.1:9/db", {body: "select 1", args: 5});`,
		"password_env":   `mysql.query("mysql://127.0.0.1:9/db", {body: "select 1", password_env: "X"});`,
	} {
		if err := runScript(t, nil, body); err == nil {
			t.Errorf("%s: want the script to throw", name)
		}
	}
}

func TestMySQL_PasswordFromEnv(t *testing.T) {
	s := mysqltest.StartWithPassword(t, myRows, "from-env")
	path := writeScript(t, "s.js", "export default function () {\n"+assertFn+`
		const r = mysql.query("`+s.URL()+`", {tls: "false", username: "app", password: env.DB_PASSWORD, body: "select 1"});
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

func TestMySQL_SafetyCheckRefusesTheHost(t *testing.T) {
	check := func(h string) error {
		if h == "db.example.com" {
			return errors.New("host not allowed")
		}
		return nil
	}
	if err := runScript(t, netapi.SafetyCheck(check), `mysql.query("mysql://db.example.com/app", {body: "select 1"});`); err == nil || !strings.Contains(err.Error(), "host not allowed") {
		t.Errorf("err = %v", err)
	}
}
