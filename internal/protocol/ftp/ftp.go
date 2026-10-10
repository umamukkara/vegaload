// Package ftp implements the "ftp" protocol.Protocol driver. It speaks
// plain FTP and FTPS. The control connection is one command at a time, so
// a call takes one session from a pool and gives it back. Every transfer
// also opens a data connection.
//
// The client library sets no deadline after the TCP connect. Each call sets
// a deadline on the control connection and on each data connection, and a
// watchdog closes those connections when the budget ends.
//
// Uploads and deletes change files on the server. They need allow_writes,
// and a fixed path or a delete also needs allow_admin. An account that can
// only touch a scratch directory is the real lock.
package ftp

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jlaffaye/ftp"

	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/protocol/xfercommon"
)

// Options are the -opt keys this driver accepts.
var Options = []string{
	"mode", "path", "size", "fill", "expect_size", "expect_sha256", "expect",
	"limit", "hidden", "username", "password_env", "tls", "tls_verify",
	"sessions", "connection", "data_host", "epsv", "allow_writes", "allow_admin",
}

const (
	modeConnect   = "connect"
	modeDownload  = "download"
	modeUpload    = "upload"
	modeList      = "list"
	modeStat      = "stat"
	modeDelete    = "delete"
	modeRoundtrip = "roundtrip"
)

// Entry is one name from a listing.
type Entry struct {
	Name string
	Type string
	Size int64
	Time time.Time
}

// Timing is how long the parts of a call took, in milliseconds.
type Timing struct {
	ConnectMs  int64
	LoginMs    int64
	TransferMs int64
	UploadMs   int64
	DownloadMs int64
	DeleteMs   int64
}

// Reply is what a call returns besides the byte counts.
type Reply struct {
	Entries []Entry
	Size    int64
	Total   int
	Timing  Timing
	Text    string
}

// Driver is an FTP protocol.Protocol.
type Driver struct {
	target  protocol.Target
	timeout time.Duration

	host, port      string
	username        string
	password        string
	tlsMode         string
	tlsVerify       bool
	perCall         bool
	sessions        int
	dataHostControl bool
	epsvOff         bool
	allowWrites     bool
	allowAdmin      bool
	script          bool

	mode          string
	path          string
	size          int64
	sizeSet       bool
	fill          string
	expectSize    int64
	expectSizeSet bool
	expectSHA     string
	expect        string
	limit         int
	hidden        bool

	pool   *pool
	tlsCfg *tls.Config
	owns   bool
}

type pool struct {
	mu     sync.Mutex
	slots  chan struct{}
	idle   []*session
	closed bool
	salt   string
	seq    atomic.Uint64
}

type session struct {
	ctx             context.Context
	srv             *ftp.ServerConn
	ctrl            net.Conn
	ctrlHost        string
	mu              sync.Mutex
	data            []net.Conn
	readN           atomic.Int64
	writeN          atomic.Int64
	listHidden      bool
	implicit        bool
	tlsOn           bool
	dataHostControl bool
	tlsCfg          *tls.Config
	reused          bool
	discard         bool
	connectMs       int64
	loginMs         int64
}

// New builds a driver. It does not connect.
func New(target protocol.Target, timeout time.Duration) (*Driver, error) {
	return build(target, timeout, nil, false)
}

// NewConn is New for a script. password is already known, so password_env
// is not read. The job of each call is set with Call.
func NewConn(target protocol.Target, timeout time.Duration, password *string) (*Driver, error) {
	return build(target, timeout, password, true)
}

func build(target protocol.Target, timeout time.Duration, password *string, script bool) (*Driver, error) {
	d := &Driver{target: target, timeout: timeout, script: script, owns: true}
	if err := d.parseURL(); err != nil {
		return nil, err
	}
	if err := d.readConn(password); err != nil {
		return nil, err
	}
	if !script {
		if err := d.readJob(); err != nil {
			return nil, err
		}
	}
	var salt [4]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return nil, fmt.Errorf("ftp: %w", err)
	}
	d.pool = &pool{slots: make(chan struct{}, d.sessions), salt: hex.EncodeToString(salt[:])}
	if d.tlsMode != "none" {
		d.tlsCfg = &tls.Config{
			ServerName:         d.host,
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: !d.tlsVerify,
			ClientSessionCache: tls.NewLRUClientSessionCache(32),
		}
	}
	return d, nil
}

