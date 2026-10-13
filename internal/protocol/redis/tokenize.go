package redis

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vegaload/vegaload/internal/protocol/sqlcommon"
)

// command is one Redis command. name is the first token in lower case.
// argv is every argument that will be sent, after placeholders are filled.
type command struct {
	name string
	argv []string
}

const maxCommands = 10000

// parseCommands reads a redis-cli style body. One command per line. A ?
// that is its own unquoted token is replaced by the next args value, as one
// argument. Quoted "?" is the two characters quote, question, quote in the
// source and the one character ? on the wire: it is not a placeholder.
func parseCommands(body string, rawArgs string, hasArgs bool) ([]command, error) {
	var args []any
	if hasArgs {
		var err error
		args, err = sqlcommon.ParseArgs("redis", rawArgs)
		if err != nil {
			return nil, err
		}
	}
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var cmds []command
	used := 0
	for _, line := range lines {
		toks, err := tokenizeLine(line)
		if err != nil {
			return nil, err
		}
		if len(toks) == 0 {
			continue
		}
		if toks[0].place {
			return nil, fmt.Errorf("redis: a command name cannot be a placeholder")
		}
		name := strings.ToLower(toks[0].text)
		if isContainer(name) && len(toks) > 1 && toks[1].place {
			return nil, fmt.Errorf("redis: a subcommand cannot be a placeholder")
		}
		argv := make([]string, len(toks))
		for i, tok := range toks {
			if !tok.place {
				argv[i] = tok.text
				continue
			}
			if used >= len(args) {
				return nil, fmt.Errorf("redis: the commands have more ? placeholders than args (args has %d values)", len(args))
			}
			text, err := argText(used, args[used])
			if err != nil {
				return nil, err
			}
			argv[i] = text
			used++
		}
		cmds = append(cmds, command{name: name, argv: argv})
	}
	if len(cmds) == 0 {
		return nil, fmt.Errorf("redis: the command is required (pass it with -body)")
	}
	if used < len(args) {
		return nil, fmt.Errorf("redis: args has %d values but the commands have only %d ? placeholders", len(args), used)
	}
	if len(cmds) > maxCommands {
		return nil, fmt.Errorf("redis: a pipeline is limited to %d commands", maxCommands)
	}
	return cmds, nil
}

// SplitTokens splits a redis-cli body into one token list per command line.
// Blank lines and comments are left out. A quoted token is returned as the
// text it stands for, so a later join can quote it again. An unquoted ?
// is the text "?", not an args placeholder, until parseCommands sees it.
func SplitTokens(body string) ([][]string, error) {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var out [][]string
	for _, line := range lines {
		toks, err := tokenizeLine(line)
		if err != nil {
			return nil, err
		}
		if len(toks) == 0 {
			continue
		}
		row := make([]string, len(toks))
		for i, tok := range toks {
			row[i] = tok.text
		}
		out = append(out, row)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("redis: the command is required (pass it with -body)")
	}
	return out, nil
}

// JoinTokens writes lines of tokens as one redis-cli body. A token that
// parseCommands would split, or would read as an args placeholder, is quoted
// so the text comes back as one argument.
func JoinTokens(lines [][]string) string {
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		for j, tok := range line {
			if j > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(quoteToken(tok))
		}
	}
	return b.String()
}

func quoteToken(tok string) string {
	if tok != "" && tok != "?" && !tokenNeedsQuote(tok) {
		return tok
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		switch c {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				fmt.Fprintf(&b, "\\x%02x", c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func tokenNeedsQuote(tok string) bool {
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		if c <= ' ' || c == '"' || c == '\'' || c == '\\' {
			return true
		}
	}
	return false
}

func argText(i int, v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case int:
		return strconv.Itoa(x), nil
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64), nil
	case bool:
		return "", fmt.Errorf("redis: args[%d] is a boolean, pass it as a string or a number", i)
	case nil:
		return "", fmt.Errorf("redis: args[%d] is null, pass it as a string or a number", i)
	default:
		return "", fmt.Errorf("redis: args[%d] is a %T, pass it as a string or a number", i, v)
	}
}

type token struct {
	text  string
	place bool // an unquoted ? standing as its own argument
}

func tokenizeLine(line string) ([]token, error) {
	i := skipSpace(line, 0)
	if i >= len(line) || line[i] == '#' {
		return nil, nil
	}
	var toks []token
	for i < len(line) {
		i = skipSpace(line, i)
		if i >= len(line) {
			break
		}
		var tok token
		var err error
		switch line[i] {
		case '"':
			tok.text, i, err = readDouble(line, i+1)
		case '\'':
			tok.text, i, err = readSingle(line, i+1)
		default:
			tok, i = readPlain(line, i)
		}
		if err != nil {
			return nil, err
		}
		toks = append(toks, tok)
	}
	return toks, nil
}

func skipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

func readPlain(line string, i int) (token, int) {
	start := i
	for i < len(line) && line[i] != ' ' && line[i] != '\t' {
		i++
	}
	text := line[start:i]
	return token{text: text, place: text == "?"}, i
}

func readDouble(line string, i int) (string, int, error) {
	var b strings.Builder
	for i < len(line) {
		c := line[i]
		if c == '\\' {
			if i+1 >= len(line) {
				return "", 0, fmt.Errorf("redis: a double-quoted string has a bad escape")
			}
			switch line[i+1] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'b':
				b.WriteByte('\b')
			case 'a':
				b.WriteByte('\a')
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			case 'x':
				if i+3 >= len(line) {
					return "", 0, fmt.Errorf("redis: a double-quoted string has a bad escape")
				}
				n, err := strconv.ParseUint(line[i+2:i+4], 16, 8)
				if err != nil {
					return "", 0, fmt.Errorf("redis: a double-quoted string has a bad escape")
				}
				b.WriteByte(byte(n))
				i += 4
				continue
			default:
				return "", 0, fmt.Errorf("redis: a double-quoted string has a bad escape")
			}
			i += 2
			continue
		}
		if c == '"' {
			return afterQuote(line, b.String(), i+1)
		}
		b.WriteByte(c)
		i++
	}
	return "", 0, fmt.Errorf("redis: a quote is not closed")
}

func readSingle(line string, i int) (string, int, error) {
	var b strings.Builder
	for i < len(line) {
		c := line[i]
		if c == '\\' && i+1 < len(line) && line[i+1] == '\'' {
			b.WriteByte('\'')
			i += 2
			continue
		}
		if c == '\'' {
			return afterQuote(line, b.String(), i+1)
		}
		b.WriteByte(c)
		i++
	}
	return "", 0, fmt.Errorf("redis: a quote is not closed")
}

// afterQuote checks that a closing quote ends the argument. "ab"cd is an
// error: the quote has to be followed by a space, a tab, or the end of the line.
func afterQuote(line, text string, i int) (string, int, error) {
	if i < len(line) && line[i] != ' ' && line[i] != '\t' {
		return "", 0, fmt.Errorf("redis: a closing quote must be followed by a space")
	}
	return text, i, nil
}
