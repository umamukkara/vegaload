package mysql

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// sqlMode is the part of the server's sql_mode that changes how the
// scanner reads the text. It is known only after the first connection.
type sqlMode struct {
	noBackslash bool
	ansiQuotes  bool
}

// scanInfo is what the scanner learned about the text, besides the
// rewritten SQL. keywords holds the first word of each statement, in
// lower case, in order. A statement that starts with "(" has that as
// its keyword.
type scanInfo struct {
	statements int
	keywords   []string
}

// scan copies sql and, when args is not nil, puts those values in place
// of ? placeholders. args nil means the text is left as it is, and the
// server judges any ?. A placeholder is only replaced where the server
// would read it as one: not inside a string, an identifier, or a comment.
//
// Executable comments (/*! ... */ and /*+ ... */) are real SQL. The
// opener, including a version such as /*!50100, is copied, the inside is
// scanned, and the closing */ is copied.
func scan(sql string, mode sqlMode, args []any) (string, scanInfo, error) {
	var out strings.Builder
	out.Grow(len(sql) + 16*len(args))
	var info scanInfo
	substitute := args != nil
	argi := 0
	placeholders := 0

	nonempty := false
	atStart := true
	gotKW := false
	kw := ""
	finish := func() {
		if !nonempty {
			return
		}
		info.statements++
		if gotKW {
			info.keywords = append(info.keywords, kw)
		} else {
			info.keywords = append(info.keywords, "")
		}
		nonempty = false
		atStart = true
		gotKW = false
		kw = ""
	}

	i := 0
	n := len(sql)
	for i < n {
		c := sql[i]
		switch {
		case c == '\'':
			j := scanQuote(sql, i, '\'', !mode.noBackslash)
			out.WriteString(sql[i:j])
			noteCode(&nonempty, &atStart, &gotKW)
			i = j
		case c == '"':
			j := scanQuote(sql, i, '"', !mode.ansiQuotes && !mode.noBackslash)
			out.WriteString(sql[i:j])
			noteCode(&nonempty, &atStart, &gotKW)
			i = j
		case c == '`':
			j := scanQuote(sql, i, '`', false)
			out.WriteString(sql[i:j])
			noteCode(&nonempty, &atStart, &gotKW)
			i = j
		case c == '-' && i+1 < n && sql[i+1] == '-':
			if i+2 == n || sql[i+2] <= ' ' || sql[i+2] == 0x7f {
				j := strings.IndexByte(sql[i:], '\n')
				if j < 0 {
					j = n
				} else {
					j += i + 1 // keep the newline
				}
				out.WriteString(sql[i:j])
				i = j
				continue
			}
			out.WriteByte(c)
			noteCode(&nonempty, &atStart, &gotKW)
			i++
		case c == '#':
			j := strings.IndexByte(sql[i:], '\n')
			if j < 0 {
				j = n
			} else {
				j += i + 1
			}
			out.WriteString(sql[i:j])
			i = j
		case c == '/' && i+1 < n && sql[i+1] == '*':
			if i+2 < n && (sql[i+2] == '!' || sql[i+2] == '+') {
				j := i + 3
				if sql[i+2] == '!' {
					for j < n && sql[j] >= '0' && sql[j] <= '9' {
						j++
					}
				}
				out.WriteString(sql[i:j])
				i = j
				continue
			}
			j := blockCommentEnd(sql, i)
			out.WriteString(sql[i:j])
			i = j
		case c == '*' && i+1 < n && sql[i+1] == '/':
			// The end of an executable comment. It is not SQL.
			out.WriteString("*/")
			i += 2
		case c == ';':
			finish()
			out.WriteByte(c)
			i++
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f':
			out.WriteByte(c)
			i++
		case c == '?':
			if !substitute {
				out.WriteByte(c)
				noteCode(&nonempty, &atStart, &gotKW)
				i++
				break
			}
			if argi >= len(args) {
				return "", info, fmt.Errorf("mysql: the SQL has more ? placeholders than args (args has %d values)", len(args))
			}
			lit, err := literal(args[argi])
			if err != nil {
				return "", info, err
			}
			out.WriteString(lit)
			argi++
			placeholders++
			noteCode(&nonempty, &atStart, &gotKW)
			i++
		case atStart && c == '(':
			out.WriteByte(c)
			nonempty = true
			atStart = false
			gotKW = true
			kw = "("
			i++
		case atStart && isLetter(c):
			j := i + 1
			for j < n && isWord(sql[j]) {
				j++
			}
			kw = strings.ToLower(sql[i:j])
			gotKW = true
			nonempty = true
			atStart = false
			out.WriteString(sql[i:j])
			i = j
		default:
			out.WriteByte(c)
			noteCode(&nonempty, &atStart, &gotKW)
			i++
		}
	}
	finish()
	if substitute && argi != len(args) {
		return "", info, fmt.Errorf("mysql: args has %d values but the SQL has only %d ? placeholders", len(args), placeholders)
	}
	return out.String(), info, nil
}

