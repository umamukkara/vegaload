// Package sftp implements the "sftp" protocol.Protocol driver. It speaks
// the SSH File Transfer Protocol. One SSH connection is shared by the
// calls that use it, and those calls are multiplexed on one channel.
//
// The SSH and SFTP libraries do not take a context. Each call runs a
// watchdog that closes the connection when the budget ends. A timeout
// closes that shared connection, so other calls on it fail too.
package sftp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/protocol/xfercommon"
)

// Options are the -opt keys this driver accepts.
var Options = []string{
	"mode", "path", "size", "fill", "expect_size", "expect_sha256", "expect",
	"limit", "hidden", "username", "password_env", "key_file", "key_passphrase_env",
	"host_key", "connections", "connection", "allow_writes", "allow_admin",
}

// Driver is an SFTP protocol.Protocol.
type Driver struct {
	target  protocol.Target
	timeout time.Duration

	host, port  string
	username    string
	password    string
	passphrase  string
	signer      ssh.Signer
	hostKeys    []string
	insecure    bool
	perCall     bool
	conns       int
	allowWrites bool
	allowAdmin  bool
	script      bool

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

	shared *shared
	owns   bool
}

type shared struct {
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
	slots  []*slot
	ids    *xfercommon.IDs
	rr     atomic.Uint64
}

type slot struct {
	mu      sync.Mutex
	current *live
	fly     *flight
}

type flight struct {
	done chan struct{}
	err  error
}

type live struct {
	mu        sync.Mutex
	raw       net.Conn
	sshc      *ssh.Client
	cli       *sftp.Client
	dead      atomic.Bool
	once      sync.Once
	cmu       sync.Mutex
	why       string
	connectMs int64
	loginMs   int64
}

// New builds a driver. It does not connect.
func New(target protocol.Target, timeout time.Duration) (*Driver, error) {
	return build(target, timeout, nil, nil, false)
}

// NewConn is New for a script. password and passphrase are already known,
// so the environment variables are not read. The job of each call is set
// with Call.
func NewConn(target protocol.Target, timeout time.Duration, password, passphrase *string) (*Driver, error) {
	return build(target, timeout, password, passphrase, true)
}

func build(target protocol.Target, timeout time.Duration, password, passphrase *string, script bool) (*Driver, error) {
	d := &Driver{target: target, timeout: timeout, script: script, owns: true}
	if err := d.parseURL(); err != nil {
		return nil, err
	}
	if err := d.readConn(password, passphrase); err != nil {
		return nil, err
	}
	if !script {
		if err := d.target.RejectUnknownOptions(Options...); err != nil {
			return nil, fmt.Errorf("sftp: %w", err)
		}
		if err := d.readJob(); err != nil {
			return nil, err
		}
	}
	ids, err := xfercommon.NewIDs()
	if err != nil {
		return nil, fmt.Errorf("sftp: %w", err)
	}
	slots := make([]*slot, d.conns)
	for i := range slots {
		slots[i] = &slot{}
	}
	d.shared = &shared{slots: slots, ids: ids}
	return d, nil
}

// Call returns a driver for one script call. It shares the connections.
// Close on it does nothing.
func (d *Driver) Call(opts map[string]string, body []byte, timeout time.Duration) (*Driver, error) {
	nd := *d
	nd.owns = false
	nd.timeout = timeout
	nd.target.Body = append([]byte(nil), body...)
	nd.target.Options = opts
	if err := nd.target.RejectUnknownOptions(Options...); err != nil {
		return nil, fmt.Errorf("sftp: %w", err)
	}
	if err := nd.readJob(); err != nil {
		return nil, err
	}
	return &nd, nil
}

// Name is the protocol name.
func (d *Driver) Name() string { return "sftp" }

// Do runs one iteration.
func (d *Driver) Do(ctx context.Context) (protocol.Result, error) {
	res, _ := d.Run(ctx)
	return res, nil
}

// Run runs one call and returns the reply a script reads.
func (d *Driver) Run(parent context.Context) (protocol.Result, xfercommon.Reply) {
	if err := xfercommon.Refuse("sftp", d.mode, d.path, d.allowWrites, d.allowAdmin, d.script); err != nil {
		return protocol.Result{Err: err}, xfercommon.Reply{}
	}
	ctx, cancel := context.WithTimeout(parent, d.timeout)
	defer cancel()
	var held atomic.Pointer[live]
	stop := context.AfterFunc(ctx, func() {
		if l := held.Load(); l != nil {
			l.kill()
		}
	})
	defer stop()

	rep, sent, got, err := d.perform(ctx, &held)
	if err != nil {
		return protocol.Result{BytesSent: sent, BytesReceived: got, Err: d.explain(parent, ctx, err)}, rep
	}
	return protocol.Result{Success: true, BytesSent: sent, BytesReceived: got}, rep
}

