package mysql

import (
	"math"
	"strings"
	"testing"
)

func TestScan_Table(t *testing.T) {
	one := []any{int64(1)}
	cases := []struct {
		name string
		sql  string
		mode sqlMode
		args []any // nil means the option was not set
		want string
		n    int
		kws  string
		err  string
	}{
		{name: "question in string", sql: "select 'it''s ?', ?", args: one, want: "select 'it''s ?',  1 ", n: 1, kws: "select"},
		{name: "question in double string", sql: `select "a?b", ?`, args: one, want: `select "a?b",  1 `, n: 1, kws: "select"},
		{name: "question in ident", sql: "select `a?b`, ?", args: one, want: "select `a?b`,  1 ", n: 1, kws: "select"},
		{name: "line comment", sql: "select ? -- ?\n", args: one, want: "select  1  -- ?\n", n: 1, kws: "select"},
		{name: "hash comment", sql: "select ? # ?\n", args: one, want: "select  1  # ?\n", n: 1, kws: "select"},
		{name: "block comment", sql: "select ? /* ? */", args: one, want: "select  1  /* ? */", n: 1, kws: "select"},
		{name: "dash five is code", sql: "select --5 + ?", args: one, want: "select --5 +  1 ", n: 1, kws: "select"},
		{name: "negative", sql: "select x -?", args: []any{int64(-5)}, want: "select x - (-5) ", n: 1, kws: "select"},
		{name: "quote", sql: "select ?", args: []any{"o'brien"}, want: "select 'o''brien'", n: 1, kws: "select"},
		{name: "backslash", sql: "select ?", args: []any{`a\b`}, want: "select _utf8mb4 X'615c62'", n: 1, kws: "select"},
		{name: "injection", sql: "select ?", args: []any{"x'); drop table t; --"}, want: "select 'x''); drop table t; --'", n: 1, kws: "select"},
		{name: "null and bool", sql: "select ?, ?, ?", args: []any{nil, true, false}, want: "select NULL, TRUE, FALSE", n: 1, kws: "select"},
		{name: "escaped quote is inside", sql: `select 'a\'?'`, args: one, err: "only 0"},
		{name: "no backslash escapes", sql: `select 'a\'?'`, mode: sqlMode{noBackslash: true}, args: one, want: `select 'a\' 1 '`, n: 1, kws: "select"},
		{name: "ansi quotes", sql: "select \"a\\\"?\"", mode: sqlMode{ansiQuotes: true}, args: one, want: "select \"a\\\" 1 \"", n: 1, kws: "select"},
		{name: "ansi quotes off keeps the question inside", sql: "select \"a\\\"?\"", args: one, err: "only 0"},
		{name: "executable version comment", sql: "select /*!50100 ? */", args: []any{int64(7)}, want: "select /*!50100  7  */", n: 1, kws: "select"},
		{name: "optimizer hint", sql: "select /*+ ? */ 1", args: []any{int64(3)}, want: "select /*+  3  */ 1", n: 1, kws: "select"},
		{name: "several statements", sql: "begin; update t set a = ? where id = ?; select 1; commit", args: []any{int64(2), int64(9)}, want: "begin; update t set a =  2  where id =  9 ; select 1; commit", n: 4, kws: "begin,update,select,commit"},
		{name: "empty and trailing", sql: "select 1;;  ", n: 1, kws: "select", want: "select 1;;  "},
		{name: "leading paren", sql: "(select 1)", n: 1, kws: "(", want: "(select 1)"},
		{name: "too few", sql: "select ?, ?", args: one, err: "more ? placeholders"},
		{name: "too many", sql: "select ?", args: []any{int64(1), int64(2)}, err: "only 1"},
		{name: "none", sql: "select 1", args: one, err: "only 0"},
		{name: "no args leaves the question", sql: "select ?", want: "select ?", n: 1, kws: "select"},
		{name: "nan", sql: "select ?", args: []any{math.NaN()}, err: "NaN"},
		{name: "bad type", sql: "select ?", args: []any{struct{}{}}, err: "cannot be used"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, info, err := scan(c.sql, c.mode, c.args)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) || !strings.HasPrefix(err.Error(), "mysql:") {
					t.Fatalf("error = %v, want it to contain %q", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got %q\nwant %q", got, c.want)
			}
			if info.statements != c.n || strings.Join(info.keywords, ",") != c.kws {
				t.Errorf("statements %d keywords %v", info.statements, info.keywords)
			}
		})
	}
}
