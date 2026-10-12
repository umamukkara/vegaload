// Package sftptest is an SSH server with an SFTP subsystem for tests.
//
// It does not implement permissions, quotas, symlink creation limits,
// fsync, rename, bandwidth shaping beyond SlowData, or SSH rekeying.
// The file store for ordinary paths is in memory. GenFile and paths
// under /sink/ are not stored.
package sftptest

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// GenByte is the byte a generated file holds at off.
func GenByte(off int64) byte { return byte(off*131 + 17) }

// Config is the starting state of a server.
type Config struct {
	Users          map[string]string
	AuthorizedKeys map[string][]ssh.PublicKey
	Files          map[string][]byte
	NoPassword     bool
	NoPublicKey    bool
	NoSubsystem    bool
}

// Server is a fake SFTP server.
type Server struct {
	ln      net.Listener
	addr    string
	mem     sftp.Handlers
	hostKey ssh.Signer
	rsaKey  ssh.Signer

	mu       sync.Mutex
	users    map[string]string
	keys     map[string][]ssh.PublicKey
	useRSA   bool
	gen      map[string]int64
	sinks    map[string]*sinkFile
	openList map[net.Conn]*tracked

	conns atomic.Int64
	open  atomic.Int32

	opsMu sync.Mutex
	ops   []string
	auths []string
	reqs  []string

	stallOp      string
	stallCh      chan struct{}
	stallAfterOp string
	stallAfterN  int
	counts       map[string]int
	dropOp       string
	replyOp      string
	replyCode    uint32
	replyText    string
	delay        time.Duration
	truncSet     bool
	truncAt      int64
	corruptSet   bool
	corruptAt    int64
	maxConn      int
	idle         time.Duration
	slowBPS      int
	handDelay    time.Duration
	authDelay    time.Duration
	noPassword   bool
	noPublicKey  bool
	noSubsystem  bool
	refuse       bool

	stop chan struct{}
	wake chan struct{}
	wg   sync.WaitGroup
	once sync.Once
}

type tracked struct {
	c    net.Conn
	last atomic.Int64
}

type sinkFile struct {
	mu   sync.Mutex
	next int64
	n    int64
	h    hash.Hash
	pend map[int64][]byte
}

// Start listens on 127.0.0.1 and serves SFTP. The default user is load
// with password secret.
func Start(t *testing.T) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		ln:       ln,
		addr:     ln.Addr().String(),
		mem:      sftp.InMemHandler(),
		hostKey:  signer,
		users:    map[string]string{"load": "secret"},
		keys:     map[string][]ssh.PublicKey{},
		gen:      map[string]int64{},
		sinks:    map[string]*sinkFile{},
		openList: map[net.Conn]*tracked{},
		counts:   map[string]int{},
		stop:     make(chan struct{}),
		wake:     make(chan struct{}),
	}
	t.Cleanup(s.Close)
	s.wg.Add(1)
	go s.acceptLoop()
	go s.idleLoop()
	return s
}

// URL is an SFTP URL for the default user. It has no password.
func (s *Server) URL() string { return "sftp://load@" + s.addr }

// Addr is host:port.
func (s *Server) Addr() string { return s.addr }

// ConnCount is how many TCP connections were accepted.
func (s *Server) ConnCount() int { return int(s.conns.Load()) }

// OpenConns is how many TCP connections are open now.
func (s *Server) OpenConns() int { return int(s.open.Load()) }

// AuthAttempts lists the methods the client tried, in order.
func (s *Server) AuthAttempts() []string {
	s.opsMu.Lock()
	defer s.opsMu.Unlock()
	return append([]string(nil), s.auths...)
}

// Ops lists "connN op path" lines. It has no passwords.
func (s *Server) Ops() []string {
	s.opsMu.Lock()
	defer s.opsMu.Unlock()
	return append([]string(nil), s.ops...)
}

// Requests lists session requests other than the sftp subsystem.
func (s *Server) Requests() []string {
	s.opsMu.Lock()
	defer s.opsMu.Unlock()
	return append([]string(nil), s.reqs...)
}

// HostKeyFingerprint is the SHA256 fingerprint of the Ed25519 host key.
func (s *Server) HostKeyFingerprint() string {
	return ssh.FingerprintSHA256(s.hostKey.PublicKey())
}

