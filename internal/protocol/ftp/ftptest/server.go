// Package ftptest is a small FTP server for tests. It speaks enough of
// the protocol for the client library: login, features, passive mode,
// list, retrieve, store, delete, size and time. It does not do active
// mode, REST, APPE, rename, MKD, RMD, permissions, quotas, ASCII mode
// or bandwidth accounting beyond the SlowData knob.
package ftptest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Config is the server's files, users and fault knobs. Apply replaces
// the parts that are set. A nil map leaves that part as it was.
type Config struct {
	Users                  map[string]string
	Files                  map[string][]byte
	AnnounceHost           string
	NoMLST                 bool
	DOSList                bool
	RequireTLSSessionReuse bool
	NoSIZE                 bool
	CorruptDownloads       bool
}

// Server is one FTP server on 127.0.0.1.
type Server struct {
	t        *testing.T
	ln       net.Listener
	implicit bool
	tlsConf  *tls.Config

	mu        sync.Mutex
	cfg       Config
	commands  []string
	accepted  int
	openN     int
	dataN     int
	gen       map[string]int64
	stall     map[string]chan struct{}
	drop      map[string]bool
	reply     map[string]replySpec
	delay     time.Duration
	stallData int
	truncData int
	maxConn   int
	idle      time.Duration
	slowBPS   int
	refuse    bool
	closed    chan struct{}
}

type replySpec struct {
	code int
	text string
}

// Start listens for plain FTP.
func Start(t *testing.T) *Server { return start(t, false, false) }

// StartTLS listens for plain FTP and allows AUTH TLS.
func StartTLS(t *testing.T) *Server { return start(t, true, false) }

// StartImplicitTLS listens with TLS from the first byte.
func StartImplicitTLS(t *testing.T) *Server { return start(t, true, true) }

func start(t *testing.T, withTLS, implicit bool) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		t: t, ln: ln, implicit: implicit,
		cfg: Config{
			Users: map[string]string{"anonymous": "vegaload@"},
			Files: map[string][]byte{},
		},
		gen:    map[string]int64{},
		stall:  map[string]chan struct{}{},
		drop:   map[string]bool{},
		reply:  map[string]replySpec{},
		closed: make(chan struct{}),
	}
	if withTLS {
		cert, err := selfSigned()
		if err != nil {
			t.Fatal(err)
		}
		s.tlsConf = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
	}
	if implicit {
		s.ln = tls.NewListener(ln, s.tlsConf)
	}
	go s.accept()
	t.Cleanup(s.Close)
	return s
}

func (s *Server) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		refuse := s.refuse
		s.accepted++
		n := s.accepted
		max := s.maxConn
		s.openN++
		s.mu.Unlock()
		if refuse {
			c.Close()
			s.mu.Lock()
			s.openN--
			s.mu.Unlock()
			continue
		}
		go s.serve(c, n, max)
	}
}

// Close stops the listener and the open connections.
func (s *Server) Close() {
	select {
	case <-s.closed:
		return
	default:
		close(s.closed)
	}
	_ = s.ln.Close()
}

// URL is the ftp:// or ftps:// address of the server.
func (s *Server) URL() string {
	scheme := "ftp"
	if s.implicit {
		scheme = "ftps"
	}
	return scheme + "://" + s.ln.Addr().String()
}

// Addr is host:port.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// ConnCount is how many control connections were accepted.
func (s *Server) ConnCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accepted
}

// OpenConns is how many control connections are open now.
func (s *Server) OpenConns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.openN
}

// DataConnCount is how many data connections were accepted.
func (s *Server) DataConnCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dataN
}

// Commands is the log of control commands. A password is stored as PASS ****.
func (s *Server) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.commands))
	copy(out, s.commands)
	return out
}

// File returns one stored file.
func (s *Server) File(path string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.cfg.Files[clean(path)]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), b...), true
}

// Files returns a copy of the stored files.
func (s *Server) Files() map[string][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]byte, len(s.cfg.Files))
	for k, v := range s.cfg.Files {
		out[k] = append([]byte(nil), v...)
	}
	return out
}

