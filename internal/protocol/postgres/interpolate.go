package postgres

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// interpolate puts the values of args into sql in place of $1, $2 and so
// on, so the whole text can be sent as one simple query and may hold several
// statements. (pgx can do this for one statement, but then it gives back only
// the first result, so the driver does it itself and reads every result.)
//
// A placeholder is only replaced where PostgreSQL would read it as one. Text
// inside 'strings', E'strings', "identifiers", $tag$dollar quotes$tag$,
// -- line comments and /* block comments */ is left as it is, and so is a $
// inside a name such as a$1.
//
// Values are written as safe literals: a number as a number, a boolean as
// true or false, nil as NULL, and text in single quotes with every quote
// doubled. Text that holds a backslash is written as an E'...' string with
// the backslashes doubled, so the result is the same whether or not the server
// has standard_conforming_strings on. Text with a zero byte is refused,
// because PostgreSQL cannot hold one.
//
// The SQL must use every value ($1 up to the last one, with no gap), and no
// placeholder may point past the values.
func interpolate(sql string, args []any) (string, error) {
	var out strings.Builder
	out.Grow(len(sql) + 16*len(args))
	seen := make([]bool, len(args))
	i := 0
	n := len(sql)
	for i < n {
		c := sql[i]
		switch {
		case c == '\'':
			j := scanQuoted(sql, i, '\'', i > 0 && (sql[i-1] == 'E' || sql[i-1] == 'e') && (i == 1 || !isIdentChar(sql[i-2])))
			out.WriteString(sql[i:j])
			i = j
		case c == '"':
			j := scanQuoted(sql, i, '"', false)
			out.WriteString(sql[i:j])
			i = j
		case c == '-' && i+1 < n && sql[i+1] == '-':
			j := strings.IndexByte(sql[i:], '\n')
			if j < 0 {
				j = n
			} else {
				j += i
			}
			out.WriteString(sql[i:j])
			i = j
		case c == '/' && i+1 < n && sql[i+1] == '*':
			j := scanBlockComment(sql, i)
			out.WriteString(sql[i:j])
			i = j
		case c == '$' && (i == 0 || !isIdentChar(sql[i-1])):
			// A placeholder ($1) or the start of a dollar quote ($tag$).
			j := i + 1
			for j < n && sql[j] >= '0' && sql[j] <= '9' {
				j++
			}
			if j > i+1 {
				if j < n && isIdentChar(sql[j]) {
					return "", fmt.Errorf("postgres: %q in the SQL is not a valid $number placeholder", sql[i:j+1])
				}
				idx, err := strconv.Atoi(sql[i+1 : j])
				if err != nil || idx < 1 {
					return "", fmt.Errorf("postgres: %q in the SQL is not a valid placeholder", sql[i:j])
				}
				if idx > len(args) {
					return "", fmt.Errorf("postgres: the SQL uses $%d but args has %d values", idx, len(args))
				}
				lit, err := literal(args[idx-1])
				if err != nil {
					return "", fmt.Errorf("postgres: args[%d]: %w", idx-1, err)
				}
				out.WriteString(lit)
				seen[idx-1] = true
				i = j
				break
			}
			if end := dollarQuoteEnd(sql, i); end > 0 {
				out.WriteString(sql[i:end])
				i = end
				break
			}
			out.WriteByte(c)
			i++
		default:
			out.WriteByte(c)
			i++
		}
	}
	for i, ok := range seen {
		if !ok {
			return "", fmt.Errorf("postgres: args[%d] is never used: the SQL has no $%d", i, i+1)
		}
	}
	return out.String(), nil
}

func isIdentChar(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// scanQuoted returns the index just after the quoted text that starts at
// sql[start] (a quote character). A doubled quote is an escaped quote. With
// backslash true (an E'...' string), a backslash also escapes the next byte.
// A quote that never closes runs to the end, and the server then reports the
// syntax error.
func scanQuoted(sql string, start int, quote byte, backslash bool) int {
	i := start + 1
	for i < len(sql) {
		switch {
		case backslash && sql[i] == '\\':
			i += 2
		case sql[i] == quote:
			if i+1 < len(sql) && sql[i+1] == quote {
				i += 2
				continue
			}
			return i + 1
		default:
			i++
		}
	}
	return len(sql)
}

// scanBlockComment returns the index just after the /* ... */ comment that
// starts at sql[start]. PostgreSQL block comments nest.
func scanBlockComment(sql string, start int) int {
	depth := 0
	i := start
	for i < len(sql) {
		switch {
		case strings.HasPrefix(sql[i:], "/*"):
			depth++
			i += 2
		case strings.HasPrefix(sql[i:], "*/"):
			depth--
			i += 2
			if depth == 0 {
				return i
			}
		default:
			i++
		}
	}
	return len(sql)
}

// dollarQuoteEnd returns the index just after the dollar-quoted string that
// starts at sql[start], or 0 if sql[start] does not start one. The tag is
// empty ($$) or a name ($body$).
func dollarQuoteEnd(sql string, start int) int {
	j := start + 1
	for j < len(sql) && (isIdentChar(sql[j]) && sql[j] != '$') {
		j++
	}
	if j >= len(sql) || sql[j] != '$' {
		return 0
	}
	if j > start+1 && sql[start+1] >= '0' && sql[start+1] <= '9' {
		return 0 // a tag cannot start with a digit
	}
	tag := sql[start : j+1]
	k := strings.Index(sql[j+1:], tag)
	if k < 0 {
		return len(sql)
	}
	return j + 1 + k + len(tag)
}

// literal writes one value as SQL text.
func literal(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "NULL", nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	case int64:
		return signed(strconv.FormatInt(x, 10)), nil
	case int:
		return signed(strconv.Itoa(x)), nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return "'" + strconv.FormatFloat(x, 'g', -1, 64) + "'", nil
		}
		return signed(strconv.FormatFloat(x, 'g', -1, 64)), nil
	case string:
		if strings.IndexByte(x, 0) >= 0 {
			return "", errors.New("text with a zero byte cannot be sent to PostgreSQL")
		}
		q := strings.ReplaceAll(x, "'", "''")
		if strings.Contains(q, `\`) {
			return "E'" + strings.ReplaceAll(q, `\`, `\\`) + "'", nil
		}
		return "'" + q + "'", nil
	}
	return "", fmt.Errorf("a value of type %T cannot be used as an argument", v)
}

// signed spaces a number and puts a negative one in brackets, so it can never
// join the text next to it: "x -$1" must not become the comment "x --5".
func signed(s string) string {
	if strings.HasPrefix(s, "-") {
		return " (" + s + ") "
	}
	return " " + s + " "
}
