// Package postgrestest is a small fake PostgreSQL server for tests, in this
// package and in others. It speaks the simple query protocol only, so a
// test must use the driver's default query_mode (simple). It does not parse
// SQL: the test gives it a Handler that answers each SQL text.
package postgrestest

import (
	"fmt"
	"net"
	"strconv"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgproto3"
)

// Column is one column of a Result. OID is the PostgreSQL type, such as 25
// for text, 23 for int4 and 16 for bool. Use Text and Int for the usual ones.
type Column struct {
	Name string
	OID  uint32
}

// Text and Int make a text column and an int4 column.
func Text(name string) Column { return Column{Name: name, OID: 25} }

// Int makes an int4 column.
func Int(name string) Column { return Column{Name: name, OID: 23} }

// Result is the answer to one statement. A cell is a string, or nil for NULL.
type Result struct {
	Columns []Column
	Rows    [][]any
	// Tag is the command tag, such as "SELECT 2" or "UPDATE 1".
	Tag string
	// ErrCode and ErrMessage, when set, make the statement fail. The statements
	// after it in the same query are not run.
	ErrCode    string
	ErrMessage string
}

// Handler answers one query text. It returns one Result for each statement
// in the text. No result at all means an empty query.
type Handler func(sql string) []Result

// Server is a running fake server.
type Server struct {
	ln       net.Listener
	handler  Handler
	password string

	mu      sync.Mutex
	conns   int
	queries []string
	startup []map[string]string
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
func (s *Server) URL() string { return "postgres://" + s.ln.Addr().String() + "/testdb" }

// Addr is host:port.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// ConnCount is how many connections have been opened.
func (s *Server) ConnCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

// Queries are the SQL texts received, in order.
func (s *Server) Queries() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.queries...)
}

// Startups are the startup parameters of each connection (user, database,
// application_name, and any other setting the client sent).
func (s *Server) Startups() []map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]string(nil), s.startup...)
}

func (s *Server) serve(c net.Conn) {
	defer c.Close()
	be := pgproto3.NewBackend(c, c)

	var params map[string]string
	for params == nil {
		msg, err := be.ReceiveStartupMessage()
		if err != nil {
			return
		}
		switch m := msg.(type) {
		case *pgproto3.SSLRequest, *pgproto3.GSSEncRequest:
			if _, err := c.Write([]byte("N")); err != nil {
				return
			}
		case *pgproto3.StartupMessage:
			params = m.Parameters
		default:
			return
		}
	}
	s.mu.Lock()
	s.conns++
	s.startup = append(s.startup, params)
	s.mu.Unlock()

	if s.password != "" {
		if err := be.SetAuthType(pgproto3.AuthTypeCleartextPassword); err != nil {
			return
		}
		be.Send(&pgproto3.AuthenticationCleartextPassword{})
		if be.Flush() != nil {
			return
		}
		msg, err := be.Receive()
		if err != nil {
			return
		}
		pm, ok := msg.(*pgproto3.PasswordMessage)
		if !ok || pm.Password != s.password {
			be.Send(&pgproto3.ErrorResponse{Severity: "FATAL", Code: "28P01", Message: "password authentication failed"})
			_ = be.Flush()
			return
		}
	}
	be.Send(&pgproto3.AuthenticationOk{})
	be.Send(&pgproto3.ParameterStatus{Name: "server_version", Value: "16.0"})
	be.Send(&pgproto3.ParameterStatus{Name: "client_encoding", Value: "UTF8"})
	be.Send(&pgproto3.ParameterStatus{Name: "standard_conforming_strings", Value: "on"})
	be.Send(&pgproto3.BackendKeyData{ProcessID: 1, SecretKey: 1})
	be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
	if be.Flush() != nil {
		return
	}

	for {
		msg, err := be.Receive()
		if err != nil {
			return
		}
		switch m := msg.(type) {
		case *pgproto3.Query:
			s.mu.Lock()
			s.queries = append(s.queries, m.String)
			s.mu.Unlock()
			s.answer(be, m.String)
		case *pgproto3.Terminate:
			return
		default:
			be.Send(&pgproto3.ErrorResponse{Severity: "ERROR", Code: "0A000", Message: "the fake server speaks only the simple query protocol"})
			be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		}
		if be.Flush() != nil {
			return
		}
	}
}

func (s *Server) answer(be *pgproto3.Backend, sql string) {
	results := s.handler(sql)
	if len(results) == 0 {
		be.Send(&pgproto3.EmptyQueryResponse{})
	}
	for _, r := range results {
		if r.ErrCode != "" || r.ErrMessage != "" {
			code := r.ErrCode
			if code == "" {
				code = "XX000"
			}
			be.Send(&pgproto3.ErrorResponse{Severity: "ERROR", Code: code, Message: r.ErrMessage})
			break
		}
		if len(r.Columns) > 0 {
			fds := make([]pgproto3.FieldDescription, len(r.Columns))
			for i, col := range r.Columns {
				fds[i] = pgproto3.FieldDescription{Name: []byte(col.Name), DataTypeOID: col.OID, DataTypeSize: -1, TypeModifier: -1}
			}
			be.Send(&pgproto3.RowDescription{Fields: fds})
		}
		for _, row := range r.Rows {
			vals := make([][]byte, len(row))
			for i, cell := range row {
				if cell != nil {
					vals[i] = []byte(text(cell))
				}
			}
			be.Send(&pgproto3.DataRow{Values: vals})
		}
		tag := r.Tag
		if tag == "" {
			tag = "SELECT " + strconv.Itoa(len(r.Rows))
		}
		be.Send(&pgproto3.CommandComplete{CommandTag: []byte(tag)})
	}
	be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
}

func text(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case bool:
		return strconv.FormatBool(x)
	}
	return fmt.Sprint(v)
}
