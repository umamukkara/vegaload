// Package xfercommon holds helpers for a file transfer that do not know
// which protocol is moving the bytes. FTP uses them. A later protocol can
// use the same ones.
package xfercommon

import (
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"strconv"
	"strings"
	"unicode"
)

const (
	// MaxSize is the most a generated payload may be: 4 GiB.
	MaxSize = 4 << 30
	// MaxPath is the most bytes a remote path may be.
	MaxPath = 4096
	// MaxBody is the most a caller-supplied body may be.
	MaxBody = 64 << 20
)

// ParseSize reads a size such as 1024, 10KiB, 10MiB or 1GiB.
// The unit is 1024-based. A bare number is a count of bytes.
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("a size is required")
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("size %q: want a number such as 1024 or 10MiB", s)
	}
	n, err := strconv.ParseInt(s[:i], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("size %q: want a number such as 1024 or 10MiB", s)
	}
	unit := strings.TrimSpace(s[i:])
	var mul int64 = 1
	switch unit {
	case "", "B", "b":
	case "KiB", "kib":
		mul = 1 << 10
	case "MiB", "mib":
		mul = 1 << 20
	case "GiB", "gib":
		mul = 1 << 30
	default:
		return 0, fmt.Errorf("size %q: want a unit of B, KiB, MiB or GiB", s)
	}
	if n > MaxSize/mul {
		return 0, fmt.Errorf("size %q is above 4GiB", s)
	}
	return n * mul, nil
}

// CheckText refuses a CR, LF or NUL in v. The error names the field and
// does not include the value.
func CheckText(field, v string) error {
	if strings.ContainsAny(v, "\r\n\x00") {
		return fmt.Errorf("%s must not contain a line break or a NUL", field)
	}
	return nil
}

// CheckPath is CheckText for a remote path, and it also limits the length.
func CheckPath(path string) error {
	if err := CheckText("path", path); err != nil {
		return err
	}
	if len(path) > MaxPath {
		return fmt.Errorf("path is %d bytes, the most is %d", len(path), MaxPath)
	}
	return nil
}

// ReplaceID replaces each {id} in s with id. Nothing else is substituted.
func ReplaceID(s, id string) string {
	return strings.ReplaceAll(s, "{id}", id)
}

// FillReader returns a reader of exactly n bytes. mode is random, zero or
// text. The same mode and n always produce the same bytes. The reader
// never holds those bytes all at once.
func FillReader(mode string, n int64) (io.Reader, error) {
	if n < 0 || n > MaxSize {
		return nil, fmt.Errorf("size must be from 0 to 4GiB")
	}
	switch mode {
	case "", "random":
		return &xorReader{n: n, s: 0x6a09e667f3bcc909}, nil
	case "zero":
		return io.LimitReader(zeroReader{}, n), nil
	case "text":
		return io.LimitReader(&textReader{}, n), nil
	default:
		return nil, fmt.Errorf("fill %q, want random, zero or text", mode)
	}
}

// xorReader is a fast seeded byte stream. It is not a secure generator.
type xorReader struct {
	n int64
	s uint64
}

func (r *xorReader) Read(p []byte) (int, error) {
	if r.n <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.n {
		p = p[:r.n]
	}
	for i := range p {
		r.s ^= r.s << 13
		r.s ^= r.s >> 7
		r.s ^= r.s << 17
		if r.s == 0 {
			r.s = 0x6a09e667f3bcc909
		}
		p[i] = byte(r.s)
	}
	r.n -= int64(len(p))
	return len(p), nil
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

type textReader struct {
	i int
}

var textLine = []byte("vegaload file transfer line\n")

func (r *textReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = textLine[r.i%len(textLine)]
		r.i++
	}
	return len(p), nil
}

// CountingReader counts the bytes read from r.
type CountingReader struct {
	R io.Reader
	N int64
}

func (c *CountingReader) Read(p []byte) (int, error) {
	n, err := c.R.Read(p)
	c.N += int64(n)
	return n, err
}

// CountingWriter counts the bytes written to w.
type CountingWriter struct {
	W io.Writer
	N int64
}

func (c *CountingWriter) Write(p []byte) (int, error) {
	n, err := c.W.Write(p)
	c.N += int64(n)
	return n, err
}

// NewHash returns a SHA-256 hash.
func NewHash() hash.Hash { return sha256.New() }

// HexDigest is the hex encoding of sum, lowercase.
func HexDigest(sum []byte) string {
	const hexd = "0123456789abcdef"
	b := make([]byte, len(sum)*2)
	for i, v := range sum {
		b[i*2] = hexd[v>>4]
		b[i*2+1] = hexd[v&0x0f]
	}
	return string(b)
}

// ValidSHA256 reports whether s is 64 hex characters.
func ValidSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !unicode.Is(unicode.ASCII_Hex_Digit, r) {
			return false
		}
	}
	return true
}