// Apply changes users, files and knobs. A nil Users or Files map is left
// unchanged. AnnounceHost, when non-empty, replaces the PASV address.
func (s *Server) Apply(cfg Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cfg.Users != nil {
		s.cfg.Users = cfg.Users
	}
	if cfg.Files != nil {
		s.cfg.Files = cfg.Files
	}
	if cfg.AnnounceHost != "" {
		s.cfg.AnnounceHost = cfg.AnnounceHost
	}
	s.cfg.NoMLST = cfg.NoMLST
	s.cfg.DOSList = cfg.DOSList
	s.cfg.RequireTLSSessionReuse = cfg.RequireTLSSessionReuse
	s.cfg.NoSIZE = cfg.NoSIZE
	s.cfg.CorruptDownloads = cfg.CorruptDownloads
}

// Generate makes RETR of path stream n zero bytes without storing them.
func (s *Server) Generate(path string, n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen[clean(path)] = n
}

// StallOn makes the server read cmd and then never answer.
func (s *Server) StallOn(cmd string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stall[strings.ToUpper(cmd)] = make(chan struct{})
}

// StallDataAfter stops a transfer after n bytes and waits.
func (s *Server) StallDataAfter(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stallData = n
}

// DropOn closes the control connection when cmd is read.
func (s *Server) DropOn(cmd string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drop[strings.ToUpper(cmd)] = true
}

// ReplyOn answers cmd with a fixed code and text.
func (s *Server) ReplyOn(cmd string, code int, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reply[strings.ToUpper(cmd)] = replySpec{code, text}
}

// Delay waits before every reply.
func (s *Server) Delay(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delay = d
}

// TruncateDataAfter closes the data connection after n bytes and replies 426.
func (s *Server) TruncateDataAfter(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.truncData = n
}

// MaxConnections makes the n+1th control connection receive 421.
func (s *Server) MaxConnections(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxConn = n
}

// IdleTimeout closes a control connection that is quiet for d, with 421.
func (s *Server) IdleTimeout(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idle = d
}

// SlowData limits a data transfer to about bytesPerSecond.
func (s *Server) SlowData(bytesPerSecond int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.slowBPS = bytesPerSecond
}

// ClearFaults removes stall, drop and truncate knobs.
func (s *Server) ClearFaults() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stall = map[string]chan struct{}{}
	s.drop = map[string]bool{}
	s.reply = map[string]replySpec{}
	s.stallData = 0
	s.truncData = 0
}

// RefuseConnections closes new control connections at once.
func (s *Server) RefuseConnections(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refuse = v
}

func (s *Server) serve(raw net.Conn, id, max int) {
	defer func() {
		raw.Close()
		s.mu.Lock()
		s.openN--
		s.mu.Unlock()
	}()
	c := &ctrl{
		s: s, id: id, raw: raw, r: newLineReader(raw),
		dir: "/", protP: s.implicit, dead: make(chan struct{}),
	}
	if max > 0 && id > max {
		c.write(421, "Too many connections")
		return
	}
	c.write(220, "vegaload test ftp")
	if s.implicit {
		if tc, ok := raw.(*tls.Conn); ok {
			if err := tc.Handshake(); err != nil {
				return
			}
		}
	}
	idle := s.snapshotIdle()
	for {
		if idle > 0 {
			_ = raw.SetReadDeadline(time.Now().Add(idle))
		} else {
			_ = raw.SetReadDeadline(time.Time{})
		}
		line, err := c.r.ReadLine()
		if err != nil {
			if idle > 0 && isTimeout(err) {
				c.write(421, "Idle timeout")
			}
			return
		}
		_ = raw.SetReadDeadline(time.Time{})
		cmd, arg, err := parseCommand(line)
		if err != nil {
			c.write(500, "Syntax error")
			continue
		}
		s.log(id, cmd, arg)
		if s.dropped(cmd) {
			return
		}
		if ch := s.stalled(cmd); ch != nil {
			remote := c.waitRemoteClose()
			select {
			case <-ch:
			case <-remote:
			case <-c.dead:
			case <-s.closed:
			}
			return
		}
		if spec, ok := s.fixedReply(cmd); ok {
			c.write(spec.code, spec.text)
			continue
		}
		if !c.dispatch(cmd, arg) {
			return
		}
	}
}

