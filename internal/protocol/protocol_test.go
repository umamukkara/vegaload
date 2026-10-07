package protocol

import (
	"testing"
	"time"
)

func TestTargetOption(t *testing.T) {
	tg := Target{Options: map[string]string{"read": "64", "wait": "250ms", "mode": "publish"}}

	if got := tg.Option("mode", "x"); got != "publish" {
		t.Errorf("Option(mode) = %q", got)
	}
	if got := tg.Option("missing", "x"); got != "x" {
		t.Errorf("Option(missing) = %q, want the default", got)
	}
	if n, err := tg.OptionInt("read", 0); err != nil || n != 64 {
		t.Errorf("OptionInt(read) = %d, %v", n, err)
	}
	if n, err := tg.OptionInt("missing", 7); err != nil || n != 7 {
		t.Errorf("OptionInt(missing) = %d, %v", n, err)
	}
	if d, err := tg.OptionDuration("wait", 0); err != nil || d != 250*time.Millisecond {
		t.Errorf("OptionDuration(wait) = %v, %v", d, err)
	}
	if d, err := tg.OptionDuration("missing", time.Second); err != nil || d != time.Second {
		t.Errorf("OptionDuration(missing) = %v, %v", d, err)
	}
}

func TestTargetOption_BadValuesAreErrors(t *testing.T) {
	tg := Target{Options: map[string]string{"read": "lots", "wait": "soon"}}
	if _, err := tg.OptionInt("read", 0); err == nil {
		t.Error("OptionInt on text: want an error")
	}
	if _, err := tg.OptionDuration("wait", 0); err == nil {
		t.Error("OptionDuration on text: want an error")
	}
}

func TestTargetOption_NilMap(t *testing.T) {
	var tg Target
	if got := tg.Option("a", "d"); got != "d" {
		t.Errorf("nil Options: got %q", got)
	}
	if n, err := tg.OptionInt("a", 3); err != nil || n != 3 {
		t.Errorf("nil Options: got %d, %v", n, err)
	}
}
