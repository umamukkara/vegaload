package xfercommon

import (
	"bytes"
	"io"
	"testing"
)

func TestParseSize(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
	}{
		{"0", 0},
		{"1024", 1024},
		{"10KiB", 10 << 10},
		{"1MiB", 1 << 20},
		{"1GiB", 1 << 30},
	} {
		got, err := ParseSize(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", tc.in, got, err, tc.want)
		}
	}
	if _, err := ParseSize("10MB"); err == nil {
		t.Error("10MB should be refused")
	}
	if _, err := ParseSize("5GiB"); err == nil {
		t.Error("5GiB should be refused")
	}
}

func TestCheckTextAndPath(t *testing.T) {
	if err := CheckText("path", "a\r\nDELE b"); err == nil || bytes.Contains([]byte(err.Error()), []byte("DELE")) {
		t.Fatalf("line break: %v", err)
	}
	if err := CheckPath(string(bytes.Repeat([]byte("a"), MaxPath+1))); err == nil {
		t.Fatal("a long path was accepted")
	}
}

func TestReplaceID(t *testing.T) {
	if got := ReplaceID("/upload/vl-{id}.bin", "s-1"); got != "/upload/vl-s-1.bin" {
		t.Fatal(got)
	}
}

func TestFillIsDeterministicAndBounded(t *testing.T) {
	a, err := FillReader("random", 1000)
	if err != nil {
		t.Fatal(err)
	}
	b, err := FillReader("random", 1000)
	if err != nil {
		t.Fatal(err)
	}
	ab, _ := io.ReadAll(a)
	bb, _ := io.ReadAll(b)
	if !bytes.Equal(ab, bb) || len(ab) != 1000 {
		t.Fatalf("random fill len %d", len(ab))
	}
	z, _ := FillReader("zero", 8)
	zb, _ := io.ReadAll(z)
	if !bytes.Equal(zb, make([]byte, 8)) {
		t.Fatalf("zero fill %v", zb)
	}
	tx, _ := FillReader("text", 80)
	tb, _ := io.ReadAll(tx)
	if len(tb) != 80 || !bytes.Contains(tb, []byte("vegaload")) {
		t.Fatalf("text fill %q", tb)
	}
}

func TestValidSHA256(t *testing.T) {
	if !ValidSHA256("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef") {
		t.Fatal("hex should be valid")
	}
	if ValidSHA256("zz") || ValidSHA256("abcd") {
		t.Fatal("bad hex was accepted")
	}
}
