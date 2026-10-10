package redis

import (
	"strings"
	"testing"
)

func TestTokenize_Table(t *testing.T) {
	cases := []struct {
		body string
		want [][]string
	}{
		{"GET user:42", [][]string{{"GET", "user:42"}}},
		{"  get\tuser:42  ", [][]string{{"get", "user:42"}}},
		{"PING\n\n# a comment\nPING", [][]string{{"PING"}, {"PING"}}},
		{`SET k "a b"`, [][]string{{"SET", "k", "a b"}}},
		{`"a\nb\t\r\"\\"`, [][]string{{"a\nb\t\r\"\\"}}},
		{`"\x41\x0a"`, [][]string{{"A\n"}}},
		{`'it\'s'`, [][]string{{"it's"}}},
		{`'a\n'`, [][]string{{`a\n`}}},
		{"GET \"?\"", [][]string{{"GET", "?"}}},
	}
	for _, c := range cases {
		got, err := parseCommands(c.body, "", false)
		if err != nil {
			t.Errorf("%q: %v", c.body, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("%q: %d commands, want %d", c.body, len(got), len(c.want))
			continue
		}
		for i, cmd := range got {
			if strings.Join(cmd.argv, "\x00") != strings.Join(c.want[i], "\x00") {
				t.Errorf("%q command %d = %#v, want %#v", c.body, i, cmd.argv, c.want[i])
			}
		}
	}
}

func TestTokenize_Errors(t *testing.T) {
	cases := []struct {
		body string
		args string
		has  bool
		want string
	}{
		{`GET "ab`, "", false, "quote is not closed"},
		{`GET 'ab`, "", false, "quote is not closed"},
		{`GET "ab"cd`, "", false, "followed by a space"},
		{`GET "\q"`, "", false, "bad escape"},
		{`GET "\x"`, "", false, "bad escape"},
		{`GET "\x4"`, "", false, "bad escape"},
		{`GET "\xGG"`, "", false, "bad escape"},
		{"? foo", "", false, "command name cannot be a placeholder"},
		{"CONFIG ?", "", false, "subcommand cannot be a placeholder"},
		{"", "", false, "the command is required"},
		{"# only a comment\n", "", false, "the command is required"},
		{"GET ?", "", false, "more ? placeholders than args (args has 0 values)"},
		{"GET ?", `[]`, true, "more ? placeholders than args (args has 0 values)"},
		{"GET ?", `[1]`, true, ""},
		{"PING", `[1, 2]`, true, "args has 2 values but the commands have only 0 ? placeholders"},
		{"GET ?\nGET ?", `[1]`, true, "more ? placeholders than args (args has 1 values)"},
		{"GET ?", `[true]`, true, "args[0] is a boolean"},
		{"GET ?", `[null]`, true, "args[0] is null"},
	}
	for _, c := range cases {
		_, err := parseCommands(c.body, c.args, c.has)
		if c.want == "" {
			if err != nil {
				t.Errorf("%q: %v", c.body, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: got %v, want %q", c.body, err, c.want)
		}
	}
}

func TestTokenize_PlaceholderIsOneArgument(t *testing.T) {
	got, err := parseCommands("SET ? ?", `["a b", "say \"hi\"\r\nFLUSHALL"]`, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].argv) != 3 {
		t.Fatalf("%#v", got)
	}
	if got[0].argv[1] != "a b" || got[0].argv[2] != "say \"hi\"\r\nFLUSHALL" {
		t.Fatalf("%#v", got[0].argv)
	}
}

func TestTokenize_PipelineCap(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxCommands+1; i++ {
		b.WriteString("PING\n")
	}
	if _, err := parseCommands(b.String(), "", false); err == nil || !strings.Contains(err.Error(), "10000") {
		t.Fatal(err)
	}
}

func TestTokenize_ArgsTypes(t *testing.T) {
	got, err := parseCommands("MSET ? ? ? ?", `[1, 1.5, "x", 9007199254740993]`, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"MSET", "1", "1.5", "x", "9007199254740993"}
	if strings.Join(got[0].argv, ",") != strings.Join(want, ",") {
		t.Fatalf("%#v", got[0].argv)
	}
}