// AddRSA adds an RSA host key. Later connections can present it.
func (s *Server) AddRSA() error {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	signer, err := ssh.NewSignerFromKey(k)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.rsaKey = signer
	s.useRSA = true
	s.mu.Unlock()
	return nil
}

// RSAFingerprint is the SHA256 fingerprint of the RSA host key.
func (s *Server) RSAFingerprint() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rsaKey == nil {
		return ""
	}
	return ssh.FingerprintSHA256(s.rsaKey.PublicKey())
}

// Apply replaces users, keys and flags, and writes files.
func (s *Server) Apply(cfg Config) {
	s.mu.Lock()
	if cfg.Users != nil {
		s.users = cfg.Users
	}
	if cfg.AuthorizedKeys != nil {
		s.keys = cfg.AuthorizedKeys
	}
	s.noPassword = cfg.NoPassword
	s.noPublicKey = cfg.NoPublicKey
	s.noSubsystem = cfg.NoSubsystem
	s.mu.Unlock()
	for p, b := range cfg.Files {
		_ = s.put(p, b)
	}
}

// GenFile serves size bytes at path. The bytes are made on each read.
func (s *Server) GenFile(p string, size int64) {
	s.mu.Lock()
	s.gen[path.Clean(p)] = size
	s.mu.Unlock()
}

// SinkHash reports how many bytes were written to a /sink/ path and
// their SHA-256. ok is false when the path was not a sink.
func (s *Server) SinkHash(p string) (n int64, sum string, ok bool) {
	s.mu.Lock()
	sk := s.sinks[path.Clean(p)]
	s.mu.Unlock()
	if sk == nil {
		return 0, "", false
	}
	sk.mu.Lock()
	defer sk.mu.Unlock()
	return sk.n, fmt.Sprintf("%x", sk.h.Sum(nil)), true
}

// File reads one ordinary file back.
func (s *Server) File(p string) ([]byte, bool) {
	req := sftp.NewRequest("Get", p)
	req.Flags = 0x01
	rd, err := s.mem.FileGet.Fileread(req)
	if err != nil {
		return nil, false
	}
	buf := make([]byte, 0, 64)
	tmp := make([]byte, 32*1024)
	var off int64
	for {
		n, err := rd.ReadAt(tmp, off)
		buf = append(buf, tmp[:n]...)
		off += int64(n)
		if err == io.EOF {
			return buf, true
		}
		if err != nil {
			return buf, n > 0 || len(buf) > 0
		}
		if n == 0 {
			return buf, true
		}
	}
}

// Files returns the ordinary files in the store. Sink and generated
// files are not included.
func (s *Server) Files() map[string][]byte {
	out := map[string][]byte{}
	var walk func(dir string)
	walk = func(dir string) {
		req := sftp.NewRequest("List", dir)
		ls, err := s.mem.FileList.Filelist(req)
		if err != nil {
			return
		}
		infos, _ := readAll(ls)
		for _, fi := range infos {
			p := path.Join(dir, fi.Name())
			if fi.IsDir() {
				walk(p)
				continue
			}
			if fi.Mode()&os.ModeSymlink != 0 {
				continue
			}
			b, ok := s.File(p)
			if ok {
				out[p] = b
			}
		}
	}
	walk("/")
	return out
}

// Mkdir creates a directory in the store.
func (s *Server) Mkdir(p string) error {
	return s.mem.FileCmd.Filecmd(sftp.NewRequest("Mkdir", p))
}

// Symlink creates a symlink. target is the existing path. link is the new name.
func (s *Server) Symlink(target, link string) error {
	r := sftp.NewRequest("Symlink", target)
	r.Target = link
	return s.mem.FileCmd.Filecmd(r)
}

// StallOn reads the request and does not answer until ClearFaults.
func (s *Server) StallOn(op string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stallOp = op
	if s.stallCh == nil {
		s.stallCh = make(chan struct{})
	}
}

// StallAfter lets n operations of op succeed, then stalls.
func (s *Server) StallAfter(op string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stallAfterOp = op
	s.stallAfterN = n
	if s.stallCh == nil {
		s.stallCh = make(chan struct{})
	}
}