// Close closes the shared connections. A Call driver does nothing.
func (d *Driver) Close() error {
	if !d.owns || d.shared == nil {
		return nil
	}
	d.shared.mu.Lock()
	d.shared.closed = true
	d.shared.mu.Unlock()
	var all []*live
	for _, sl := range d.shared.slots {
		sl.mu.Lock()
		if sl.current != nil {
			all = append(all, sl.current)
			sl.current = nil
		}
		sl.mu.Unlock()
	}
	for _, l := range all {
		l.kill()
	}
	done := make(chan struct{})
	go func() {
		d.shared.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
	return nil
}

func (d *Driver) perform(ctx context.Context, held *atomic.Pointer[live]) (rep xfercommon.Reply, sent, got int64, err error) {
	own := d.perCall || d.mode == modeConnect
	var l *live
	var reused bool
	if own {
		l, err = d.open(ctx, held)
	} else {
		l, reused, err = d.take(ctx, held)
	}
	if err != nil {
		return xfercommon.Reply{}, 0, 0, err
	}
	if held.Load() == nil {
		held.Store(l)
	}
	defer func() {
		if own || l.dead.Load() {
			d.forget(l)
		}
	}()

	id := d.shared.ids.Next()
	path := xfercommon.ReplaceID(d.path, id)
	if err = xfercommon.CheckPath(path); err != nil && d.mode != modeConnect {
		return xfercommon.Reply{}, 0, 0, fmt.Errorf("sftp: %w", err)
	}
	body := append([]byte(nil), d.target.Body...)
	if len(body) > 0 {
		body = []byte(xfercommon.ReplaceID(string(body), id))
	}
	start := time.Now()
	switch d.mode {
	case modeConnect:
		_, err = l.cli.Getwd()
		rep.Text = "logged in"
	case modeDownload:
		got, rep, err = d.download(l, path)
	case modeUpload:
		sent, rep, err = d.upload(l, path, body)
	case modeList:
		rep, err = d.list(l, path)
	case modeStat:
		rep, err = d.stat(l, path)
	case modeDelete:
		err = d.remove(l, path)
		if err == nil {
			rep.Text = "deleted " + path
		}
	case modeRoundtrip:
		sent, got, rep, err = d.roundtrip(l, path, body)
	default:
		err = fmt.Errorf("sftp: mode %q is not supported", d.mode)
	}
	rep.Timing.TransferMs = time.Since(start).Milliseconds()
	if !reused {
		rep.Timing.ConnectMs = l.connectMs
		rep.Timing.LoginMs = l.loginMs
	}
	if err != nil {
		err = d.wrap(path, err, l)
	}
	return rep, sent, got, err
}

func (d *Driver) take(ctx context.Context, held *atomic.Pointer[live]) (*live, bool, error) {
	sl := d.shared.slots[d.shared.next()]
	for {
		sl.mu.Lock()
		if sl.current != nil && !sl.current.dead.Load() {
			l := sl.current
			sl.mu.Unlock()
			held.Store(l)
			return l, true, nil
		}
		if sl.current != nil {
			old := sl.current
			sl.current = nil
			sl.mu.Unlock()
			old.kill()
			continue
		}
		if sl.fly != nil {
			f := sl.fly
			sl.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, false, ctx.Err()
			case <-f.done:
			}
			if f.err != nil {
				return nil, false, f.err
			}
			continue
		}
		f := &flight{done: make(chan struct{})}
		sl.fly = f
		sl.mu.Unlock()

		l, err := d.open(ctx, held)

		var killLater *live
		sl.mu.Lock()
		d.shared.mu.Lock()
		closed := d.shared.closed
		d.shared.mu.Unlock()
		if err == nil && (closed || l.dead.Load()) {
			killLater = l
			err = errors.New("sftp: the server closed the connection during login (too many connections, or MaxStartups?)")
		}
		if err == nil {
			sl.current = l
		}
		f.err = err
		sl.fly = nil
		close(f.done)
		sl.mu.Unlock()
		if killLater != nil {
			killLater.kill()
		}
		if err != nil {
			return nil, false, err
		}
		return l, false, nil
	}
}

