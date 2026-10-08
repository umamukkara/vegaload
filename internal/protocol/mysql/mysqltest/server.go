// Package mysqltest is a small fake MySQL server for tests, in this
// package and in others. It speaks the text query protocol only
// (handshake v10 and mysql_native_password), so a test must use the
// driver's default query_mode (simple). It does not parse SQL: the test
// gives it a Handler that answers each SQL text.
package mysqltest

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
)

// Column is one column of a Result. Type is a server type name such as
// "BIGINT", "VARCHAR" or "UNSIGNED BIGINT". Use Text and Int for the usual ones.
type Column struct {
	Name string
	Type string
}

// Text and Int make a VARCHAR column and a BIGINT column.
func Text(name string) Column { return Column{Name: name, Type: "VARCHAR"} }

// Int makes a BIGINT column.
func Int(name string) Column { return Column{Name: name, Type: "BIGINT"} }

// Result is the answer to one statement. A cell is printed as text, or nil
// for NULL. Set ErrNumber to make the statement fail. The statements after
// it in the same reply are not sent.
type Result struct {
	Columns      []Column
	Rows         [][]any
	Affected     int64
	LastInsertID int64
	ErrNumber    uint16
	ErrState     string
	ErrMessage   string
}

// Handler answers one query text. It returns one Result for each statement
// in the text. No result at all means an OK packet with nothing changed.
type Handler func(sql string) []Result

// Login is one client that finished the handshake.
type Login struct {
	User string
	DB   string
}

// Server is a running fake server.
type Server struct {
	ln       net.Listener
	handler  Handler
	password string

	mu      sync.Mutex
	conns   int
	queries []string
	inits   []string
	logins  []Login
	mode    string
}

// Start starts a server on a free local port and stops it when the test ends.
func Start(t testing.TB, h Handler) *Server {
	t.Helper()
	return start(t, h, "")
}

// StartWithPassword is Start, but a client must log in with this password.
func StartWithPassword(t testing.TB, h Handler, password string) *Server {
	t.Helper()
	return start(t, h, password)
}

func start(t testing.TB, h Handler, password string) *Server {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{ln: ln, handler: h, password: password}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

// URL is the target to give the driver.
func (s *Server) URL() string { return "mysql://" + s.ln.Addr().String() + "/testdb" }

// ConnCount is how many connections have logged in.
func (s *Server) ConnCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

// Queries are the SQL texts received, in order, without the session setup
// the driver sends on each new connection.
func (s *Server) Queries() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.queries...)
}

// Inits are the session statements the client sent, such as the read-only
// setting and the sql_mode query. They are not in Queries.
func (s *Server) Inits() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.inits...)
}

// Logins are the user and database of each connection.
func (s *Server) Logins() []Login {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Login(nil), s.logins...)
}

// SetSQLMode sets the text SELECT @@SESSION.sql_mode returns.
func (s *Server) SetSQLMode(mode string) {
	s.mu.Lock()
	s.mode = mode
	s.mu.Unlock()
}

func (s *Server) sqlMode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

func (s *Server) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	bw := bufio.NewWriter(c)

	salt := make([]byte, 20)
	if _, err := rand.Read(salt); err != nil {
		return
	}
	if writePacket(bw, 0, greeting(salt)) != nil || bw.Flush() != nil {
		return
	}
	seq, payload, err := readPacket(br)
	if err != nil {
		return
	}
	// A client that insists on TLS sends a short request. This server has
	// no TLS, so the connection is closed and the client can fall back.
	if len(payload) == 32 && binary.LittleEndian.Uint32(payload)&(1<<11) != 0 {
		return
	}
	user, db, auth, err := parseHandshake(payload)
	if err != nil {
		return
	}
	if s.password != "" && !bytesEqual(auth, scramble(salt, s.password)) {
		_ = writePacket(bw, seq+1, errPacket(1045, "28000", "Access denied"))
		_ = bw.Flush()
		return
	}
	if writePacket(bw, seq+1, okPacket(0, 0, statusAutocommit)) != nil || bw.Flush() != nil {
		return
	}
	s.mu.Lock()
	s.conns++
	s.logins = append(s.logins, Login{User: user, DB: db})
	s.mu.Unlock()

	for {
		_, payload, err = readPacket(br)
		if err != nil {
			return
		}
		if len(payload) == 0 {
			return
		}
		switch payload[0] {
		case comQuit:
			return
		case comQuery:
			if s.answer(bw, string(payload[1:])) != nil {
				return
			}
		case comPing, comInitDB, comReset, comSetOption:
			if writePacket(bw, 1, okPacket(0, 0, statusAutocommit)) != nil || bw.Flush() != nil {
				return
			}
		default:
			if writePacket(bw, 1, errPacket(1047, "HY000", "Unknown command")) != nil || bw.Flush() != nil {
				return
			}
		}
	}
}