// DropOn closes the TCP connection when op runs.
func (s *Server) DropOn(op string) {
	s.mu.Lock()
	s.dropOp = op
	s.mu.Unlock()
}

// ReplyOn makes op fail with an SFTP status code and text.
func (s *Server) ReplyOn(op string, code uint32, text string) {
	s.mu.Lock()
	s.replyOp = op
	s.replyCode = code
	s.replyText = text
	s.mu.Unlock()
}

// Delay waits before each operation.
func (s *Server) Delay(d time.Duration) {
	s.mu.Lock()
	s.delay = d
	s.mu.Unlock()
}

// TruncateReadAfter fails a read once n bytes have been returned.
func (s *Server) TruncateReadAfter(n int64) {
	s.mu.Lock()
	s.truncSet = true
	s.truncAt = n
	s.mu.Unlock()
}

// CorruptByte flips one byte of a read at offset.
func (s *Server) CorruptByte(offset int64) {
	s.mu.Lock()
	s.corruptSet = true
	s.corruptAt = offset
	s.mu.Unlock()
}

// MaxConnections closes the connection after n, during the handshake.
func (s *Server) MaxConnections(n int) {
	s.mu.Lock()
	s.maxConn = n
	s.mu.Unlock()
}

// IdleTimeout closes a connection that has been idle for d.
func (s *Server) IdleTimeout(d time.Duration) {
	s.mu.Lock()
	s.idle = d
	s.mu.Unlock()
}

// SlowData limits a transfer to about bytesPerSecond.
func (s *Server) SlowData(bytesPerSecond int) {
	s.mu.Lock()
	s.slowBPS = bytesPerSecond
	s.mu.Unlock()
}

// RefuseConnections closes each new TCP connection at once.
func (s *Server) RefuseConnections(on bool) {
	s.mu.Lock()
	s.refuse = on
	s.mu.Unlock()
}

// HandshakeDelay waits before the SSH handshake.
func (s *Server) HandshakeDelay(d time.Duration) {
	s.mu.Lock()
	s.handDelay = d
	s.mu.Unlock()
}

// AuthDelay waits inside the login callbacks.
func (s *Server) AuthDelay(d time.Duration) {
	s.mu.Lock()
	s.authDelay = d
	s.mu.Unlock()
}

// ClearFaults releases stalls and turns the knobs off.
func (s *Server) ClearFaults() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stallOp = ""
	s.stallAfterOp = ""
	s.stallAfterN = 0
	s.counts = map[string]int{}
	s.dropOp = ""
	s.replyOp = ""
	s.delay = 0
	s.truncSet = false
	s.corruptSet = false
	s.slowBPS = 0
	s.handDelay = 0
	s.authDelay = 0
	s.maxConn = 0
	s.idle = 0
	s.refuse = false
	if s.stallCh != nil {
		close(s.stallCh)
		s.stallCh = nil
	}
	close(s.wake)
	s.wake = make(chan struct{})
}

// Close stops the server.
func (s *Server) Close() {
	s.once.Do(func() {
		close(s.stop)
		s.ClearFaults()
		_ = s.ln.Close()
		s.mu.Lock()
		for c := range s.openList {
			_ = c.Close()
		}
		s.mu.Unlock()
		s.wg.Wait()
	})
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		n := int(s.conns.Add(1))
		s.wg.Add(1)
		go s.serve(c, n)
	}
}