// Call returns a driver for one script call. It shares the pool. Close
// on it does nothing.
func (d *Driver) Call(opts map[string]string, body []byte, timeout time.Duration) (*Driver, error) {
	nd := &Driver{
		target:          d.target,
		timeout:         timeout,
		host:            d.host,
		port:            d.port,
		username:        d.username,
		password:        d.password,
		tlsMode:         d.tlsMode,
		tlsVerify:       d.tlsVerify,
		perCall:         d.perCall,
		sessions:        d.sessions,
		dataHostControl: d.dataHostControl,
		epsvOff:         d.epsvOff,
		allowWrites:     d.allowWrites,
		allowAdmin:      d.allowAdmin,
		script:          d.script,
		pool:            d.pool,
		tlsCfg:          d.tlsCfg,
		owns:            false,
	}
	nd.target.Body = append([]byte(nil), body...)
	nd.target.Options = opts
	if err := nd.target.RejectUnknownOptions(Options...); err != nil {
		return nil, fmt.Errorf("ftp: %w", err)
	}
	if err := nd.readJob(); err != nil {
		return nil, err
	}
	return nd, nil
}

// Name is the protocol name.
func (d *Driver) Name() string { return "ftp" }

// Do runs one iteration.
func (d *Driver) Do(ctx context.Context) (protocol.Result, error) {
	res, _ := d.Run(ctx)
	return res, nil
}

// Run runs one call and returns the reply a script reads.
func (d *Driver) Run(parent context.Context) (protocol.Result, Reply) {
	if err := d.refuse(); err != nil {
		return protocol.Result{Err: err}, Reply{}
	}
	ctx, cancel := context.WithTimeout(parent, d.timeout)
	defer cancel()
	var held atomic.Pointer[session]
	stop := context.AfterFunc(ctx, func() {
		if s := held.Load(); s != nil {
			s.closeRecorded()
		}
	})
	defer stop()

	rep, sent, got, reused, err := d.exec(ctx, &held)
	if err != nil {
		return protocol.Result{BytesSent: sent, BytesReceived: got, Err: d.explain(parent, ctx, err, reused)}, rep
	}
	return protocol.Result{Success: true, BytesSent: sent, BytesReceived: got}, rep
}

// Close quits idle sessions. A Call driver does nothing.
func (d *Driver) Close() error {
	if !d.owns || d.pool == nil {
		return nil
	}
	d.pool.mu.Lock()
	d.pool.closed = true
	idle := d.pool.idle
	d.pool.idle = nil
	d.pool.mu.Unlock()
	done := make(chan struct{})
	go func() {
		for _, s := range idle {
			s.close()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		for _, s := range idle {
			s.closeRecorded()
		}
	}
	return nil
}

func (d *Driver) exec(ctx context.Context, held *atomic.Pointer[session]) (rep Reply, sent, got int64, reused bool, err error) {
	s, err := d.acquire(ctx, held)
	if err != nil {
		return Reply{}, 0, 0, false, err
	}
	reused = s.reused
	keep := false
	defer func() { d.finish(s, keep) }()
	if dl, ok := ctx.Deadline(); ok && s.ctrl != nil {
		_ = s.ctrl.SetDeadline(dl)
	}
	s.readN.Store(0)
	s.writeN.Store(0)
	id := d.pool.salt + "-" + fmt.Sprintf("%d", d.pool.seq.Add(1))
	path := strings.ReplaceAll(d.path, "{id}", id)
	if err = xfercommonCheck(path); err != nil {
		return Reply{}, 0, 0, reused, err
	}
	body := append([]byte(nil), d.target.Body...)
	if len(body) > 0 {
		body = []byte(strings.ReplaceAll(string(body), "{id}", id))
	}
	start := time.Now()
	switch d.mode {
	case modeConnect:
		err = s.srv.NoOp()
		rep.Text = "logged in"
	case modeDownload:
		got, rep, err = d.download(s, path)
	case modeUpload:
		sent, rep, err = d.upload(s, path, body)
	case modeList:
		got, rep, err = d.list(s, path)
	case modeStat:
		rep, err = d.stat(s, path)
	case modeDelete:
		err = s.srv.Delete(path)
		rep.Text = "deleted " + path
	case modeRoundtrip:
		sent, got, rep, err = d.roundtrip(ctx, s, path, body)
	default:
		err = fmt.Errorf("ftp: mode %q is not supported", d.mode)
	}
	rep.Timing.TransferMs = time.Since(start).Milliseconds()
	if !s.reused {
		rep.Timing.ConnectMs = s.connectMs
		rep.Timing.LoginMs = s.loginMs
	}
	if s.readN.Load() > got {
		got = s.readN.Load()
	}
	if s.writeN.Load() > sent {
		sent = s.writeN.Load()
	}
	keep = keepSession(err)
	return rep, sent, got, reused, err
}

func xfercommonCheck(path string) error {
	if err := xfercommon.CheckPath(path); err != nil {
		return fmt.Errorf("ftp: %w", err)
	}
	return nil
}

func (d *Driver) acquire(ctx context.Context, held *atomic.Pointer[session]) (*session, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case d.pool.slots <- struct{}{}:
	}
	s := &session{
		ctx: ctx, listHidden: d.hidden && d.mode == modeList,
		implicit: d.tlsMode == "implicit", tlsOn: d.tlsMode != "none",
		dataHostControl: d.dataHostControl, tlsCfg: d.tlsCfg,
		discard: d.perCall || d.mode == modeConnect,
	}
	held.Store(s)
	if !s.discard {
		if idle := d.pool.takeIdle(d.mode == modeList, d.hidden); idle != nil {
			idle.ctx = ctx
			idle.reused = true
			idle.discard = false
			held.Store(idle)
			return idle, nil
		}
	}
	if err := d.dial(ctx, s); err != nil {
		held.Store(s)
		d.finish(s, false)
		return nil, err
	}
	return s, nil
}