func (s *Server) snapshotIdle() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idle
}

func (s *Server) log(id int, cmd, arg string) {
	if cmd == "PASS" {
		arg = "****"
	}
	line := fmt.Sprintf("conn%d %s", id, cmd)
	if arg != "" {
		line += " " + arg
	}
	s.mu.Lock()
	s.commands = append(s.commands, line)
	s.mu.Unlock()
}

func (s *Server) dropped(cmd string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.drop[cmd]
}

func (s *Server) stalled(cmd string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stall[cmd]
}

func (s *Server) fixedReply(cmd string) (replySpec, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	spec, ok := s.reply[cmd]
	return spec, ok
}

type ctrl struct {
	s      *Server
	id     int
	raw    net.Conn
	r      *lineReader
	dir    string
	user   string
	authed bool
	protP  bool
	dataLn net.Listener
	dataCh chan dataConn
	dead   chan struct{}
	once   sync.Once
}

type dataConn struct {
	c        net.Conn
	noResume bool
}

func (c *ctrl) kill() {
	c.once.Do(func() { close(c.dead) })
}

func (c *ctrl) write(code int, text string) {
	c.s.mu.Lock()
	d := c.s.delay
	c.s.mu.Unlock()
	if d > 0 {
		t := time.NewTimer(d)
		select {
		case <-t.C:
		case <-c.dead:
			t.Stop()
			return
		}
	}
	if strings.Contains(text, "\n") {
		lines := strings.Split(text, "\n")
		for i, line := range lines {
			sep := "-"
			if i == len(lines)-1 {
				sep = " "
			}
			fmt.Fprintf(c.raw, "%d%s%s\r\n", code, sep, line)
		}
		return
	}
	fmt.Fprintf(c.raw, "%d %s\r\n", code, text)
}

func (c *ctrl) dispatch(cmd, arg string) bool {
	switch cmd {
	case "USER":
		c.user = arg
		c.write(331, "Password required")
	case "PASS":
		if !c.s.loginOK(c.user, arg) {
			c.write(530, "Login incorrect.")
			return true
		}
		c.authed = true
		c.write(230, "Logged in")
	case "FEAT":
		c.write(211, c.s.featText())
	case "OPTS":
		c.write(200, "UTF8 set to on")
	case "TYPE":
		c.write(200, "Type set to I")
	case "NOOP":
		c.write(200, "OK")
	case "QUIT":
		c.write(221, "Bye")
		return false
	case "AUTH":
		if c.s.tlsConf == nil || c.s.implicit {
			c.write(502, "AUTH TLS is not supported")
			return true
		}
		c.write(234, "AUTH TLS successful")
		tc := tls.Server(c.raw, c.s.tlsConf)
		if err := tc.Handshake(); err != nil {
			return false
		}
		c.raw = tc
		c.r = newLineReader(tc)
	case "PBSZ":
		c.write(200, "PBSZ=0")
	case "PROT":
		if strings.EqualFold(arg, "P") {
			c.protP = true
		}
		c.write(200, "Protection set")
	case "EPSV":
		c.passive(true)
	case "PASV":
		c.passive(false)
	case "PWD":
		c.write(257, "\""+c.dir+"\" is the current directory")
	case "CWD":
		p := c.abs(arg)
		if !c.s.isDir(p) && p != "/" {
			c.write(550, "No such directory")
			return true
		}
		c.dir = p
		c.write(250, "Directory changed")
	case "LIST", "MLSD", "NLST", "RETR", "STOR":
		c.transfer(cmd, arg)
	case "DELE":
		p := c.abs(arg)
		if !c.s.delete(p) {
			c.write(550, p+": No such file or directory")
			return true
		}
		c.write(250, "Deleted")
	case "SIZE":
		if c.s.noSize() {
			c.write(502, "SIZE is not supported")
			return true
		}
		n, ok := c.s.sizeOf(c.abs(arg))
		if !ok {
			c.write(550, "No such file")
			return true
		}
		c.write(213, strconv.FormatInt(n, 10))
	case "MDTM":
		if _, ok := c.s.sizeOf(c.abs(arg)); !ok {
			c.write(550, "No such file")
			return true
		}
		c.write(213, "20200102150405")
	case "MLST":
		c.write(250, "End")
	default:
		c.write(502, "Command not implemented")
	}
	return true
}