func (s *Server) answer(bw *bufio.Writer, sql string) error {
	c := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sql), ";"))
	switch {
	case strings.EqualFold(c, "SET SESSION TRANSACTION READ ONLY"), strings.EqualFold(c, "SELECT @@SESSION.sql_mode"):
		s.mu.Lock()
		s.inits = append(s.inits, c)
		s.mu.Unlock()
		var err error
		if strings.HasPrefix(strings.ToUpper(c), "SELECT") {
			err = writeResults(bw, []Result{{
				Columns: []Column{Text("@@SESSION.sql_mode")},
				Rows:    [][]any{{s.sqlMode()}},
			}})
		} else {
			err = writeResults(bw, []Result{{}})
		}
		if err != nil {
			return err
		}
		return bw.Flush()
	}
	s.mu.Lock()
	s.queries = append(s.queries, sql)
	s.mu.Unlock()
	results := s.handler(sql)
	if len(results) == 0 {
		results = []Result{{}}
	}
	if err := writeResults(bw, results); err != nil {
		return err
	}
	return bw.Flush()
}

const (
	statusAutocommit  uint16 = 0x0002
	statusMoreResults uint16 = 0x0008

	comQuit      byte = 0x01
	comInitDB    byte = 0x02
	comQuery     byte = 0x03
	comPing      byte = 0x0e
	comSetOption byte = 0x1b
	comReset     byte = 0x1f
)

func greeting(salt []byte) []byte {
	// Protocol 4.1 greeting. No SSL capability, so a client that prefers
	// TLS stays on plain text.
	const caps uint32 = 1 | // long password
		1<<1 | // found rows
		1<<2 | // long flag
		1<<3 | // connect with db
		1<<9 | // protocol 41
		1<<13 | // transactions
		1<<15 | // secure connection
		1<<16 | // multi statements
		1<<17 | // multi results
		1<<19 // plugin auth
	var b []byte
	b = append(b, 10)
	b = append(b, "5.7.40"...)
	b = append(b, 0)
	b = append(b, 1, 0, 0, 0) // connection id
	b = append(b, salt[:8]...)
	b = append(b, 0)
	b = append(b, byte(caps&0xff), byte((caps>>8)&0xff))
	b = append(b, 45) // utf8mb4_general_ci
	b = append(b, byte(statusAutocommit), byte(statusAutocommit>>8))
	b = append(b, byte((caps>>16)&0xff), byte((caps>>24)&0xff))
	b = append(b, 21)                  // auth data length
	b = append(b, make([]byte, 10)...) // reserved
	b = append(b, salt[8:20]...)
	b = append(b, 0)
	b = append(b, "mysql_native_password"...)
	b = append(b, 0)
	return b
}