func (p *pool) takeIdle(list, hidden bool) *session {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.idle) - 1; i >= 0; i-- {
		s := p.idle[i]
		if list && s.listHidden != hidden {
			continue
		}
		p.idle = append(p.idle[:i], p.idle[i+1:]...)
		return s
	}
	return nil
}

func (d *Driver) finish(s *session, keep bool) {
	if s == nil {
		return
	}
	if !keep || s.discard || s.ctrl == nil {
		s.close()
	} else {
		s.dropData()
		_ = s.ctrl.SetDeadline(time.Time{})
		d.pool.mu.Lock()
		closed := d.pool.closed
		if !closed {
			d.pool.idle = append(d.pool.idle, s)
		}
		d.pool.mu.Unlock()
		if closed {
			s.close()
		}
	}
	select {
	case <-d.pool.slots:
	default:
	}
}

func (d *Driver) dial(ctx context.Context, s *session) error {
	s.ctx = ctx
	opts := []ftp.DialOption{
		ftp.DialWithDialFunc(s.dialFunc),
		ftp.DialWithContext(ctx),
	}
	if d.epsvOff {
		opts = append(opts, ftp.DialWithDisabledEPSV(true))
	}
	if s.listHidden {
		opts = append(opts, ftp.DialWithForceListHidden(true))
	}
	switch d.tlsMode {
	case "implicit":
		opts = append(opts, ftp.DialWithTLS(d.tlsCfg))
	case "explicit":
		opts = append(opts, ftp.DialWithExplicitTLS(d.tlsCfg))
	}
	t0 := time.Now()
	conn, err := ftp.Dial(net.JoinHostPort(d.host, d.port), opts...)
	if err != nil {
		return err
	}
	s.connectMs = time.Since(t0).Milliseconds()
	t1 := time.Now()
	if err := conn.Login(d.username, d.password); err != nil {
		_ = conn.Quit()
		return err
	}
	s.loginMs = time.Since(t1).Milliseconds()
	s.srv = conn
	return nil
}

func (s *session) dialFunc(network, address string) (net.Conn, error) {
	first := s.ctrl == nil
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if !first {
		if s.dataHostControl {
			if s.ctrlHost != "" {
				host = s.ctrlHost
			}
		} else if ip := net.ParseIP(host); ip == nil {
			return nil, fmt.Errorf("ftp: the PASV host is not an IP address")
		}
		address = net.JoinHostPort(host, port)
	}
	raw, err := (&net.Dialer{}).DialContext(s.ctx, network, address)
	if err != nil {
		if first {
			return nil, fmt.Errorf("ftp: could not connect to %s: %s", address, err.Error())
		}
		which := "control"
		if !s.dataHostControl {
			which = "announced"
		}
		return nil, fmt.Errorf("ftp: could not open the data connection to %s: %s (data_host=%s)", address, err.Error(), which)
	}
	conn := net.Conn(raw)
	if s.tlsOn && (!first || s.implicit) {
		tc := tls.Client(raw, s.tlsCfg)
		// A data connection starts its handshake on the first read or write.
		// Handshaking here, before the transfer command is sent, stalls
		// servers that begin TLS only after that command.
		// An implicit control connection has no cleartext banner, so it
		// handshakes now.
		if first && s.implicit {
			if err := tc.HandshakeContext(s.ctx); err != nil {
				raw.Close()
				msg := fmt.Sprintf("ftp: the TLS handshake with the server failed (control): %s", err.Error())
				if certErr(err) {
					msg += ": use -insecure for a test server"
				}
				return nil, errors.New(msg)
			}
		}
		conn = tc
	}
	wrapped := &byteConn{Conn: conn}
	if !first {
		wrapped.readN = &s.readN
		wrapped.writeN = &s.writeN
		if dl, ok := s.ctx.Deadline(); ok {
			_ = wrapped.SetDeadline(dl)
		}
		s.mu.Lock()
		s.data = append(s.data, wrapped)
		s.mu.Unlock()
	} else {
		s.mu.Lock()
		s.ctrl = wrapped
		if ta, ok := raw.RemoteAddr().(*net.TCPAddr); ok {
			s.ctrlHost = ta.IP.String()
		}
		s.mu.Unlock()
	}
	return wrapped, nil
}

