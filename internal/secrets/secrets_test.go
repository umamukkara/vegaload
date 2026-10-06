package secrets

import "testing"

func TestRedact_ReplacesEveryRegisteredValue(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	Register("hunter2")
	Register("tok-abc")
	got := Redact("login hunter2 then Bearer tok-abc and hunter2 again")
	want := "login [redacted] then Bearer [redacted] and [redacted] again"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRedact_LongestFirstAndNoSecretsLeavesTextAlone(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	if got := Redact("nothing here"); got != "nothing here" {
		t.Fatalf("got %q", got)
	}
	Register("abc")
	Register("abcdef")
	if got := Redact("x abcdef y"); got != "x [redacted] y" {
		t.Fatalf("a secret containing another must go whole, got %q", got)
	}
}

func TestRegister_IgnoresEmptyAndDuplicates(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	Register("")
	Register("a1b2")
	Register("a1b2")
	if got := Redact("keep this a1b2"); got != "keep this [redacted]" {
		t.Fatalf("got %q", got)
	}
	if len(values) != 1 {
		t.Fatalf("want 1 value, got %v", values)
	}
}
