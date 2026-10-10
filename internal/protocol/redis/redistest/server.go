// Package redistest is a small Redis server for tests. It speaks enough
// RESP2 to answer the commands a client sends, including a pipeline and a
// MULTI/EXEC transaction. It is not a Redis implementation.
package redistest

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Kind is the RESP type of a Value.
type Kind int

const (
	// Simple is a status reply, such as OK or PONG.
	Simple Kind = iota
	// Error is a Redis error reply. Text is the message, including the code word.
	Error
	// Integer is a number reply.
	Integer
	// Bulk is a string reply.
	Bulk
	// Nil is a missing value.
	Nil
	// Array is a list of values.
	Array
)

// Value is one RESP reply.
type Value struct {
	Kind  Kind
	Text  string
	Int   int64
	Items []Value
}

// SimpleString is a status reply.
func SimpleString(s string) Value { return Value{Kind: Simple, Text: s} }

// ErrorString is an error reply. s should start with the code word, such as "ERR something".
func ErrorString(s string) Value { return Value{Kind: Error, Text: s} }

// Int is an integer reply.
func Int(n int64) Value { return Value{Kind: Integer, Int: n} }

// BulkString is a string reply.
func BulkString(s string) Value { return Value{Kind: Bulk, Text: s} }

// NilValue is a missing value.
func NilValue() Value { return Value{Kind: Nil} }

// ArrayOf is a list reply.
func ArrayOf(items ...Value) Value { return Value{Kind: Array, Items: items} }

// Handler answers one user command. args[0] is the command name as the
// client sent it. Built-in commands (HELLO, AUTH, SELECT, PING, QUIT, MULTI,
// EXEC, DISCARD) are not passed to the handler.
type Handler func(args []string) Value

// Server is a running fake server.
type Server struct {
	ln       net.Listener
	handler  Handler
	password string
	tlsOn    bool

	noHello    atomic.Bool
	closeAfter atomic.Int32

	mu       sync.Mutex
	commands [][]string
	setup    [][]string
	conns    int
}

// Start listens on 127.0.0.1 and serves until the test ends.
func Start(t testing.TB, h Handler) *Server {
	t.Helper()
	return start(t, h, "", false)
}

// StartWithPassword is Start, and HELLO or AUTH must present password.
// A wrong password is a Redis error that does not contain the password.
func StartWithPassword(t testing.TB, h Handler, password string) *Server {
	t.Helper()
	return start(t, h, password, false)
}

// StartTLS is Start behind a self-signed certificate for 127.0.0.1.
func StartTLS(t testing.TB, h Handler) *Server {
	t.Helper()
	return start(t, h, "", true)
}

func start(t testing.TB, h Handler, password string, useTLS bool) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if useTLS {
		cert, err := selfSigned()
		if err != nil {
			ln.Close()
			t.Fatal(err)
		}
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
	}
	s := &Server{ln: ln, handler: h, password: password, tlsOn: useTLS}
	if s.handler == nil {
		s.handler = func([]string) Value { return SimpleString("OK") }
	}
	go s.accept()
	t.Cleanup(func() { ln.Close() })
	return s
}

func selfSigned() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// DisableHello makes HELLO answer with an unknown-command error, so a client
// falls back to AUTH and SELECT.
func (s *Server) DisableHello() { s.noHello.Store(true) }

// CloseAfterUserCommands closes the connection after n user commands have
// been read, without writing a reply. Setup commands do not count. n is
// counted per connection.
func (s *Server) CloseAfterUserCommands(n int) { s.closeAfter.Store(int32(n)) }

// URL is a redis:// or rediss:// URL for this server.
func (s *Server) URL() string {
	scheme := "redis"
	if s.tlsOn {
		scheme = "rediss"
	}
	return scheme + "://" + s.ln.Addr().String()
}

// Addr is host:port.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// ConnCount is how many connections were accepted.
func (s *Server) ConnCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

// Commands are the user commands received, in order, without HELLO, AUTH or
// SELECT. MULTI and EXEC are included. Each entry is the argument list.
func (s *Server) Commands() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneArgs(s.commands)
}

// Setup is HELLO, AUTH and SELECT, in the order they were received.
func (s *Server) Setup() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneArgs(s.setup)
}

func cloneArgs(in [][]string) [][]string {
	out := make([][]string, len(in))
	for i, a := range in {
		out[i] = append([]string(nil), a...)
	}
	return out
}

func (s *Server) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns++
		s.mu.Unlock()
		go s.serve(c)
	}
}