func (c *ctrl) abs(p string) string {
	if p == "" || p == "." {
		return c.dir
	}
	if strings.HasPrefix(p, "/") {
		return clean(p)
	}
	return clean(c.dir + "/" + p)
}

func (c *ctrl) passive(epsv bool) {
	if c.dataLn != nil {
		c.dataLn.Close()
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		c.write(425, "Cannot open a passive port")
		return
	}
	c.dataLn = ln
	port := ln.Addr().(*net.TCPAddr).Port
	c.dataCh = make(chan dataConn, 1)
	go c.acceptData(ln, c.dataCh)
	if epsv {
		c.write(229, fmt.Sprintf("Entering Extended Passive Mode (|||%d|)", port))
		return
	}
	host := c.s.announce()
	ip := net.ParseIP(host).To4()
	if ip == nil {
		ip = net.ParseIP("127.0.0.1").To4()
	}
	p1, p2 := port/256, port%256
	c.write(227, fmt.Sprintf("Entering Passive Mode (%d,%d,%d,%d,%d,%d).", ip[0], ip[1], ip[2], ip[3], p1, p2))
}

func (c *ctrl) acceptData(ln net.Listener, ch chan dataConn) {
	dc, err := ln.Accept()
	if err != nil {
		return
	}
	c.s.mu.Lock()
	c.s.dataN++
	c.s.mu.Unlock()
	out := dataConn{c: dc}
	if c.protP && c.s.tlsConf != nil {
		// The handshake waits until the transfer command has been
		// answered. Doing it here stalls the command loop.
		out.c = tls.Server(dc, c.s.tlsConf)
	}
	select {
	case ch <- out:
	case <-c.dead:
		out.c.Close()
	}
}

func (c *ctrl) transfer(cmd, arg string) {
	if c.dataCh == nil {
		c.write(425, "Use PASV or EPSV first")
		return
	}
	var dc dataConn
	select {
	case dc = <-c.dataCh:
	case <-c.dead:
		return
	case <-time.After(5 * time.Second):
		c.write(425, "No data connection")
		return
	}
	defer dc.c.Close()
	if c.dataLn != nil {
		c.dataLn.Close()
		c.dataLn = nil
	}
	if dc.noResume {
		c.write(425, "TLS session was not reused")
		return
	}
	if c.s.stalled("226") != nil && cmd != "STOR" && cmd != "RETR" && cmd != "LIST" && cmd != "MLSD" {
		// stall point is handled below after the data, for every transfer
	}
	listArg, hidden := splitListArg(arg)
	switch cmd {
	case "LIST", "MLSD", "NLST":
		p := c.abs(listArg)
		if !c.s.isDir(p) {
			c.write(550, "No such directory")
			return
		}
		body := c.s.listing(cmd, p, hidden)
		c.write(150, "Here comes the listing")
		if !c.finishTLS(dc) {
			c.write(426, "TLS session was not reused")
			return
		}
		if !c.pump(dc.c, strings.NewReader(body), dc.c) {
			c.afterShort()
			return
		}
	case "RETR":
		p := c.abs(arg)
		r, ok := c.s.open(p)
		if !ok {
			c.write(550, p+": No such file or directory")
			return
		}
		defer r.Close()
		c.write(150, "Opening binary mode data connection")
		if !c.finishTLS(dc) {
			c.write(426, "TLS session was not reused")
			return
		}
		if !c.pump(dc.c, r, dc.c) {
			c.afterShort()
			return
		}
	case "STOR":
		p := c.abs(arg)
		c.write(150, "Ok to send data")
		if !c.finishTLS(dc) {
			c.write(426, "TLS session was not reused")
			return
		}
		var buf limitedBuf
		if !c.pump(&buf, dc.c, dc.c) {
			c.s.put(p, buf.b)
			c.afterShort()
			return
		}
		c.s.put(p, buf.b)
	}
	c.finishTransfer()
}