// noteCode marks that the statement holds real SQL, and that the first
// keyword, if any, has already been passed.
func noteCode(nonempty, atStart, gotKW *bool) {
	*nonempty = true
	if *atStart {
		*atStart = false
		*gotKW = false
	}
}

func isLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isWord(c byte) bool {
	return isLetter(c) || (c >= '0' && c <= '9') || c == '_'
}

// scanQuote returns the index just after the quoted text that starts at
// sql[start]. A doubled quote is an escaped quote. With backslash true, a
// backslash also escapes the next byte. An unclosed quote runs to the end.
func scanQuote(sql string, start int, quote byte, backslash bool) int {
	i := start + 1
	for i < len(sql) {
		switch {
		case backslash && sql[i] == '\\':
			i += 2
			if i > len(sql) {
				return len(sql)
			}
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

// blockCommentEnd returns the index just after the /* ... */ comment that
// starts at sql[start]. MySQL block comments do not nest.
func blockCommentEnd(sql string, start int) int {
	j := strings.Index(sql[start+2:], "*/")
	if j < 0 {
		return len(sql)
	}
	return start + 2 + j + 2
}

// literal writes one value as SQL text that means the same thing whether
// or not the server treats a backslash as an escape.
func literal(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "NULL", nil
	case bool:
		if x {
			return "TRUE", nil
		}
		return "FALSE", nil
	case int64:
		return signed(strconv.FormatInt(x, 10)), nil
	case int:
		return signed(strconv.Itoa(x)), nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return "", errors.New("mysql: NaN and Inf cannot be used as an argument")
		}
		return signed(strconv.FormatFloat(x, 'g', -1, 64)), nil
	case string:
		if plainString(x) {
			return "'" + strings.ReplaceAll(x, "'", "''") + "'", nil
		}
		return "_utf8mb4 X'" + hex.EncodeToString([]byte(x)) + "'", nil
	}
	return "", fmt.Errorf("mysql: a value of type %T cannot be used as an argument", v)
}

// plainString reports whether s can be written in single quotes. A
// backslash, a NUL, a control character, or bytes that are not valid
// UTF-8 use the hex form instead.
func plainString(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r == '\\' || r == 0 || r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// signed spaces a number and puts a negative one in brackets, so it can
// never join the dash before it: "x -?" must not become the comment "x --5".
func signed(s string) string {
	if strings.HasPrefix(s, "-") {
		return " (" + s + ") "
	}
	return " " + s + " "
}

func returnsRows(kw string) bool {
	switch kw {
	case "select", "show", "describe", "desc", "explain", "with", "call",
		"values", "table", "handler", "help", "analyze", "optimize",
		"check", "checksum", "repair", "(":
		return true
	}
	return false
}

// preparedRefused reports a statement prepared mode cannot run. Those
// statements change the connection, and a prepared call does not keep one.
func preparedRefused(kw string) bool {
	return changesSession(kw) || startsTx(kw)
}

func changesSession(kw string) bool {
	switch kw {
	case "set", "use", "lock", "xa", "unlock":
		return true
	}
	return false
}

func startsTx(kw string) bool {
	return kw == "begin" || kw == "start"
}