func (s *Server) serve(c net.Conn, n int) {
	defer s.wg.Done()
	s.open.Add(1)
	defer s.open.Add(-1)
	defer c.Close()
	tr := &tracked{c: c}
	tr.last.Store(time.Now().UnixNano())
	s.mu.Lock()
	refuse := s.refuse
	maxConn := s.maxConn
	hand := s.handDelay
	s.openList[c] = tr
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.openList, c)
		s.mu.Unlock()
	}()
	if refuse || (maxConn > 0 && n > maxConn) {
		return
	}
	if hand > 0 {
		s.sleep(hand)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			s.logAuth("password")
			s.touch(c)
			if d := s.authWait(); d > 0 {
				s.sleep(d)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.noPassword {
				return nil, errors.New("password is off")
			}
			want, ok := s.users[meta.User()]
			if !ok || want != string(pass) {
				return nil, errors.New("no")
			}
			return nil, nil
		},
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			s.logAuth("publickey")
			s.touch(c)
			if d := s.authWait(); d > 0 {
				s.sleep(d)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.noPublicKey {
				return nil, errors.New("public key is off")
			}
			for _, have := range s.keys[meta.User()] {
				if string(have.Marshal()) == string(key.Marshal()) {
					return nil, nil
				}
			}
			return nil, errors.New("unknown key")
		},
	}
	cfg.AddHostKey(s.hostKey)
	s.mu.Lock()
	if s.useRSA && s.rsaKey != nil {
		cfg.AddHostKey(s.rsaKey)
	}
	s.mu.Unlock()
	sshConn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)
	for ch := range chans {
		if ch.ChannelType() != "session" {
			_ = ch.Reject(ssh.UnknownChannelType, "only session")
			continue
		}
		channel, requests, err := ch.Accept()
		if err != nil {
			continue
		}
		go s.session(c, channel, requests, n)
	}
}

func (s *Server) session(tcp net.Conn, ch ssh.Channel, reqs <-chan *ssh.Request, id int) {
	defer ch.Close()
	for req := range reqs {
		switch req.Type {
		case "subsystem":
			name := sshName(req.Payload)
			s.mu.Lock()
			off := s.noSubsystem
			s.mu.Unlock()
			if name == "sftp" && !off {
				req.Reply(true, nil)
				h := &connHandler{s: s, tcp: tcp, id: id}
				rs := sftp.NewRequestServer(ch, h.handlers())
				_ = rs.Serve()
				return
			}
			s.logReq(req.Type)
			req.Reply(false, nil)
		case "exec", "shell", "pty-req":
			s.logReq(req.Type)
			req.Reply(false, nil)
		default:
			req.Reply(false, nil)
		}
	}
}

func sshName(payload []byte) string {
	if len(payload) < 4 {
		return ""
	}
	n := int(payload[0])<<24 | int(payload[1])<<16 | int(payload[2])<<8 | int(payload[3])
	if n < 0 || len(payload) < 4+n {
		return ""
	}
	return string(payload[4 : 4+n])
}

func (s *Server) idleLoop() {
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-tick.C:
			s.closeIdle()
		}
	}
}

func (s *Server) closeIdle() {
	s.mu.Lock()
	idle := s.idle
	if idle <= 0 {
		s.mu.Unlock()
		return
	}
	now := time.Now()
	var drop []net.Conn
	for c, tr := range s.openList {
		last := time.Unix(0, tr.last.Load())
		if now.Sub(last) > idle {
			drop = append(drop, c)
		}
	}
	s.mu.Unlock()
	for _, c := range drop {
		_ = c.Close()
	}
}

func (s *Server) touch(c net.Conn) {
	s.mu.Lock()
	tr := s.openList[c]
	s.mu.Unlock()
	if tr != nil {
		tr.last.Store(time.Now().UnixNano())
	}
}

func (s *Server) sleep(d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	s.mu.Lock()
	wake := s.wake
	s.mu.Unlock()
	select {
	case <-t.C:
	case <-s.stop:
	case <-wake:
	}
}

func (s *Server) authWait() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authDelay
}