// finishTLS completes a data-connection handshake after the 150 reply.
func (c *ctrl) finishTLS(dc dataConn) bool {
	tc, ok := dc.c.(*tls.Conn)
	if !ok {
		return true
	}
	if err := tc.Handshake(); err != nil {
		return false
	}
	c.s.mu.Lock()
	reuse := c.s.cfg.RequireTLSSessionReuse
	c.s.mu.Unlock()
	if reuse && !tc.ConnectionState().DidResume {
		return false
	}
	return true
}

func (c *ctrl) afterShort() {
	if c.s.truncated() {
		c.write(426, "Connection closed; transfer aborted")
	}
}

func (c *ctrl) waitRemoteClose() <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		buf := make([]byte, 1)
		_, _ = c.raw.Read(buf)
	}()
	return ch
}

func (c *ctrl) waitConn(conn net.Conn) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(io.Discard, conn)
	}()
	select {
	case <-done:
	case <-c.s.closed:
	}
}

func (c *ctrl) finishTransfer() {
	if ch := c.s.stalled("226"); ch != nil {
		select {
		case <-ch:
		case <-c.dead:
		case <-c.s.closed:
		}
		return
	}
	if c.s.truncated() {
		c.write(426, "Connection closed; transfer aborted")
		return
	}
	c.write(226, "Transfer complete")
}

// pump copies until EOF. It returns false when the transfer was cut short
// and the caller must not send 226.
func (c *ctrl) pump(dst io.Writer, src io.Reader, watch net.Conn) bool {
	stallN, truncN, bps := c.s.dataKnobs()
	buf := make([]byte, 32*1024)
	sent := 0
	for {
		n, err := src.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if truncN > 0 && sent+n > truncN {
				chunk = chunk[:truncN-sent]
				_, _ = dst.Write(chunk)
				return false
			}
			if stallN > 0 && sent >= stallN {
				c.waitConn(watch)
				return false
			}
			if _, werr := dst.Write(chunk); werr != nil {
				return false
			}
			sent += len(chunk)
			if bps > 0 {
				time.Sleep(time.Duration(len(chunk)) * time.Second / time.Duration(bps))
			}
			if stallN > 0 && sent >= stallN {
				c.waitConn(watch)
				return false
			}
		}
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
	}
}

func (s *Server) loginOK(user, pass string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	want, ok := s.cfg.Users[user]
	if !ok {
		return false
	}
	return want == pass
}

func (s *Server) featText() string {
	s.mu.Lock()
	no := s.cfg.NoMLST
	noSize := s.cfg.NoSIZE
	s.mu.Unlock()
	var b strings.Builder
	b.WriteString("Features:\n")
	if !no {
		b.WriteString(" MLST size*;type*;modify*;\n")
	}
	b.WriteString(" UTF8\n")
	if !noSize {
		b.WriteString(" SIZE\n")
	}
	b.WriteString(" MDTM\n")
	b.WriteString("End")
	return b.String()
}

func (s *Server) announce() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.AnnounceHost != "" {
		return s.cfg.AnnounceHost
	}
	return "127.0.0.1"
}

func (s *Server) noSize() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.NoSIZE
}

func (s *Server) truncated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.truncData > 0
}

func (s *Server) dataKnobs() (stall, trunc, bps int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stallData, s.truncData, s.slowBPS
}