func (s *Server) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	bw := bufio.NewWriter(c)
	inMulti := false
	var queued []Value
	userN := 0
	for {
		args, err := readArray(br)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}
		name := strings.ToUpper(args[0])
		switch name {
		case "HELLO":
			s.recordSetup(args)
			if s.noHello.Load() {
				writeValue(bw, ErrorString("ERR unknown command 'HELLO'"))
				bw.Flush()
				continue
			}
			if !s.helloAuthOK(args) {
				writeValue(bw, ErrorString("WRONGPASS invalid username-password pair"))
				bw.Flush()
				continue
			}
			writeValue(bw, helloReply())
			bw.Flush()
		case "AUTH":
			s.recordSetup(args)
			if !s.authOK(args) {
				writeValue(bw, ErrorString("WRONGPASS invalid username-password pair"))
				bw.Flush()
				continue
			}
			writeValue(bw, SimpleString("OK"))
			bw.Flush()
		case "SELECT":
			s.recordSetup(args)
			writeValue(bw, SimpleString("OK"))
			bw.Flush()
		case "QUIT":
			writeValue(bw, SimpleString("OK"))
			bw.Flush()
			return
		case "PING":
			if s.maybeClose(c, &userN) {
				s.recordCommand(args)
				return
			}
			s.recordCommand(args)
			if inMulti {
				queued = append(queued, SimpleString("PONG"))
				writeValue(bw, SimpleString("QUEUED"))
			} else {
				writeValue(bw, SimpleString("PONG"))
			}
			bw.Flush()
		case "MULTI":
			s.recordCommand(args)
			inMulti = true
			queued = nil
			writeValue(bw, SimpleString("OK"))
			bw.Flush()
		case "DISCARD":
			s.recordCommand(args)
			inMulti = false
			queued = nil
			writeValue(bw, SimpleString("OK"))
			bw.Flush()
		case "EXEC":
			s.recordCommand(args)
			writeValue(bw, ArrayOf(queued...))
			bw.Flush()
			inMulti = false
			queued = nil
		default:
			if s.maybeClose(c, &userN) {
				s.recordCommand(args)
				return
			}
			v := s.handler(args)
			s.recordCommand(args)
			if inMulti {
				queued = append(queued, v)
				writeValue(bw, SimpleString("QUEUED"))
			} else {
				writeValue(bw, v)
			}
			bw.Flush()
		}
	}
}

func (s *Server) maybeClose(c net.Conn, userN *int) bool {
	limit := int(s.closeAfter.Load())
	if limit <= 0 {
		return false
	}
	*userN++
	if *userN >= limit {
		c.Close()
		return true
	}
	return false
}

func (s *Server) recordCommand(args []string) {
	s.mu.Lock()
	s.commands = append(s.commands, append([]string(nil), args...))
	s.mu.Unlock()
}

func (s *Server) recordSetup(args []string) {
	s.mu.Lock()
	s.setup = append(s.setup, append([]string(nil), args...))
	s.mu.Unlock()
}

func (s *Server) helloAuthOK(args []string) bool {
	if s.password == "" {
		return true
	}
	user, pass, ok := helloAuth(args)
	if !ok {
		return false
	}
	return pass == s.password && user != ""
}

func helloAuth(args []string) (user, pass string, ok bool) {
	for i := 1; i < len(args); i++ {
		if strings.EqualFold(args[i], "AUTH") && i+2 < len(args) {
			return args[i+1], args[i+2], true
		}
	}
	return "", "", false
}

func (s *Server) authOK(args []string) bool {
	if s.password == "" {
		return true
	}
	switch len(args) {
	case 2:
		return args[1] == s.password
	case 3:
		return args[2] == s.password
	default:
		return false
	}
}

func helloReply() Value {
	return ArrayOf(
		BulkString("server"), BulkString("redis"),
		BulkString("version"), BulkString("7.4.0"),
		BulkString("proto"), Int(2),
		BulkString("id"), Int(1),
		BulkString("mode"), BulkString("standalone"),
		BulkString("role"), BulkString("master"),
		BulkString("modules"), ArrayOf(),
	)
}

func readArray(r *bufio.Reader) ([]string, error) {
	line, err := readLine(r)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 || line[0] != '*' {
		return nil, fmt.Errorf("want an array, got %q", line)
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil || n < 0 {
		return nil, fmt.Errorf("bad array length %q", line)
	}
	args := make([]string, n)
	for i := 0; i < n; i++ {
		line, err = readLine(r)
		if err != nil {
			return nil, err
		}
		if len(line) == 0 || line[0] != '$' {
			return nil, fmt.Errorf("want a bulk string, got %q", line)
		}
		ln, err := strconv.Atoi(line[1:])
		if err != nil || ln < 0 {
			return nil, fmt.Errorf("bad bulk length %q", line)
		}
		buf := make([]byte, ln+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args[i] = string(buf[:ln])
	}
	return args, nil
}

func readLine(r *bufio.Reader) (string, error) {
	s, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}

func writeValue(w io.Writer, v Value) {
	switch v.Kind {
	case Simple:
		fmt.Fprintf(w, "+%s\r\n", v.Text)
	case Error:
		fmt.Fprintf(w, "-%s\r\n", v.Text)
	case Integer:
		fmt.Fprintf(w, ":%d\r\n", v.Int)
	case Bulk:
		fmt.Fprintf(w, "$%d\r\n%s\r\n", len(v.Text), v.Text)
	case Nil:
		io.WriteString(w, "$-1\r\n")
	case Array:
		fmt.Fprintf(w, "*%d\r\n", len(v.Items))
		for _, it := range v.Items {
			writeValue(w, it)
		}
	default:
		fmt.Fprintf(w, "+OK\r\n")
	}
}