func (sh *shared) next() int {
	n := sh.rr.Add(1)
	return int((n - 1) % uint64(len(sh.slots)))
}

func (d *Driver) forget(l *live) {
	if l == nil || d.shared == nil {
		return
	}
	for _, sl := range d.shared.slots {
		sl.mu.Lock()
		if sl.current == l {
			sl.current = nil
		}
		sl.mu.Unlock()
	}
	l.kill()
}

func (d *Driver) open(ctx context.Context, held *atomic.Pointer[live]) (*live, error) {
	l := &live{}
	if held != nil {
		held.Store(l)
	}
	addr := d.addr()
	t0 := time.Now()
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("sftp: could not connect to %s: %s", addr, err.Error())
	}
	l.connectMs = time.Since(t0).Milliseconds()
	// The watchdog may already be closing this attempt. Publish the socket
	// under the same lock kill uses, and drop it if the budget has ended.
	if l.publish(raw, nil, nil) {
		_ = raw.Close()
		return nil, context.DeadlineExceeded
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(dl)
	}
	t1 := time.Now()
	sshConn, chans, reqs, err := ssh.NewClientConn(raw, addr, d.sshConfig())
	if err != nil {
		l.kill()
		return nil, d.loginErr(err)
	}
	sshc := ssh.NewClient(sshConn, chans, reqs)
	if l.publish(nil, sshc, nil) {
		_ = sshc.Close()
		_ = raw.Close()
		return nil, context.DeadlineExceeded
	}
	cli, err := sftp.NewClient(sshc)
	if err != nil {
		l.kill()
		return nil, d.loginErr(err)
	}
	if l.publish(nil, nil, cli) {
		_ = cli.Close()
		l.kill()
		return nil, context.DeadlineExceeded
	}
	l.loginMs = time.Since(t1).Milliseconds()
	if !l.dead.Load() {
		_ = raw.SetDeadline(time.Time{})
	}
	if !d.shared.track(l) {
		l.kill()
		return nil, errors.New("sftp: the server closed the connection during login (too many connections, or MaxStartups?)")
	}
	return l, nil
}

func (d *Driver) sshConfig() *ssh.ClientConfig {
	var auth []ssh.AuthMethod
	if d.signer != nil {
		auth = append(auth, ssh.PublicKeys(d.signer))
	}
	if d.password != "" {
		auth = append(auth, ssh.Password(d.password))
	}
	cfg := &ssh.ClientConfig{
		User: d.username,
		Auth: auth,
		// A server often has several host keys. The server picks the first
		// type in this list that it has. Use the same order as OpenSSH, so
		// the fingerprint that ssh-keyscan shows first is the one we meet.
		HostKeyAlgorithms: hostKeyOrder,
	}
	if d.insecure {
		// Only -insecure skips the host key check.
		cfg.HostKeyCallback = ssh.InsecureIgnoreHostKey()
	} else {
		cfg.HostKeyCallback = d.checkHostKey
	}
	return cfg
}

// hostKeyOrder is the order in which the client offers host key types.
var hostKeyOrder = []string{
	ssh.KeyAlgoED25519,
	ssh.KeyAlgoECDSA256,
	ssh.KeyAlgoECDSA384,
	ssh.KeyAlgoECDSA521,
	ssh.KeyAlgoRSASHA512,
	ssh.KeyAlgoRSASHA256,
	ssh.KeyAlgoRSA,
}

func (d *Driver) checkHostKey(hostname string, remote net.Addr, key ssh.PublicKey) error {
	fp := ssh.FingerprintSHA256(key)
	shown := key.Type() + " " + fp
	if len(d.hostKeys) == 0 {
		return fmt.Errorf("sftp: the host key of %s is not trusted (%s). Check it, then use -opt host_key=%s, or use -insecure for a test server", d.addr(), shown, fp)
	}
	for _, want := range d.hostKeys {
		if want == fp {
			return nil
		}
	}
	return fmt.Errorf("sftp: the host key of %s does not match (%s). A server can have several host keys: list the fingerprint of every one in host_key (ssh-keyscan shows them)", d.addr(), shown)
}

func (sh *shared) track(l *live) bool {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if sh.closed || l.sshc == nil {
		return false
	}
	sh.wg.Add(1)
	go func() {
		defer sh.wg.Done()
		err := l.sshc.Wait()
		l.dead.Store(true)
		if err != nil {
			l.setCause(err.Error())
		} else {
			l.setCause("EOF")
		}
	}()
	return true
}