func (s *Server) put(p string, data []byte) error {
	dir := path.Dir(path.Clean(p))
	if dir != "/" && dir != "." {
		parts := splitPath(dir)
		cur := ""
		for _, part := range parts {
			cur += "/" + part
			_ = s.mem.FileCmd.Filecmd(sftp.NewRequest("Mkdir", cur))
		}
	}
	req := sftp.NewRequest("Put", p)
	req.Flags = 0x02 | 0x08 | 0x10
	w, err := s.mem.FilePut.Filewrite(req)
	if err != nil {
		return err
	}
	if _, err := w.WriteAt(data, 0); err != nil {
		return err
	}
	if c, ok := w.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

func splitPath(p string) []string {
	p = path.Clean(p)
	if p == "/" || p == "." {
		return nil
	}
	var out []string
	for _, part := range splitSlash(p) {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func splitSlash(p string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(p); i++ {
		if i == len(p) || p[i] == '/' {
			out = append(out, p[start:i])
			start = i + 1
		}
	}
	return out
}

func (s *Server) logOp(id int, op, p string) {
	s.opsMu.Lock()
	s.ops = append(s.ops, fmt.Sprintf("%d %s %s", id, op, p))
	s.opsMu.Unlock()
}

func (s *Server) logAuth(method string) {
	s.opsMu.Lock()
	s.auths = append(s.auths, method)
	s.opsMu.Unlock()
}

func (s *Server) logReq(typ string) {
	s.opsMu.Lock()
	s.reqs = append(s.reqs, typ)
	s.opsMu.Unlock()
}

type connHandler struct {
	s   *Server
	tcp net.Conn
	id  int
}

func (h *connHandler) handlers() sftp.Handlers {
	return sftp.Handlers{FileGet: h, FilePut: h, FileCmd: h, FileList: h}
}

func (h *connHandler) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	h.s.touch(h.tcp)
	if err := h.s.begin("open", r.Filepath, h.tcp, h.id); err != nil {
		return nil, err
	}
	if n, ok := h.s.genOf(r.Filepath); ok {
		return &fileReader{s: h.s, tcp: h.tcp, id: h.id, path: r.Filepath, size: n}, nil
	}
	rd, err := h.s.mem.FileGet.Fileread(r)
	if err != nil {
		return nil, err
	}
	return &fileReader{s: h.s, tcp: h.tcp, id: h.id, path: r.Filepath, inner: rd, size: -1}, nil
}

func (h *connHandler) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	h.s.touch(h.tcp)
	if err := h.s.begin("open", r.Filepath, h.tcp, h.id); err != nil {
		return nil, err
	}
	if isSink(r.Filepath) {
		return &fileWriter{s: h.s, tcp: h.tcp, id: h.id, path: r.Filepath, sink: h.s.sinkFor(r.Filepath)}, nil
	}
	w, err := h.s.mem.FilePut.Filewrite(r)
	if err != nil {
		return nil, err
	}
	return &fileWriter{s: h.s, tcp: h.tcp, id: h.id, path: r.Filepath, inner: w}, nil
}

func (h *connHandler) Filecmd(r *sftp.Request) error {
	h.s.touch(h.tcp)
	op := "cmd"
	if r.Method == "Remove" {
		op = "remove"
	}
	if err := h.s.begin(op, r.Filepath, h.tcp, h.id); err != nil {
		return err
	}
	if r.Method == "Remove" && strings.HasPrefix(path.Clean(r.Filepath), "/auto/") {
		return nil
	}
	return h.s.mem.FileCmd.Filecmd(r)
}

func (h *connHandler) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	return h.list(r, false)
}

func (h *connHandler) Lstat(r *sftp.Request) (sftp.ListerAt, error) {
	return h.list(r, true)
}

func (h *connHandler) list(r *sftp.Request, lstat bool) (sftp.ListerAt, error) {
	h.s.touch(h.tcp)
	op := "stat"
	if r.Method == "List" {
		op = "readdir"
	}
	if err := h.s.begin(op, r.Filepath, h.tcp, h.id); err != nil {
		return nil, err
	}
	if r.Method != "List" {
		if fi, ok := h.s.virtual(r.Filepath); ok {
			return sliceLister{fi}, nil
		}
	}
	var ls sftp.ListerAt
	var err error
	if lstat {
		if lister, ok := h.s.mem.FileList.(sftp.LstatFileLister); ok {
			ls, err = lister.Lstat(r)
		}
	}
	if ls == nil && err == nil {
		ls, err = h.s.mem.FileList.Filelist(r)
	}
	if r.Method == "List" {
		extra := h.s.virtualChildren(r.Filepath)
		if err != nil && len(extra) == 0 {
			return nil, err
		}
		var have []os.FileInfo
		if err == nil {
			have, _ = readAll(ls)
		}
		have = append(have, extra...)
		return sliceLister(have), nil
	}
	return ls, err
}

func (s *Server) begin(op, p string, tcp net.Conn, id int) error {
	s.touch(tcp)
	s.mu.Lock()
	delay := s.delay
	s.mu.Unlock()
	if delay > 0 {
		s.sleep(delay)
	}
	if op != "cmd" {
		s.logOp(id, op, p)
	}
	if err := s.maybeStall(op); err != nil {
		return err
	}
	s.mu.Lock()
	drop := s.dropOp == op
	reply := s.replyOp == op
	code := s.replyCode
	text := s.replyText
	s.mu.Unlock()
	if drop {
		if tcp != nil {
			_ = tcp.Close()
		}
		return io.ErrClosedPipe
	}
	if reply {
		return &codedErr{text: text, err: fx(code)}
	}
	return nil
}

