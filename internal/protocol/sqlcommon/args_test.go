package sqlcommon

import "testing"

func TestParseArgs(t *testing.T) {
	got, err := ParseArgs("postgres", `[1, 2.5, "x", true, null, {"a": [1]}]`)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != int64(1) || got[1] != 2.5 || got[2] != "x" || got[3] != true || got[4] != nil || got[5] != `{"a":[1]}` {
		t.Errorf("args = %#v", got)
	}
	// A big whole number is not rounded through a float.
	got, err = ParseArgs("postgres", `[9007199254740993]`)
	if err != nil || got[0] != int64(9007199254740993) {
		t.Errorf("big number = %#v %v", got, err)
	}
	if _, err := ParseArgs("mysql", `{"a":1}`); err == nil || err.Error()[:6] != "mysql:" {
		t.Errorf("object: %v", err)
	}
}
