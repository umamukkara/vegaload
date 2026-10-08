package python

import (
	"strings"
	"testing"

	"github.com/vegaload/vegaload/internal/inputs"
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

func TestMySQL_QueryReturnsRowsAndInsertId(t *testing.T) {
	s := mysqltest.Start(t, myRows)
	must(t, nil, `
r = mysql.query("`+s.URL()+`", tls="false", username="app", body="select id, name from t where id > ?", args=[0])
assert r.ok, r.error
assert r.rowCount == 2 and len(r.rows) == 2, r
assert r.rows[0].id == 1 and r.rows[0].name == "ann", r.rows[0]
assert r.rows[1]["name"] is None
assert r.columns == ["id", "name"], r.columns
assert "commandTag" not in r
assert r.lastInsertId == 0 and r.rowsAffected == 0
ins = mysql.query("`+s.URL()+`", tls="false", username="app", allow_writes=True, body="insert into t (name) values (?)", args=["ann"])
assert ins.ok, ins.error
assert ins.lastInsertId == 7, ins.lastInsertId
assert ins.rowsAffected == 1, ins.rowsAffected
assert "commandTag" not in ins
`)
	var sawSelect, sawInsert bool
	for _, q := range s.Queries() {
		if strings.Contains(q, "id >  0 ") {
			sawSelect = true
		}
		if strings.Contains(q, "ann") {
			sawInsert = true
		}
	}
	if !sawSelect || !sawInsert {
		t.Errorf("queries = %q", s.Queries())
	}
}

func TestMySQL_OneConnectionForManyCalls(t *testing.T) {
	s := mysqltest.Start(t, myRows)
	must(t, nil, `
for i in range(4):
    r = mysql.query("`+s.URL()+`", tls="false", body="select 1")
    assert r.ok, r.error
`)
	if n := s.ConnCount(); n != 1 {
		t.Errorf("%d connections for 4 calls, want 1", n)
	}
}

func TestMySQL_FailureIsAReplyNotAnException(t *testing.T) {
	s := mysqltest.Start(t, func(string) []mysqltest.Result {
		return []mysqltest.Result{{ErrNumber: 1146, ErrState: "42S02", ErrMessage: "no such table"}}
	})
	must(t, nil, `
r = mysql.query("`+s.URL()+`", tls="false", body="select * from nosuch")
assert r.ok is False
assert "1146" in r.error and "42S02" in r.error, r.error
assert r.rows == []
`)
}

func TestMySQL_SetupMistakesRaise(t *testing.T) {
	for name, body := range map[string]string{
		"no sql":         `mysql.query("mysql://127.0.0.1:9/db", tls="false")`,
		"unknown option": `mysql.query("mysql://127.0.0.1:9/db", body="select 1", bogus=1)`,
		"args not list":  `mysql.query("mysql://127.0.0.1:9/db", body="select 1", args=5)`,
		"password_env":   `mysql.query("mysql://127.0.0.1:9/db", body="select 1", password_env="X")`,
	} {
		if err := runIteration(t, nil, body); err == nil {
			t.Errorf("%s: want an exception", name)
		}
	}
}

func TestMySQL_PasswordFromEnv(t *testing.T) {
	s := mysqltest.StartWithPassword(t, myRows, "from-env")
	in := inputs.New(map[string]string{"DB_PASSWORD": "from-env"}, nil)
	must(t, nil, `
r = mysql.query("`+s.URL()+`", tls="false", username="app", password=env.DB_PASSWORD, body="select 1")
assert r.ok, r.error
`, WithInputs(in))
}