func (s *session) closeRecorded() {
	s.mu.Lock()
	data := append([]net.Conn(nil), s.data...)
	ctrl := s.ctrl
	s.mu.Unlock()
	for i := len(data) - 1; i >= 0; i-- {
		_ = data[i].Close()
	}
	if ctrl != nil {
		_ = ctrl.Close()
	}
}

func (s *session) dropData() {
	s.mu.Lock()
	data := s.data
	s.data = nil
	s.mu.Unlock()
	for _, c := range data {
		_ = c.Close()
	}
}

func (s *session) close() {
	s.dropData()
	if s.srv != nil {
		done := make(chan struct{})
		go func() {
			_ = s.srv.Quit()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			s.closeRecorded()
		}
		return
	}
	s.closeRecorded()
}

type byteConn struct {
	net.Conn
	readN  *atomic.Int64
	writeN *atomic.Int64
}

func (c *byteConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 && c.readN != nil {
		c.readN.Add(int64(n))
	}
	return n, err
}

func (c *byteConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 && c.writeN != nil {
		c.writeN.Add(int64(n))
	}
	return n, err
}

func (c *byteConn) Handshake() error {
	if h, ok := c.Conn.(interface{ Handshake() error }); ok {
		return h.Handshake()
	}
	return nil
}

func keepSession(err error) bool {
	if err == nil {
		return true
	}
	var te *textproto.Error
	if errors.As(err, &te) {
		return te.Code != 421
	}
	return false
}

func certErr(err error) bool {
	var u x509.UnknownAuthorityError
	var h x509.HostnameError
	return errors.As(err, &u) || errors.As(err, &h) || strings.Contains(err.Error(), "certificate")
}

func (d *Driver) explain(parent, ctx context.Context, err error, reused bool) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if callTimedOut(ctx, err) {
		return errors.New(d.redact(fmt.Sprintf("ftp: timed out after %s", d.timeout)))
	}
	if strings.HasPrefix(err.Error(), "ftp:") {
		return errors.New(d.redact(cut(err.Error(), 300)))
	}
	var te *textproto.Error
	if errors.As(err, &te) {
		msg := cut(strings.TrimSpace(te.Msg), 300)
		if te.Code == 530 {
			return errors.New(d.redact(fmt.Sprintf("ftp: the server refused the login (user %q): %s", d.username, msg)))
		}
		if te.Code == 421 {
			if reused {
				return errors.New(d.redact(fmt.Sprintf("ftp: the session to %s was closed by the server: %s", d.addr(), msg)))
			}
			return errors.New(d.redact(fmt.Sprintf("ftp: the server has no room for another session (421): %s. sessions is %d", msg, d.sessions)))
		}
		return errors.New(d.redact(fmt.Sprintf("ftp: %d %s", te.Code, msg)))
	}
	low := strings.ToLower(err.Error())
	if strings.Contains(low, "incorrect") || strings.Contains(err.Error(), "530") {
		return errors.New(d.redact(fmt.Sprintf("ftp: the server refused the login (user %q): %s", d.username, cut(err.Error(), 300))))
	}
	if reused && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || isReset(err)) {
		return errors.New(d.redact(fmt.Sprintf("ftp: the session to %s was closed by the server: %s", d.addr(), cut(err.Error(), 300))))
	}
	if certErr(err) {
		return errors.New(d.redact(fmt.Sprintf("ftp: the TLS handshake with the server failed (control): %s: use -insecure for a test server", cut(err.Error(), 300))))
	}
	return errors.New(d.redact(fmt.Sprintf("ftp: %s", cut(err.Error(), 300))))
}

func (d *Driver) addr() string { return net.JoinHostPort(d.host, d.port) }

func (d *Driver) redact(s string) string {
	if d.password == "" {
		return s
	}
	return strings.ReplaceAll(s, d.password, "****")
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func callTimedOut(ctx context.Context, err error) bool {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		if dl, ok := ctx.Deadline(); ok && time.Until(dl) <= time.Millisecond {
			return true
		}
	}
	return false
}

// isReset reports that the connection died under us. On Linux this is a
// reset or a broken pipe. On Windows the text differs ("An established
// connection was aborted by the software in your host machine", "forcibly
// closed"), so a network error that is not a timeout counts too.
func isReset(err error) bool {
	var oe *net.OpError
	if errors.As(err, &oe) && !oe.Timeout() {
		return true
	}
	low := strings.ToLower(err.Error())
	for _, m := range []string{"connection reset", "broken pipe", "forcibly closed", "connection was aborted", "connection aborted"} {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}