func (s *Server) isDir(p string) bool {
	p = clean(p)
	if p == "/" {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := p + "/"
	for name := range s.cfg.Files {
		if name == p {
			return false
		}
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	for name := range s.gen {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func (s *Server) sizeOf(p string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.gen[p]; ok {
		return n, true
	}
	b, ok := s.cfg.Files[p]
	if !ok {
		return 0, false
	}
	return int64(len(b)), true
}

func (s *Server) open(p string) (io.ReadCloser, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.gen[p]; ok {
		return io.NopCloser(io.LimitReader(zeroGen{}, n)), true
	}
	b, ok := s.cfg.Files[p]
	if !ok {
		return nil, false
	}
	out := append([]byte(nil), b...)
	if s.cfg.CorruptDownloads && len(out) > 0 {
		out[0] ^= 0xff
	}
	return io.NopCloser(&bytesReader{b: out}), true
}

func (s *Server) put(p string, b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Files[p] = append([]byte(nil), b...)
}

func (s *Server) delete(p string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.cfg.Files[p]; !ok {
		if _, ok := s.gen[p]; !ok {
			return false
		}
		delete(s.gen, p)
		return true
	}
	delete(s.cfg.Files, p)
	return true
}

type entry struct {
	name string
	dir  bool
	size int64
}

func (s *Server) children(dir string) []entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]entry{}
	add := func(path string, size int64, file bool) {
		rest := strings.TrimPrefix(path, dir)
		rest = strings.TrimPrefix(rest, "/")
		if rest == "" {
			return
		}
		name, _, more := strings.Cut(rest, "/")
		if name == "" {
			return
		}
		if more || !file {
			seen[name] = entry{name: name, dir: true}
			return
		}
		if cur, ok := seen[name]; ok && cur.dir {
			return
		}
		seen[name] = entry{name: name, size: size}
	}
	prefix := dir
	if prefix != "/" {
		prefix += "/"
	}
	for path, b := range s.cfg.Files {
		if dir == "/" || strings.HasPrefix(path, prefix) || path == strings.TrimRight(dir, "/") {
			if dir == "/" && !strings.HasPrefix(path, "/") {
				continue
			}
			if dir != "/" && !strings.HasPrefix(path, prefix) {
				continue
			}
			add(path, int64(len(b)), true)
		}
	}
	for path, n := range s.gen {
		if dir == "/" || strings.HasPrefix(path, prefix) {
			add(path, n, true)
		}
	}
	var out []entry
	for _, e := range seen {
		out = append(out, e)
	}
	return out
}

func (s *Server) listing(cmd, dir string, hidden bool) string {
	s.mu.Lock()
	dos := s.cfg.DOSList
	s.mu.Unlock()
	var b strings.Builder
	for _, e := range s.children(dir) {
		if !hidden && cmd != "MLSD" && strings.HasPrefix(e.name, ".") {
			continue
		}
		switch {
		case cmd == "NLST":
			b.WriteString(e.name)
			b.WriteString("\r\n")
		case cmd == "MLSD":
			kind := "file"
			if e.dir {
				kind = "dir"
			}
			fmt.Fprintf(&b, "type=%s;size=%d;modify=20200102150405; %s\r\n", kind, e.size, e.name)
		case dos:
			if e.dir {
				fmt.Fprintf(&b, "01-02-06  03:04PM       <DIR> %s\r\n", e.name)
			} else {
				fmt.Fprintf(&b, "01-02-06  03:04PM       %d %s\r\n", e.size, e.name)
			}
		default:
			if e.dir {
				fmt.Fprintf(&b, "drwxr-xr-x 1 ftp ftp %d Jan 2 15:04 %s\r\n", e.size, e.name)
			} else {
				fmt.Fprintf(&b, "-rw-r--r-- 1 ftp ftp %d Jan 2 15:04 %s\r\n", e.size, e.name)
			}
		}
	}
	return b.String()
}

func splitListArg(arg string) (path string, hidden bool) {
	arg = strings.TrimSpace(arg)
	if arg == "-a" {
		return "", true
	}
	if strings.HasPrefix(arg, "-a ") {
		return strings.TrimSpace(arg[3:]), true
	}
	return arg, false
}

func clean(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	parts := strings.Split(p, "/")
	var out []string
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			continue
		}
		out = append(out, part)
	}
	return "/" + strings.Join(out, "/")
}

type zeroGen struct{}

func (zeroGen) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

type bytesReader struct{ b []byte }

func (b *bytesReader) Read(p []byte) (int, error) {
	if len(b.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b.b)
	b.b = b.b[n:]
	return n, nil
}

// limitedBuf stores a STOR body. Tests do not upload unbounded data
// except the memory check, which is tens of megabytes.
type limitedBuf struct {
	b []byte
}

func (l *limitedBuf) Write(p []byte) (int, error) {
	l.b = append(l.b, p...)
	return len(p), nil
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

func selfSigned() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "vegaload-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return tls.X509KeyPair(certPEM, keyPEM)
}
