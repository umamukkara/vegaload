package postgres

import (
	"strings"
	"testing"
)

func TestInterpolate(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		args []any
		want string
	}{
		{"numbers and text", "select $1, $2, $3", []any{int64(7), 2.5, "x"}, "select  7 ,  2.5 , 'x'"},
		{"null and bool", "select $1, $2, $3", []any{nil, true, false}, "select NULL, true, false"},
		{"a value can be used twice", "select $1, $1", []any{int64(3)}, "select  3 ,  3 "},
		{"out of order", "select $2, $1", []any{"a", "b"}, "select 'b', 'a'"},
		{"quote is doubled", "select $1", []any{"it's"}, "select 'it''s'"},
		{"a value that looks like SQL stays text", "select $1", []any{"x'; drop table t; --"}, "select 'x''; drop table t; --'"},
		{"a backslash makes an E string", "select $1", []any{`a\b'c`}, `select E'a\\b''c'`},
		{"a negative number cannot become a comment", "select 1 -$1", []any{int64(-5)}, "select 1 - (-5) "},
		{"a placeholder in a string is left alone", "select '$1', $1", []any{"v"}, "select '$1', 'v'"},
		{"a doubled quote inside a string", "select 'it''s $1', $1", []any{"v"}, "select 'it''s $1', 'v'"},
		{"an E string with an escaped quote", `select E'a\' $1', $1`, []any{"v"}, `select E'a\' $1', 'v'`},
		{"an identifier", `select "col$1" from t where a = $1`, []any{int64(1)}, `select "col$1" from t where a =  1 `},
		{"a line comment", "select 1 -- $1\n, $1", []any{"v"}, "select 1 -- $1\n, 'v'"},
		{"a block comment, nested", "select /* a /* $1 */ $1 */ $1", []any{"v"}, "select /* a /* $1 */ $1 */ 'v'"},
		{"a dollar quote", "select $$ $1 $$, $1", []any{"v"}, "select $$ $1 $$, 'v'"},
		{"a tagged dollar quote", "do $body$ select $1 $body$; select $1", []any{"v"}, "do $body$ select $1 $body$; select 'v'"},
		{"a dollar in a name", "select a$1 from t where b = $1", []any{"v"}, "select a$1 from t where b = 'v'"},
		{"a cast", "select $1::int", []any{"5"}, "select '5'::int"},
		{"a big number keeps its digits", "select $1", []any{int64(9007199254740993)}, "select  9007199254740993 "},
		{"a placeholder with two digits", "select $1,$2,$3,$4,$5,$6,$7,$8,$9,$10", []any{int64(1), int64(2), int64(3), int64(4), int64(5), int64(6), int64(7), int64(8), int64(9), int64(10)}, "select  1 , 2 , 3 , 4 , 5 , 6 , 7 , 8 , 9 , 10 "},
		{"no args and no placeholders", "select 1", nil, "select 1"},
	}
	for _, c := range cases {
		got, err := interpolate(c.sql, c.args)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s:\n got  %q\n want %q", c.name, got, c.want)
		}
	}
}

func TestInterpolate_Errors(t *testing.T) {
	cases := map[string]struct {
		sql  string
		args []any
		want string
	}{
		"too few values":      {"select $2", []any{int64(1)}, "$2 but args has 1"},
		"an unused value":     {"select $1", []any{int64(1), int64(2)}, "args[1] is never used"},
		"a gap":               {"select $2", []any{int64(1), int64(2)}, "args[0] is never used"},
		"a gap in the middle": {"select $1, $3", []any{int64(1), int64(2), int64(3)}, "args[1] is never used"},
		"no placeholder":      {"select 1", []any{int64(1)}, "args[0] is never used"},
		"a zero":              {"select $0", []any{int64(1)}, "not a valid"},
		"a number and name":   {"select $1abc", []any{int64(1)}, "not a valid"},
		"a zero byte":         {"select $1", []any{"a\x00b"}, "zero byte"},
		"a nested value":      {"select $1", []any{[]any{1}}, "cannot be used"},
	}
	for name, c := range cases {
		_, err := interpolate(c.sql, c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error = %v, want it to contain %q", name, err, c.want)
		}
	}
}