func parseHandshake(p []byte) (user, db string, auth []byte, err error) {
	if len(p) < 32 {
		return "", "", nil, io.ErrUnexpectedEOF
	}
	caps := binary.LittleEndian.Uint32(p[0:4])
	rest := p[32:]
	user, rest, err = readCString(rest)
	if err != nil {
		return "", "", nil, err
	}
	if caps&(1<<21) != 0 && len(rest) > 0 && rest[0] >= 251 {
		auth, rest, err = readLenenc(rest)
	} else {
		if len(rest) < 1 {
			return "", "", nil, io.ErrUnexpectedEOF
		}
		n := int(rest[0])
		if len(rest) < 1+n {
			return "", "", nil, io.ErrUnexpectedEOF
		}
		auth = append([]byte(nil), rest[1:1+n]...)
		rest = rest[1+n:]
	}
	if caps&8 != 0 { // connect with database
		db, rest, err = readCString(rest)
		if err != nil {
			return "", "", nil, err
		}
	}
	return user, db, auth, nil
}

func scramble(salt []byte, password string) []byte {
	if password == "" {
		return nil
	}
	h := sha1.New()
	_, _ = io.WriteString(h, password)
	stage1 := h.Sum(nil)
	h.Reset()
	h.Write(stage1)
	hash := h.Sum(nil)
	h.Reset()
	h.Write(salt)
	h.Write(hash)
	out := h.Sum(nil)
	for i := range out {
		out[i] ^= stage1[i]
	}
	return out
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var d byte
	for i := range a {
		d |= a[i] ^ b[i]
	}
	return d == 0
}

func writeResults(bw *bufio.Writer, results []Result) error {
	seq := byte(1)
	for i, r := range results {
		more := i < len(results)-1
		status := statusAutocommit
		if more {
			status |= statusMoreResults
		}
		if r.ErrNumber != 0 {
			return writePacket(bw, seq, errPacket(r.ErrNumber, r.ErrState, r.ErrMessage))
		}
		if len(r.Columns) == 0 {
			if err := writePacket(bw, seq, okPacket(uint64(r.Affected), uint64(r.LastInsertID), status)); err != nil {
				return err
			}
			seq++
			continue
		}
		if err := writePacket(bw, seq, appendLenenc(nil, uint64(len(r.Columns)))); err != nil {
			return err
		}
		seq++
		for _, col := range r.Columns {
			if err := writePacket(bw, seq, columnDef(col)); err != nil {
				return err
			}
			seq++
		}
		if err := writePacket(bw, seq, eofPacket(statusAutocommit)); err != nil {
			return err
		}
		seq++
		for _, row := range r.Rows {
			if err := writePacket(bw, seq, rowPacket(row, len(r.Columns))); err != nil {
				return err
			}
			seq++
		}
		if err := writePacket(bw, seq, eofPacket(status)); err != nil {
			return err
		}
		seq++
	}
	return nil
}

func columnDef(col Column) []byte {
	name := strings.ToUpper(strings.TrimSpace(col.Type))
	var flags uint16
	if strings.HasPrefix(name, "UNSIGNED ") {
		flags |= 32
		name = strings.TrimPrefix(name, "UNSIGNED ")
	}
	ft, charset := byte(0xfd), byte(45) // VARCHAR, utf8mb4
	switch name {
	case "TINYINT":
		ft = 0x01
	case "SMALLINT":
		ft = 0x02
	case "INT":
		ft = 0x03
	case "FLOAT":
		ft = 0x04
	case "DOUBLE":
		ft = 0x05
	case "TIMESTAMP":
		ft = 0x07
	case "BIGINT":
		ft = 0x08
	case "MEDIUMINT":
		ft = 0x09
	case "DATE":
		ft = 0x0a
	case "TIME":
		ft = 0x0b
	case "DATETIME":
		ft = 0x0c
	case "YEAR":
		ft = 0x0d
	case "BIT":
		ft = 0x10
	case "JSON":
		ft = 0xf5
	case "DECIMAL":
		ft = 0xf6
	case "ENUM":
		ft = 0xf7
	case "SET":
		ft = 0xf8
	case "TEXT":
		ft = 0xfc
	case "BLOB":
		ft, charset = 0xfc, 63
	case "CHAR":
		ft = 0xfe
	case "BINARY":
		ft, charset = 0xfe, 63
	case "VARCHAR", "":
		ft = 0xfd
	}
	var b []byte
	for _, s := range []string{"def", "", "", "", col.Name, col.Name} {
		b = appendLenStr(b, s)
	}
	b = append(b, 0x0c, charset, 0)
	b = append(b, 0xff, 0xff, 0, 0)
	b = append(b, ft, byte(flags), byte(flags>>8), 0, 0, 0)
	return b
}