// publish stores the pieces of a connection. It reports whether kill has
// already run, in which case the caller must close what it just passed.
func (l *live) publish(raw net.Conn, sshc *ssh.Client, cli *sftp.Client) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if raw != nil {
		l.raw = raw
	}
	if sshc != nil {
		l.sshc = sshc
	}
	if cli != nil {
		l.cli = cli
	}
	return l.dead.Load()
}

func (l *live) kill() {
	l.once.Do(func() {
		l.mu.Lock()
		l.dead.Store(true)
		l.setCause("closed")
		raw, sshc, cli := l.raw, l.sshc, l.cli
		l.mu.Unlock()
		// Close the socket first. Closing the SFTP client waits for the
		// request in progress, and a stalled server never answers it.
		if raw != nil {
			_ = raw.Close()
		}
		if sshc != nil {
			_ = sshc.Close()
		}
		if cli != nil {
			_ = cli.Close()
		}
	})
}

func (l *live) setCause(s string) {
	l.cmu.Lock()
	defer l.cmu.Unlock()
	if l.why == "" {
		l.why = s
	}
}

func (l *live) cause() string {
	l.cmu.Lock()
	defer l.cmu.Unlock()
	if l.why == "" || l.why == "closed" {
		return "EOF"
	}
	return l.why
}

func (d *Driver) loginErr(err error) error {
	msg := err.Error()
	// Our own host key error comes back inside "ssh: handshake failed: ".
	if i := strings.Index(msg, "sftp: the host key"); i >= 0 {
		return errors.New(msg[i:])
	}
	if strings.Contains(msg, "unable to authenticate") {
		return fmt.Errorf("sftp: the server refused the login (user %q) (offered: %s)", d.username, d.offered())
	}
	if strings.Contains(strings.ToLower(msg), "subsystem") {
		return fmt.Errorf("sftp: the server has no sftp subsystem for this user")
	}
	if isLost(err) {
		return errors.New("sftp: the server closed the connection during login (too many connections, or MaxStartups?)")
	}
	return err
}

func (d *Driver) offered() string {
	var parts []string
	if d.signer != nil {
		parts = append(parts, "key")
	}
	if d.password != "" {
		parts = append(parts, "password")
	}
	return strings.Join(parts, ", ")
}

func (d *Driver) wrap(path string, err error, l *live) error {
	if err == nil || strings.HasPrefix(err.Error(), "sftp:") {
		return err
	}
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("sftp: no such file: %s", xfercommon.Cut(path, 300))
	}
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("sftp: permission denied: %s", xfercommon.Cut(path, 300))
	}
	var se *sftp.StatusError
	if errors.As(err, &se) {
		return fmt.Errorf("sftp: the server said: %s (code %d)", xfercommon.Cut(se.Error(), 300), se.Code)
	}
	if isLost(err) || (l != nil && l.dead.Load()) {
		return d.closedErr(l, err)
	}
	return err
}

func (d *Driver) closedErr(l *live, opErr error) error {
	cause := "EOF"
	if l != nil {
		cause = l.cause()
	}
	if cause == "EOF" && opErr != nil && !errors.Is(opErr, io.EOF) {
		cause = opErr.Error()
	}
	return fmt.Errorf("sftp: the connection to %s was closed while this call was running: %s", d.addr(), xfercommon.Cut(cause, 300))
}

func (d *Driver) explain(parent, ctx context.Context, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if callTimedOut(ctx, err) {
		return errors.New(d.redact(fmt.Sprintf("sftp: timed out after %s", d.timeout)))
	}
	if strings.HasPrefix(err.Error(), "sftp:") {
		return errors.New(d.redact(xfercommon.Cut(err.Error(), 300)))
	}
	return errors.New(d.redact(xfercommon.Cut("sftp: "+err.Error(), 300)))
}

func (d *Driver) addr() string { return net.JoinHostPort(d.host, d.port) }

func (d *Driver) redact(s string) string {
	if d.password != "" {
		s = strings.ReplaceAll(s, d.password, "****")
	}
	if d.passphrase != "" {
		s = strings.ReplaceAll(s, d.passphrase, "****")
	}
	return s
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

func isLost(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if strings.Contains(strings.ToLower(err.Error()), "connection lost") {
		return true
	}
	var oe *net.OpError
	if errors.As(err, &oe) && !oe.Timeout() {
		return true
	}
	return false
}