func (s *Server) maybeStall(op string) error {
	s.mu.Lock()
	on := s.stallOp == op
	after := false
	if s.stallAfterOp == op && s.stallAfterN > 0 {
		s.counts[op]++
		after = s.counts[op] > s.stallAfterN
	}
	ch := s.stallCh
	s.mu.Unlock()
	if !on && !after {
		return nil
	}
	if ch == nil {
		return nil
	}
	<-ch
	return io.ErrClosedPipe
}

func (s *Server) genOf(p string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.gen[path.Clean(p)]
	return n, ok
}

func (s *Server) sinkFor(p string) *sinkFile {
	p = path.Clean(p)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sinks[p] == nil {
		s.sinks[p] = &sinkFile{h: sha256.New(), pend: map[int64][]byte{}}
	}
	return s.sinks[p]
}

func isSink(p string) bool {
	p = path.Clean(p)
	return p == "/sink" || len(p) > len("/sink/") && p[:len("/sink/")] == "/sink/"
}

func (s *Server) virtual(p string) (os.FileInfo, bool) {
	p = path.Clean(p)
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.HasPrefix(p, "/auto/") {
		return vinfo{name: path.Base(p), size: 1, mode: 0o644, mod: time.Unix(0, 0).UTC()}, true
	}
	if n, ok := s.gen[p]; ok {
		return vinfo{name: path.Base(p), size: n, mode: 0o644, mod: time.Unix(0, 0).UTC()}, true
	}
	if sk, ok := s.sinks[p]; ok {
		sk.mu.Lock()
		n := sk.n
		sk.mu.Unlock()
		return vinfo{name: path.Base(p), size: n, mode: 0o644, mod: time.Unix(0, 0).UTC()}, true
	}
	return nil, false
}

func (s *Server) virtualChildren(dir string) []os.FileInfo {
	dir = path.Clean(dir)
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []os.FileInfo
	add := func(p string, n int64) {
		if path.Dir(p) == dir {
			out = append(out, vinfo{name: path.Base(p), size: n, mode: 0o644, mod: time.Unix(0, 0).UTC()})
		}
	}
	for p, n := range s.gen {
		add(p, n)
	}
	for p, sk := range s.sinks {
		sk.mu.Lock()
		n := sk.n
		sk.mu.Unlock()
		add(p, n)
	}
	return out
}

type codedErr struct {
	text string
	err  error
}

func (e *codedErr) Error() string { return e.text }
func (e *codedErr) Unwrap() error { return e.err }

func fx(code uint32) error {
	switch code {
	case 1:
		return sftp.ErrSSHFxEOF
	case 2:
		return sftp.ErrSSHFxNoSuchFile
	case 3:
		return sftp.ErrSSHFxPermissionDenied
	case 5:
		return sftp.ErrSSHFxBadMessage
	case 6:
		return sftp.ErrSSHFxNoConnection
	case 7:
		return sftp.ErrSSHFxConnectionLost
	case 8:
		return sftp.ErrSSHFxOpUnsupported
	default:
		return sftp.ErrSSHFxFailure
	}
}

type vinfo struct {
	name string
	size int64
	mode os.FileMode
	mod  time.Time
}

func (v vinfo) Name() string       { return v.name }
func (v vinfo) Size() int64        { return v.size }
func (v vinfo) Mode() os.FileMode  { return v.mode }
func (v vinfo) ModTime() time.Time { return v.mod }
func (v vinfo) IsDir() bool        { return v.mode.IsDir() }
func (v vinfo) Sys() any           { return nil }

type sliceLister []os.FileInfo

func (s sliceLister) ListAt(ls []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(s)) {
		return 0, io.EOF
	}
	n := copy(ls, s[offset:])
	if n < len(ls) {
		return n, io.EOF
	}
	return n, nil
}