func rowPacket(row []any, n int) []byte {
	var b []byte
	for i := 0; i < n; i++ {
		var v any
		if i < len(row) {
			v = row[i]
		}
		if v == nil {
			b = append(b, 0xfb)
			continue
		}
		b = appendLenStr(b, fmt.Sprint(v))
	}
	return b
}

func okPacket(affected, last uint64, status uint16) []byte {
	b := []byte{0x00}
	b = appendLenenc(b, affected)
	b = appendLenenc(b, last)
	return append(b, byte(status), byte(status>>8), 0, 0)
}

func eofPacket(status uint16) []byte {
	return []byte{0xfe, 0, 0, byte(status), byte(status >> 8)}
}

func errPacket(code uint16, state, msg string) []byte {
	if len(state) != 5 {
		state = "HY000"
	}
	b := []byte{0xff, byte(code), byte(code >> 8), '#'}
	b = append(b, state...)
	return append(b, msg...)
}

func writePacket(w io.Writer, seq byte, payload []byte) error {
	hdr := []byte{byte(len(payload)), byte(len(payload) >> 8), byte(len(payload) >> 16), seq}
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readPacket(r io.Reader) (seq byte, payload []byte, err error) {
	var hdr [4]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := int(hdr[0]) | int(hdr[1])<<8 | int(hdr[2])<<16
	seq = hdr[3]
	payload = make([]byte, n)
	_, err = io.ReadFull(r, payload)
	return seq, payload, err
}

func appendLenenc(b []byte, n uint64) []byte {
	switch {
	case n < 251:
		return append(b, byte(n))
	case n < 1<<16:
		return append(b, 0xfc, byte(n), byte(n>>8))
	case n < 1<<24:
		return append(b, 0xfd, byte(n), byte(n>>8), byte(n>>16))
	default:
		b = append(b, 0xfe)
		for i := 0; i < 8; i++ {
			b = append(b, byte(n>>(8*uint(i))))
		}
		return b
	}
}

func appendLenStr(b []byte, s string) []byte {
	b = appendLenenc(b, uint64(len(s)))
	return append(b, s...)
}

func readCString(b []byte) (string, []byte, error) {
	i := 0
	for i < len(b) && b[i] != 0 {
		i++
	}
	if i >= len(b) {
		return "", nil, io.ErrUnexpectedEOF
	}
	return string(b[:i]), b[i+1:], nil
}

func readLenenc(b []byte) ([]byte, []byte, error) {
	if len(b) == 0 {
		return nil, nil, io.ErrUnexpectedEOF
	}
	var n uint64
	var rest []byte
	switch b[0] {
	case 0xfc:
		if len(b) < 3 {
			return nil, nil, io.ErrUnexpectedEOF
		}
		n = uint64(b[1]) | uint64(b[2])<<8
		rest = b[3:]
	case 0xfd:
		if len(b) < 4 {
			return nil, nil, io.ErrUnexpectedEOF
		}
		n = uint64(b[1]) | uint64(b[2])<<8 | uint64(b[3])<<16
		rest = b[4:]
	case 0xfe:
		if len(b) < 9 {
			return nil, nil, io.ErrUnexpectedEOF
		}
		n = binary.LittleEndian.Uint64(b[1:9])
		rest = b[9:]
	default:
		n = uint64(b[0])
		rest = b[1:]
	}
	if uint64(len(rest)) < n {
		return nil, nil, io.ErrUnexpectedEOF
	}
	return append([]byte(nil), rest[:n]...), rest[n:], nil
}