func readAll(l sftp.ListerAt) ([]os.FileInfo, error) {
	if l == nil {
		return nil, nil
	}
	var out []os.FileInfo
	buf := make([]os.FileInfo, 64)
	var off int64
	for {
		n, err := l.ListAt(buf, off)
		out = append(out, buf[:n]...)
		off += int64(n)
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		if n == 0 {
			return out, nil
		}
	}
}

type fileReader struct {
	s     *Server
	tcp   net.Conn
	id    int
	path  string
	inner io.ReaderAt
	size  int64
	once  sync.Once
	got   atomic.Int64
}

func (r *fileReader) ReadAt(p []byte, off int64) (int, error) {
	r.s.touch(r.tcp)
	r.once.Do(func() { r.s.logOp(r.id, "read", r.path) })
	if err := r.s.faultOnly("read", r.tcp); err != nil {
		return 0, err
	}
	r.s.pace(len(p))
	var n int
	var err error
	if r.inner == nil {
		if off >= r.size {
			return 0, io.EOF
		}
		if off+int64(len(p)) > r.size {
			p = p[:r.size-off]
		}
		for i := range p {
			p[i] = GenByte(off + int64(i))
		}
		n = len(p)
	} else {
		n, err = r.inner.ReadAt(p, off)
		p = p[:n]
	}
	r.flip(p, off)
	if lim, ok := r.s.trunc(); ok {
		base := r.got.Load()
		if off >= lim || base >= lim {
			return 0, io.ErrUnexpectedEOF
		}
		if off+int64(n) > lim {
			n = int(lim - off)
			r.got.Add(int64(n))
			return n, io.ErrUnexpectedEOF
		}
	}
	r.got.Add(int64(n))
	return n, err
}

func (r *fileReader) flip(p []byte, off int64) {
	at, ok := r.s.corrupt()
	if !ok {
		return
	}
	if at >= off && at < off+int64(len(p)) {
		p[at-off] ^= 0xff
	}
}

func (s *Server) faultOnly(op string, tcp net.Conn) error {
	if err := s.maybeStall(op); err != nil {
		return err
	}
	s.mu.Lock()
	drop := s.dropOp == op
	reply := s.replyOp == op
	code := s.replyCode
	text := s.replyText
	s.mu.Unlock()
	if drop {
		if tcp != nil {
			_ = tcp.Close()
		}
		return io.ErrClosedPipe
	}
	if reply {
		return &codedErr{text: text, err: fx(code)}
	}
	return nil
}

func (s *Server) trunc() (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.truncAt, s.truncSet
}

func (s *Server) corrupt() (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.corruptAt, s.corruptSet
}

func (s *Server) pace(n int) {
	s.mu.Lock()
	bps := s.slowBPS
	s.mu.Unlock()
	if bps <= 0 || n <= 0 {
		return
	}
	s.sleep(time.Duration(n) * time.Second / time.Duration(bps))
}

type fileWriter struct {
	s     *Server
	tcp   net.Conn
	id    int
	path  string
	inner io.WriterAt
	sink  *sinkFile
	once  sync.Once
}

func (w *fileWriter) WriteAt(p []byte, off int64) (int, error) {
	w.s.touch(w.tcp)
	w.once.Do(func() { w.s.logOp(w.id, "write", w.path) })
	if err := w.s.faultOnly("write", w.tcp); err != nil {
		return 0, err
	}
	w.s.pace(len(p))
	if w.sink != nil {
		return w.sink.writeAt(p, off)
	}
	return w.inner.WriteAt(p, off)
}

func (w *fileWriter) Close() error {
	w.s.touch(w.tcp)
	w.s.logOp(w.id, "close", w.path)
	if err := w.s.faultOnly("close", w.tcp); err != nil {
		return err
	}
	if c, ok := w.inner.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

func (sk *sinkFile) writeAt(p []byte, off int64) (int, error) {
	sk.mu.Lock()
	defer sk.mu.Unlock()
	buf := append([]byte(nil), p...)
	sk.pend[off] = buf
	sk.n += int64(len(p))
	for {
		b, ok := sk.pend[sk.next]
		if !ok {
			break
		}
		delete(sk.pend, sk.next)
		_, _ = sk.h.Write(b)
		sk.next += int64(len(b))
	}
	return len(p), nil
}
