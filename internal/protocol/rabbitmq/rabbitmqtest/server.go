package rabbitmqtest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	codeNotFound       = 404
	codeLocked         = 405
	codePrecondition   = 406
	codeAccessRefused  = 403
	codeNotAllowed     = 530
	codeNotImplemented = 540
	codeCommandInvalid = 503
	codeUnexpected     = 505
	codeNoRoute        = 312
	codeForced         = 320
)

// Config is the login and vhost set. An empty Users map accepts guest/guest.
// An empty Vhosts list accepts only "/".
type Config struct {
	Users  map[string]string
	Vhosts []string
}

// Pub is one basic.publish the broker accepted into its log, including
// messages that were then returned as not routed.
type Pub struct {
	Exchange     string
	RoutingKey   string
	Body         []byte
	MessageID    string
	ContentType  string
	Expiration   string
	DeliveryMode uint8
	Priority     uint8
	Timestamp    time.Time
	Headers      map[string]any
	Mandatory    bool
}

// ConnInfo is one connection that finished opening.
type ConnInfo struct {
	Heartbeat uint16
	Name      string
	Vhost     string
	User      string
}

// Server is an in-memory AMQP 0-9-1 broker.
type Server struct {
	t      testingT
	ln     net.Listener
	tlsCfg *tls.Config

	mu       sync.Mutex
	users    map[string]string
	vhosts   map[string]bool
	nextConn int
	accepted int
	open     int
	methods  []string
	pubs     []Pub
	queues   map[string]*queue
	ex       map[string]*exchange
	binds    []binding

	stall   string
	drop    string
	chClose string
	chCode  uint16
	chText  string
	nack    bool
	delay   time.Duration
	refuse  bool
	conns   map[int]*conn
	opened  []ConnInfo
}

type testingT interface {
	Helper()
	Cleanup(func())
	Fatalf(string, ...any)
}

type exchange struct {
	name, kind              string
	durable, autoDel, built bool
}

type binding struct {
	queue, exchange, key string
}

type queue struct {
	name                 string
	durable, exclusive   bool
	autoDel              bool
	owner                int
	args                 map[string]any
	ready                []qmsg
	hadConsumer          bool
	unacked, peakUnacked int
}

type qmsg struct {
	exchange, key string
	body          []byte
	props         props
	redelivered   bool
}

type conn struct {
	s         *Server
	id        int
	c         net.Conn
	wmu       sync.Mutex
	mu        sync.Mutex
	chs       map[uint16]*channel
	vhost     string
	user      string
	name      string
	heartbeat uint16
	dead      bool
	partial   map[uint16]*pubIn
}

// pubIn is a basic.publish whose header and body may arrive later, with
// other channels' frames in between.
type pubIn struct {
	exch, key string
	mandatory bool
	gotHeader bool
	size      uint64
	props     props
	body      []byte
	drop      bool
	closeCode uint16
	closeText string
	delay     time.Duration
}

type channel struct {
	id        uint16
	dead      bool
	confirm   bool
	pubTag    uint64
	delTag    uint64
	prefetch  uint16
	global    bool
	consumers map[string]*consumer
	unacked   map[uint64]*held
	rr        int
}

type consumer struct {
	tag, queue string
	noAck      bool
}

type held struct {
	queue, consumer string
	msg             qmsg
}

// Start listens on 127.0.0.1:0 and accepts connections until the test ends.
func Start(t testingT) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := newServer(t, ln, nil)
	go s.accept()
	return s
}

// StartTLS is Start with a certificate the process generated. Clients that
// verify certificates fail. Clients that skip verification succeed.
func StartTLS(t testingT) *Server {
	t.Helper()
	cert, err := selfSigned()
	if err != nil {
		t.Fatalf("cert: %v", err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := newServer(t, ln, &tls.Config{Certificates: []tls.Certificate{cert}})
	go s.accept()
	return s
}

func newServer(t testingT, ln net.Listener, tlsCfg *tls.Config) *Server {
	s := &Server{
		t: t, ln: ln, tlsCfg: tlsCfg,
		users:  map[string]string{"guest": "guest"},
		vhosts: map[string]bool{"/": true},
		queues: map[string]*queue{},
		ex: map[string]*exchange{
			"":           {name: "", kind: "direct", built: true},
			"amq.direct": {name: "amq.direct", kind: "direct", built: true, durable: true},
			"amq.fanout": {name: "amq.fanout", kind: "fanout", built: true, durable: true},
			"amq.topic":  {name: "amq.topic", kind: "topic", built: true, durable: true},
		},
		conns: map[int]*conn{},
	}
	t.Cleanup(func() { _ = s.ln.Close(); s.dropAll() })
	return s
}

// Apply replaces the user and vhost sets. Call it before dialing.
func (s *Server) Apply(cfg Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cfg.Users != nil {
		s.users = cfg.Users
	}
	if cfg.Vhosts != nil {
		s.vhosts = map[string]bool{}
		for _, v := range cfg.Vhosts {
			s.vhosts[v] = true
		}
	}
}

// URL is amqp://127.0.0.1:port, or amqps:// for StartTLS.
func (s *Server) URL() string {
	scheme := "amqp"
	if s.tlsCfg != nil {
		scheme = "amqps"
	}
	return scheme + "://" + s.ln.Addr().String()
}

// Addr is host:port.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// ConnCount is how many TCP connections were accepted.
func (s *Server) ConnCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accepted
}

// OpenConns is how many connections are still open.
func (s *Server) OpenConns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open
}

// Methods is the log of client methods, as "conn/channel name".
func (s *Server) Methods() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.methods))
	copy(out, s.methods)
	return out
}

// Push puts a message on a queue and delivers it if a consumer is waiting.
// Tests use it for a message the caller did not publish.
func (s *Server) Push(queue, messageID, body string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queues[queue]
	if q == nil {
		return false
	}
	q.ready = append(q.ready, qmsg{body: []byte(body), props: props{MessageID: messageID}})
	if ch, _ := s.pickConsumer(queue); ch != nil {
		if cn := s.connOf(ch); cn != nil {
			cn.deliver(q)
		}
	}
	return true
}

// Queues lists queue names.
func (s *Server) Queues() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.queues))
	for n := range s.queues {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Depth is the number of ready messages in the queue.
func (s *Server) Depth(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queues[name]
	if q == nil {
		return 0
	}
	return len(q.ready)
}

// Unacked is how many delivered messages of this queue are not yet acked.
func (s *Server) Unacked(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queues[name]
	if q == nil {
		return 0
	}
	return q.unacked
}

// PeakUnacked is the most unacked messages this queue has had at once.
func (s *Server) PeakUnacked(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queues[name]
	if q == nil {
		return 0
	}
	return q.peakUnacked
}

// Published is every basic.publish, in arrival order.
func (s *Server) Published() []Pub {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Pub, len(s.pubs))
	copy(out, s.pubs)
	return out
}

// Connections lists connections that finished connection.open.
func (s *Server) Connections() []ConnInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ConnInfo, len(s.opened))
	copy(out, s.opened)
	return out
}

// StallOn reads the named method and never answers it.
func (s *Server) StallOn(method string) {
	s.mu.Lock()
	s.stall = method
	s.mu.Unlock()
}

// DropConnectionOn closes the TCP connection when the named method arrives.
func (s *Server) DropConnectionOn(method string) {
	s.mu.Lock()
	s.drop = method
	s.mu.Unlock()
}

// CloseChannelOn answers the named method with channel.close.
func (s *Server) CloseChannelOn(method string, code uint16, text string) {
	s.mu.Lock()
	s.chClose, s.chCode, s.chText = method, code, text
	s.mu.Unlock()
}

// NackPublishes makes publisher confirms nack instead of ack.
func (s *Server) NackPublishes(on bool) {
	s.mu.Lock()
	s.nack = on
	s.mu.Unlock()
}

// Delay waits before every reply.
func (s *Server) Delay(d time.Duration) {
	s.mu.Lock()
	s.delay = d
	s.mu.Unlock()
}

// RefuseConnections closes each accepted socket before the handshake.
func (s *Server) RefuseConnections(on bool) {
	s.mu.Lock()
	s.refuse = on
	s.mu.Unlock()
}

// Block sends connection.blocked to every open connection.
func (s *Server) Block(reason string) {
	s.mu.Lock()
	list := make([]*conn, 0, len(s.conns))
	for _, c := range s.conns {
		list = append(list, c)
	}
	s.mu.Unlock()
	args, _ := appendShort(nil, reason)
	body := encodeMethod(10, 60, args)
	for _, c := range list {
		_ = c.write(wireFrame{Type: frameMethod, Body: body})
	}
}

// Unblock sends connection.unblocked to every open connection.
func (s *Server) Unblock() {
	s.mu.Lock()
	list := make([]*conn, 0, len(s.conns))
	for _, c := range s.conns {
		list = append(list, c)
	}
	s.mu.Unlock()
	body := encodeMethod(10, 61, nil)
	for _, c := range list {
		_ = c.write(wireFrame{Type: frameMethod, Body: body})
	}
}

func (s *Server) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.accepted++
		s.nextConn++
		id := s.nextConn
		s.open++
		refuse := s.refuse
		s.mu.Unlock()
		if refuse {
			_ = c.Close()
			s.mu.Lock()
			s.open--
			s.mu.Unlock()
			continue
		}
		cn := &conn{s: s, id: id, c: c, chs: map[uint16]*channel{}}
		s.mu.Lock()
		s.conns[id] = cn
		s.mu.Unlock()
		go cn.serve()
	}
}

func (s *Server) dropAll() {
	s.mu.Lock()
	list := make([]*conn, 0, len(s.conns))
	for _, c := range s.conns {
		list = append(list, c)
	}
	s.mu.Unlock()
	for _, c := range list {
		_ = c.c.Close()
	}
}

func (s *Server) log(id int, ch uint16, name string) {
	s.mu.Lock()
	s.methods = append(s.methods, fmt.Sprintf("%d/%d %s", id, ch, name))
	s.mu.Unlock()
}

func (s *Server) fault(name string) (stall, drop bool, code uint16, text string, delay time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stall == name, s.drop == name, func() uint16 {
		if s.chClose == name {
			return s.chCode
		}
		return 0
	}(), s.chText, s.delay
}

func (cn *conn) serve() {
	defer func() {
		cn.s.mu.Lock()
		delete(cn.s.conns, cn.id)
		cn.s.open--
		cn.s.dropExclusive(cn.id)
		cn.s.mu.Unlock()
		_ = cn.c.Close()
	}()
	var hdr [8]byte
	if _, err := io.ReadFull(cn.c, hdr[:]); err != nil {
		return
	}
	if string(hdr[:4]) != "AMQP" || hdr[4] != 0 || hdr[5] != 0 || hdr[6] != 9 || hdr[7] != 1 {
		_, _ = cn.c.Write([]byte{'A', 'M', 'Q', 'P', 0, 0, 9, 1})
		return
	}
	if err := cn.sendStart(); err != nil {
		return
	}
	for {
		cn.mu.Lock()
		hb := cn.heartbeat
		cn.mu.Unlock()
		if hb > 0 {
			_ = cn.c.SetReadDeadline(time.Now().Add(2 * time.Duration(hb) * time.Second))
		}
		f, err := readFrame(cn.c)
		if err != nil {
			return
		}
		if f.Type == frameHeartbeat {
			continue
		}
		if p := cn.partial[f.Channel]; p != nil {
			done, err := p.feed(f)
			if err != nil {
				_ = cn.connClose(codeUnexpected, err.Error())
				return
			}
			if !done {
				continue
			}
			delete(cn.partial, f.Channel)
			if p.delay > 0 {
				time.Sleep(p.delay)
			}
			if p.closeCode != 0 {
				_ = cn.channelClose(f.Channel, p.closeCode, p.closeText)
				cn.mu.Lock()
				if ch := cn.chs[f.Channel]; ch != nil {
					ch.dead = true
				}
				cn.mu.Unlock()
				continue
			}
			if p.drop {
				continue
			}
			if err := cn.finishPublish(f.Channel, p); err != nil {
				return
			}
			continue
		}
		if f.Type != frameMethod {
			_ = cn.connClose(codeUnexpected, "UNEXPECTED_FRAME - expected method frame")
			return
		}
		class, method, args, err := decodeMethod(f.Body)
		if err != nil {
			return
		}
		name := methodName(class, method)
		cn.s.log(cn.id, f.Channel, name)
		if name == "basic.publish" {
			p, err := startPublish(args)
			if err != nil {
				return
			}
			stall, drop, code, text, delay := cn.s.fault(name)
			if drop {
				return
			}
			p.drop = stall
			p.closeCode, p.closeText, p.delay = code, text, delay
			if cn.partial == nil {
				cn.partial = map[uint16]*pubIn{}
			}
			cn.partial[f.Channel] = p
			continue
		}
		stall, drop, code, text, delay := cn.s.fault(name)
		if drop {
			return
		}
		if code != 0 {
			if delay > 0 {
				time.Sleep(delay)
			}
			if f.Channel == 0 {
				_ = cn.connClose(code, text)
				return
			}
			_ = cn.channelClose(f.Channel, code, text)
			cn.mu.Lock()
			if ch := cn.chs[f.Channel]; ch != nil {
				ch.dead = true
			}
			cn.mu.Unlock()
			continue
		}
		if stall {
			continue
		}
		if delay > 0 {
			time.Sleep(delay)
		}
		if err := cn.dispatch(f.Channel, class, method, args); err != nil {
			return
		}
	}
}

func (cn *conn) dispatch(ch uint16, class, method uint16, args []byte) error {
	switch uint32(class)<<16 | uint32(method) {
	case 10<<16 | 11: // connection.start-ok
		return cn.onStartOk(args)
	case 10<<16 | 31: // connection.tune-ok
		return cn.onTuneOk(args)
	case 10<<16 | 40: // connection.open
		return cn.onOpen(args)
	case 10<<16 | 50: // connection.close
		_ = cn.writeMethod(0, 10, 51, nil)
		return io.EOF
	case 10<<16 | 51:
		return nil
	case 20<<16 | 10: // channel.open
		return cn.onChannelOpen(ch)
	case 20<<16 | 40: // channel.close
		cn.s.mu.Lock()
		cn.releaseChannel(ch)
		cn.s.mu.Unlock()
		return cn.writeMethod(ch, 20, 41, nil)
	case 20<<16 | 41:
		return nil
	case 40<<16 | 10:
		return cn.onExchangeDeclare(ch, args)
	case 40<<16 | 20:
		return cn.onExchangeDelete(ch, args)
	case 50<<16 | 10:
		return cn.onQueueDeclare(ch, args)
	case 50<<16 | 20:
		return cn.onQueueBind(ch, args)
	case 50<<16 | 30:
		return cn.onQueuePurge(ch, args)
	case 50<<16 | 40:
		return cn.onQueueDelete(ch, args)
	case 60<<16 | 10:
		return cn.onQos(ch, args)
	case 60<<16 | 20:
		return cn.onConsume(ch, args)
	case 60<<16 | 30:
		return cn.onCancel(ch, args)
	case 60<<16 | 40:
		return cn.connClose(codeUnexpected, "UNEXPECTED_FRAME - publish handled before dispatch")
	case 60<<16 | 80:
		return cn.onAck(ch, args, false)
	case 60<<16 | 90:
		return cn.onReject(ch, args)
	case 60<<16 | 120:
		return cn.onAck(ch, args, true)
	case 85<<16 | 10:
		return cn.onConfirm(ch, args)
	default:
		if ch == 0 {
			return cn.connClose(codeCommandInvalid, "COMMAND_INVALID - unknown method")
		}
		return cn.channelClose(ch, codeCommandInvalid, "COMMAND_INVALID - unknown method")
	}
}

func (cn *conn) sendStart() error {
	var args []byte
	var err error
	args, err = appendTable(args, map[string]any{
		"product": "VegaLoad",
		"version": "0-9-1",
		"capabilities": map[string]any{
			"publisher_confirms":         true,
			"exchange_exchange_bindings": true,
			"basic.nack":                 true,
			"consumer_cancel_notify":     true,
			"connection.blocked":         true,
		},
	})
	if err != nil {
		return err
	}
	args = appendLong(args, "PLAIN")
	args = appendLong(args, "en_US")
	body := append([]byte{0, 9}, args...)
	return cn.write(wireFrame{Type: frameMethod, Body: encodeMethod(10, 10, body)})
}

func (cn *conn) onStartOk(args []byte) error {
	table, n, err := readTable(args)
	if err != nil {
		return err
	}
	args = args[n:]
	mech, args, err := shortStr(args)
	if err != nil {
		return err
	}
	resp, _, err := longStr(args)
	if err != nil {
		return err
	}
	if mech != "PLAIN" {
		return cn.refuseLogin()
	}
	parts := strings.Split(resp, "\x00")
	if len(parts) != 3 {
		return cn.refuseLogin()
	}
	user, pass := parts[1], parts[2]
	cn.s.mu.Lock()
	want, ok := cn.s.users[user]
	cn.s.mu.Unlock()
	if !ok || want != pass {
		return cn.refuseLogin()
	}
	cn.mu.Lock()
	cn.user = user
	if name, ok := table["connection_name"].(string); ok {
		cn.name = name
	}
	cn.mu.Unlock()
	// Propose 0 so the client's heartbeat is the negotiated value. This
	// library uses the other side's number when one side says 0.
	args = putU16(nil, 2047)
	args = putU32(args, frameMax)
	args = putU16(args, 0)
	return cn.writeMethod(0, 10, 30, args)
}

func (cn *conn) refuseLogin() error {
	_ = cn.connClose(codeAccessRefused, "ACCESS_REFUSED - Login was refused using authentication mechanism PLAIN")
	return io.EOF
}

func (cn *conn) onTuneOk(args []byte) error {
	_, args, err := u16(args)
	if err != nil {
		return err
	}
	_, args, err = u32(args)
	if err != nil {
		return err
	}
	hb, _, err := u16(args)
	if err != nil {
		return err
	}
	cn.mu.Lock()
	cn.heartbeat = hb
	cn.mu.Unlock()
	if hb > 0 {
		go cn.beat(time.Duration(hb) * time.Second)
	}
	return nil
}

func (cn *conn) beat(interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for range tick.C {
		cn.mu.Lock()
		dead := cn.dead
		cn.mu.Unlock()
		if dead {
			return
		}
		if err := cn.write(wireFrame{Type: frameHeartbeat}); err != nil {
			return
		}
	}
}

func (cn *conn) onOpen(args []byte) error {
	vhost, _, err := shortStr(args)
	if err != nil {
		return err
	}
	cn.s.mu.Lock()
	ok := cn.s.vhosts[vhost]
	cn.s.mu.Unlock()
	if !ok {
		_ = cn.connClose(codeNotAllowed, "NOT_ALLOWED - vhost "+vhost+" not found")
		return io.EOF
	}
	cn.mu.Lock()
	cn.vhost = vhost
	info := ConnInfo{Heartbeat: cn.heartbeat, Name: cn.name, Vhost: vhost, User: cn.user}
	cn.mu.Unlock()
	cn.s.mu.Lock()
	cn.s.opened = append(cn.s.opened, info)
	cn.s.mu.Unlock()
	empty, _ := appendShort(nil, "")
	return cn.writeMethod(0, 10, 41, empty)
}

func (cn *conn) onChannelOpen(id uint16) error {
	cn.mu.Lock()
	cn.chs[id] = &channel{id: id, consumers: map[string]*consumer{}, unacked: map[uint64]*held{}}
	cn.mu.Unlock()
	return cn.writeMethod(id, 20, 11, appendLong(nil, ""))
}

func (cn *conn) onExchangeDeclare(id uint16, args []byte) error {
	var err error
	_, args, err = u16(args)
	if err != nil {
		return err
	}
	name, args, err := shortStr(args)
	if err != nil {
		return err
	}
	kind, args, err := shortStr(args)
	if err != nil {
		return err
	}
	if len(args) < 1 {
		return io.ErrUnexpectedEOF
	}
	bits := args[0]
	args = args[1:]
	table, _, err := readTable(args)
	if err != nil {
		return err
	}
	_ = table
	passive := bits&1 != 0
	if kind == "headers" {
		return cn.channelClose(id, codeNotImplemented, "NOT_IMPLEMENTED - headers exchange")
	}
	if strings.HasPrefix(name, "amq.") && !passive {
		return cn.channelClose(id, codeAccessRefused, "ACCESS_REFUSED - exchange name '"+name+"' contains reserved prefix 'amq.'")
	}
	cn.s.mu.Lock()
	defer cn.s.mu.Unlock()
	ex := cn.s.ex[name]
	if passive {
		if ex == nil {
			return cn.channelClose(id, codeNotFound, "NOT_FOUND - no exchange '"+name+"' in vhost '"+cn.vhost+"'")
		}
		return cn.writeMethod(id, 40, 11, nil)
	}
	if kind != "direct" && kind != "fanout" && kind != "topic" {
		return cn.channelClose(id, codeCommandInvalid, "COMMAND_INVALID - unknown exchange type '"+kind+"'")
	}
	if ex != nil && (ex.kind != kind || ex.durable != (bits&2 != 0)) {
		return cn.channelClose(id, codePrecondition, "PRECONDITION_FAILED - inequivalent arg 'type' for exchange '"+name+"' in vhost '"+cn.vhost+"'")
	}
	if ex == nil {
		cn.s.ex[name] = &exchange{name: name, kind: kind, durable: bits&2 != 0, autoDel: bits&4 != 0}
	}
	return cn.writeMethod(id, 40, 11, nil)
}

func (cn *conn) onExchangeDelete(id uint16, args []byte) error {
	_, args, err := u16(args)
	if err != nil {
		return err
	}
	name, _, err := shortStr(args)
	if err != nil {
		return err
	}
	cn.s.mu.Lock()
	defer cn.s.mu.Unlock()
	ex := cn.s.ex[name]
	if ex == nil {
		return cn.channelClose(id, codeNotFound, "NOT_FOUND - no exchange '"+name+"' in vhost '"+cn.vhost+"'")
	}
	if ex.built {
		return cn.channelClose(id, codeAccessRefused, "ACCESS_REFUSED - exchange '"+name+"' in vhost '"+cn.vhost+"' cannot be deleted")
	}
	delete(cn.s.ex, name)
	var keep []binding
	for _, b := range cn.s.binds {
		if b.exchange != name {
			keep = append(keep, b)
		}
	}
	cn.s.binds = keep
	return cn.writeMethod(id, 40, 21, nil)
}

func (cn *conn) onQueueDeclare(id uint16, args []byte) error {
	_, args, err := u16(args)
	if err != nil {
		return err
	}
	name, args, err := shortStr(args)
	if err != nil {
		return err
	}
	if len(args) < 1 {
		return io.ErrUnexpectedEOF
	}
	bits := args[0]
	table, _, err := readTable(args[1:])
	if err != nil {
		return err
	}
	passive := bits&1 != 0
	durable := bits&2 != 0
	exclusive := bits&4 != 0
	autoDel := bits&8 != 0
	cn.s.mu.Lock()
	defer cn.s.mu.Unlock()
	if name == "" && !passive {
		name = "amq.gen-" + randHex(8)
	}
	q := cn.s.queues[name]
	if passive {
		if q == nil {
			return cn.channelClose(id, codeNotFound, "NOT_FOUND - no queue '"+name+"' in vhost '"+cn.vhost+"'")
		}
		return cn.declareOK(id, q)
	}
	if q != nil {
		if q.exclusive && q.owner != cn.id {
			return cn.channelClose(id, codeLocked, "RESOURCE_LOCKED - cannot obtain exclusive access to locked queue '"+name+"' in vhost '"+cn.vhost+"'")
		}
		if q.durable != durable {
			return cn.channelClose(id, codePrecondition, "PRECONDITION_FAILED - inequivalent arg 'durable' for queue '"+name+"' in vhost '"+cn.vhost+"'")
		}
		if qt, ok := table["x-queue-type"]; ok {
			if prev, ok := q.args["x-queue-type"]; ok && prev != qt {
				return cn.channelClose(id, codePrecondition, "PRECONDITION_FAILED - inequivalent arg 'x-queue-type' for queue '"+name+"' in vhost '"+cn.vhost+"'")
			}
		}
		return cn.declareOK(id, q)
	}
	q = &queue{name: name, durable: durable, exclusive: exclusive, autoDel: autoDel, args: table}
	if exclusive {
		q.owner = cn.id
	}
	cn.s.queues[name] = q
	return cn.declareOK(id, q)
}

func (cn *conn) declareOK(id uint16, q *queue) error {
	args, err := appendShort(nil, q.name)
	if err != nil {
		return err
	}
	args = putU32(args, uint32(len(q.ready)))
	args = putU32(args, uint32(q.consumersNow(cn.s)))
	return cn.writeMethod(id, 50, 11, args)
}

func (cn *conn) onQueueBind(id uint16, args []byte) error {
	_, args, err := u16(args)
	if err != nil {
		return err
	}
	qname, args, err := shortStr(args)
	if err != nil {
		return err
	}
	exch, args, err := shortStr(args)
	if err != nil {
		return err
	}
	key, args, err := shortStr(args)
	if err != nil {
		return err
	}
	cn.s.mu.Lock()
	defer cn.s.mu.Unlock()
	if cn.s.queues[qname] == nil {
		return cn.channelClose(id, codeNotFound, "NOT_FOUND - no queue '"+qname+"' in vhost '"+cn.vhost+"'")
	}
	if cn.s.ex[exch] == nil {
		return cn.channelClose(id, codeNotFound, "NOT_FOUND - no exchange '"+exch+"' in vhost '"+cn.vhost+"'")
	}
	cn.s.binds = append(cn.s.binds, binding{queue: qname, exchange: exch, key: key})
	_ = args
	return cn.writeMethod(id, 50, 21, nil)
}

func (cn *conn) onQueuePurge(id uint16, args []byte) error {
	_, args, err := u16(args)
	if err != nil {
		return err
	}
	name, _, err := shortStr(args)
	if err != nil {
		return err
	}
	cn.s.mu.Lock()
	defer cn.s.mu.Unlock()
	q := cn.s.queues[name]
	if q == nil {
		return cn.channelClose(id, codeNotFound, "NOT_FOUND - no queue '"+name+"' in vhost '"+cn.vhost+"'")
	}
	n := uint32(len(q.ready))
	q.ready = nil
	return cn.writeMethod(id, 50, 31, putU32(nil, n))
}

func (cn *conn) onQueueDelete(id uint16, args []byte) error {
	_, args, err := u16(args)
	if err != nil {
		return err
	}
	name, _, err := shortStr(args)
	if err != nil {
		return err
	}
	cn.s.mu.Lock()
	defer cn.s.mu.Unlock()
	q := cn.s.queues[name]
	if q == nil {
		return cn.channelClose(id, codeNotFound, "NOT_FOUND - no queue '"+name+"' in vhost '"+cn.vhost+"'")
	}
	n := uint32(len(q.ready) + q.unacked)
	cn.s.deleteQueue(name)
	return cn.writeMethod(id, 50, 41, putU32(nil, n))
}

func (cn *conn) onQos(id uint16, args []byte) error {
	_, args, err := u32(args)
	if err != nil {
		return err
	}
	count, args, err := u16(args)
	if err != nil {
		return err
	}
	global := len(args) > 0 && args[0]&1 != 0
	cn.mu.Lock()
	ch := cn.chs[id]
	if ch != nil {
		ch.prefetch = count
		ch.global = global
	}
	cn.mu.Unlock()
	return cn.writeMethod(id, 60, 11, nil)
}

func (cn *conn) onConsume(id uint16, args []byte) error {
	_, args, err := u16(args)
	if err != nil {
		return err
	}
	qname, args, err := shortStr(args)
	if err != nil {
		return err
	}
	tag, args, err := shortStr(args)
	if err != nil {
		return err
	}
	noAck := len(args) > 0 && args[0]&2 != 0
	cn.s.mu.Lock()
	defer cn.s.mu.Unlock()
	q := cn.s.queues[qname]
	if q == nil {
		return cn.channelClose(id, codeNotFound, "NOT_FOUND - no queue '"+qname+"' in vhost '"+cn.vhost+"'")
	}
	if q.exclusive && q.owner != 0 && q.owner != cn.id {
		return cn.channelClose(id, codeLocked, "RESOURCE_LOCKED - cannot obtain exclusive access to locked queue '"+qname+"' in vhost '"+cn.vhost+"'")
	}
	cn.mu.Lock()
	ch := cn.chs[id]
	if ch == nil || ch.dead {
		cn.mu.Unlock()
		return cn.channelClose(id, codeCommandInvalid, "COMMAND_INVALID - unknown channel")
	}
	ch.consumers[tag] = &consumer{tag: tag, queue: qname, noAck: noAck}
	cn.mu.Unlock()
	q.hadConsumer = true
	ok, _ := appendShort(nil, tag)
	if err := cn.writeMethod(id, 60, 21, ok); err != nil {
		return err
	}
	cn.deliver(q)
	return nil
}

func (cn *conn) onCancel(id uint16, args []byte) error {
	tag, _, err := shortStr(args)
	if err != nil {
		return err
	}
	cn.s.mu.Lock()
	defer cn.s.mu.Unlock()
	cn.mu.Lock()
	ch := cn.chs[id]
	var qname string
	if ch != nil {
		if c := ch.consumers[tag]; c != nil {
			qname = c.queue
		}
		delete(ch.consumers, tag)
	}
	cn.mu.Unlock()
	if qname != "" {
		cn.s.maybeAutoDelete(qname)
	}
	out, _ := appendShort(nil, tag)
	return cn.writeMethod(id, 60, 31, out)
}

func startPublish(args []byte) (*pubIn, error) {
	_, args, err := u16(args)
	if err != nil {
		return nil, err
	}
	exch, args, err := shortStr(args)
	if err != nil {
		return nil, err
	}
	key, args, err := shortStr(args)
	if err != nil {
		return nil, err
	}
	return &pubIn{exch: exch, key: key, mandatory: len(args) > 0 && args[0]&1 != 0}, nil
}

func (p *pubIn) feed(f wireFrame) (bool, error) {
	if !p.gotHeader {
		if f.Type != frameHeader {
			return false, fmt.Errorf("UNEXPECTED_FRAME - expected content header")
		}
		_, size, props, err := decodeHeader(f.Body)
		if err != nil {
			return false, err
		}
		p.gotHeader = true
		p.size = size
		p.props = props
		if size == 0 {
			return true, nil
		}
		return false, nil
	}
	if f.Type != frameBody {
		return false, fmt.Errorf("UNEXPECTED_FRAME - expected content body")
	}
	p.body = append(p.body, f.Body...)
	return uint64(len(p.body)) >= p.size, nil
}

func (cn *conn) finishPublish(id uint16, in *pubIn) error {
	exch, key, mandatory := in.exch, in.key, in.mandatory
	body := in.body
	p := in.props
	cn.s.mu.Lock()
	defer cn.s.mu.Unlock()
	cn.s.pubs = append(cn.s.pubs, Pub{
		Exchange: exch, RoutingKey: key, Body: append([]byte(nil), body...),
		MessageID: p.MessageID, ContentType: p.ContentType, Expiration: p.Expiration,
		DeliveryMode: p.DeliveryMode, Priority: p.Priority, Timestamp: p.Timestamp,
		Headers: p.Headers, Mandatory: mandatory,
	})
	ex := cn.s.ex[exch]
	if ex == nil {
		return cn.channelClose(id, codeNotFound, "NOT_FOUND - no exchange '"+exch+"' in vhost '"+cn.vhost+"'")
	}
	targets := cn.s.route(ex, key)
	msg := qmsg{exchange: exch, key: key, body: append([]byte(nil), body...), props: p}
	for _, name := range targets {
		q := cn.s.queues[name]
		if q == nil {
			continue
		}
		q.ready = append(q.ready, msg)
	}
	cn.mu.Lock()
	ch := cn.chs[id]
	confirm := ch != nil && ch.confirm
	nack := cn.s.nack
	var tag uint64
	if confirm && ch != nil {
		ch.pubTag++
		tag = ch.pubTag
	}
	cn.mu.Unlock()
	if len(targets) == 0 && mandatory {
		ret, err := appendShort(putU16(nil, codeNoRoute), "NO_ROUTE")
		if err != nil {
			return err
		}
		ret, err = appendShort(ret, exch)
		if err != nil {
			return err
		}
		ret, err = appendShort(ret, key)
		if err != nil {
			return err
		}
		if err := cn.write(wireFrame{Type: frameMethod, Channel: id, Body: encodeMethod(60, 50, ret)}); err != nil {
			return err
		}
		hb, err := encodeHeader(60, uint64(len(body)), p)
		if err != nil {
			return err
		}
		if err := cn.write(wireFrame{Type: frameHeader, Channel: id, Body: hb}); err != nil {
			return err
		}
		for _, part := range splitBody(body) {
			if err := cn.write(wireFrame{Type: frameBody, Channel: id, Body: part}); err != nil {
				return err
			}
		}
	}
	if confirm && cn.s.stall != "basic.ack" {
		if nack {
			if err := cn.writeMethod(id, 60, 120, append(putU64(nil, tag), 0)); err != nil {
				return err
			}
		} else {
			if err := cn.writeMethod(id, 60, 80, append(putU64(nil, tag), 0)); err != nil {
				return err
			}
		}
	}
	if !nack {
		for _, name := range targets {
			if q := cn.s.queues[name]; q != nil {
				cn.deliver(q)
			}
		}
	} else if len(targets) > 0 {
		for _, name := range targets {
			if q := cn.s.queues[name]; q != nil && len(q.ready) > 0 {
				q.ready = q.ready[:len(q.ready)-1]
			}
		}
	}
	return nil
}

func (s *Server) route(ex *exchange, key string) []string {
	if ex.name == "" {
		if s.queues[key] != nil {
			return []string{key}
		}
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, b := range s.binds {
		if b.exchange != ex.name {
			continue
		}
		ok := false
		switch ex.kind {
		case "fanout":
			ok = true
		case "topic":
			ok = matchTopic(b.key, key)
		default:
			ok = b.key == key
		}
		if ok && !seen[b.queue] {
			seen[b.queue] = true
			out = append(out, b.queue)
		}
	}
	return out
}

func (cn *conn) deliver(q *queue) {
	type item struct {
		ch   *channel
		cons *consumer
		msg  qmsg
		tag  uint64
	}
	var items []item
	for len(q.ready) > 0 {
		var picked *channel
		var cons *consumer
		cn.mu.Lock()
		for _, ch := range cn.chs {
			if ch.dead {
				continue
			}
			for _, c := range ch.consumers {
				if c.queue != q.name {
					continue
				}
				if ch.prefetch > 0 && cn.unackedCount(ch, c.tag) >= int(ch.prefetch) {
					continue
				}
				picked = ch
				cons = c
				break
			}
			if picked != nil {
				break
			}
		}
		cn.mu.Unlock()
		if picked == nil {
			picked, cons = cn.s.pickConsumer(q.name)
		}
		if picked == nil || cons == nil {
			break
		}
		msg := q.ready[0]
		q.ready = q.ready[1:]
		chConn := cn.s.connOf(picked)
		if chConn == nil {
			q.ready = append([]qmsg{msg}, q.ready...)
			break
		}
		chConn.mu.Lock()
		picked.delTag++
		tag := picked.delTag
		if !cons.noAck {
			picked.unacked[tag] = &held{queue: q.name, consumer: cons.tag, msg: msg}
			q.unacked++
			if q.unacked > q.peakUnacked {
				q.peakUnacked = q.unacked
			}
		}
		chConn.mu.Unlock()
		items = append(items, item{ch: picked, cons: cons, msg: msg, tag: tag})
	}
	for _, it := range items {
		c := cn.s.connOf(it.ch)
		if c == nil {
			continue
		}
		_ = c.sendDeliver(it.ch.id, it.cons.tag, it.tag, it.msg)
	}
}

func (s *Server) connOf(ch *channel) *conn {
	for _, c := range s.conns {
		c.mu.Lock()
		_, ok := c.chs[ch.id]
		// identity: the channel pointer
		if ok && c.chs[ch.id] == ch {
			c.mu.Unlock()
			return c
		}
		c.mu.Unlock()
	}
	return nil
}

func (s *Server) pickConsumer(qname string) (*channel, *consumer) {
	for _, c := range s.conns {
		c.mu.Lock()
		for _, ch := range c.chs {
			if ch.dead {
				continue
			}
			for _, cons := range ch.consumers {
				if cons.queue != qname {
					continue
				}
				if ch.prefetch > 0 && c.unackedCount(ch, cons.tag) >= int(ch.prefetch) {
					continue
				}
				c.mu.Unlock()
				return ch, cons
			}
		}
		c.mu.Unlock()
	}
	return nil, nil
}

func (cn *conn) unackedCount(ch *channel, tag string) int {
	if ch.global {
		return len(ch.unacked)
	}
	n := 0
	for _, h := range ch.unacked {
		if h.consumer == tag {
			n++
		}
	}
	return n
}

func (cn *conn) sendDeliver(ch uint16, tag string, dtag uint64, msg qmsg) error {
	args, err := appendShort(nil, tag)
	if err != nil {
		return err
	}
	args = putU64(args, dtag)
	red := byte(0)
	if msg.redelivered {
		red = 1
	}
	args = append(args, red)
	args, err = appendShort(args, msg.exchange)
	if err != nil {
		return err
	}
	args, err = appendShort(args, msg.key)
	if err != nil {
		return err
	}
	if err := cn.write(wireFrame{Type: frameMethod, Channel: ch, Body: encodeMethod(60, 60, args)}); err != nil {
		return err
	}
	hb, err := encodeHeader(60, uint64(len(msg.body)), msg.props)
	if err != nil {
		return err
	}
	if err := cn.write(wireFrame{Type: frameHeader, Channel: ch, Body: hb}); err != nil {
		return err
	}
	for _, part := range splitBody(msg.body) {
		if err := cn.write(wireFrame{Type: frameBody, Channel: ch, Body: part}); err != nil {
			return err
		}
	}
	return nil
}

func (cn *conn) onAck(id uint16, args []byte, nack bool) error {
	tag, args, err := u64(args)
	if err != nil {
		return err
	}
	multiple := len(args) > 0 && args[0]&1 != 0
	requeue := nack && len(args) > 0 && args[0]&2 != 0
	cn.s.mu.Lock()
	defer cn.s.mu.Unlock()
	cn.mu.Lock()
	ch := cn.chs[id]
	if ch == nil {
		cn.mu.Unlock()
		return nil
	}
	var tags []uint64
	for t := range ch.unacked {
		if multiple && t <= tag || !multiple && t == tag {
			tags = append(tags, t)
		}
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i] < tags[j] })
	var back []qmsg
	var qname string
	for _, t := range tags {
		h := ch.unacked[t]
		delete(ch.unacked, t)
		if q := cn.s.queues[h.queue]; q != nil && q.unacked > 0 {
			q.unacked--
		}
		qname = h.queue
		if nack && requeue {
			h.msg.redelivered = true
			back = append(back, h.msg)
		}
	}
	cn.mu.Unlock()
	if q := cn.s.queues[qname]; q != nil && len(back) > 0 {
		q.ready = append(back, q.ready...)
		cn.deliver(q)
	}
	return nil
}

func (cn *conn) onReject(id uint16, args []byte) error {
	tag, args, err := u64(args)
	if err != nil {
		return err
	}
	requeue := len(args) > 0 && args[0]&1 != 0
	bits := byte(0)
	if requeue {
		bits = 2
	}
	return cn.onAck(id, append(putU64(nil, tag), bits), true)
}

func (cn *conn) onConfirm(id uint16, args []byte) error {
	cn.mu.Lock()
	if ch := cn.chs[id]; ch != nil {
		ch.confirm = true
	}
	cn.mu.Unlock()
	_ = args
	return cn.writeMethod(id, 85, 11, nil)
}

func (cn *conn) releaseChannel(id uint16) {
	cn.mu.Lock()
	ch := cn.chs[id]
	delete(cn.chs, id)
	cn.mu.Unlock()
	if ch == nil {
		return
	}
	var backByQueue = map[string][]qmsg{}
	for _, h := range ch.unacked {
		h.msg.redelivered = true
		backByQueue[h.queue] = append(backByQueue[h.queue], h.msg)
		if q := cn.s.queues[h.queue]; q != nil && q.unacked > 0 {
			q.unacked--
		}
	}
	queues := map[string]bool{}
	for _, c := range ch.consumers {
		queues[c.queue] = true
	}
	for name, msgs := range backByQueue {
		if q := cn.s.queues[name]; q != nil {
			q.ready = append(msgs, q.ready...)
		}
	}
	for name := range queues {
		cn.s.maybeAutoDelete(name)
	}
	for name := range backByQueue {
		if q := cn.s.queues[name]; q != nil {
			cn.deliver(q)
		}
	}
}

func (s *Server) maybeAutoDelete(name string) {
	q := s.queues[name]
	if q == nil || !q.autoDel || !q.hadConsumer {
		return
	}
	for _, c := range s.conns {
		c.mu.Lock()
		for _, ch := range c.chs {
			for _, cons := range ch.consumers {
				if cons.queue == name {
					c.mu.Unlock()
					return
				}
			}
		}
		c.mu.Unlock()
	}
	s.deleteQueue(name)
}

func (s *Server) deleteQueue(name string) {
	delete(s.queues, name)
	var keep []binding
	for _, b := range s.binds {
		if b.queue != name {
			keep = append(keep, b)
		}
	}
	s.binds = keep
}

func (s *Server) dropExclusive(id int) {
	var names []string
	for n, q := range s.queues {
		if q.exclusive && q.owner == id {
			names = append(names, n)
		}
	}
	for _, n := range names {
		s.deleteQueue(n)
	}
}

func (cn *conn) writeMethod(ch, class, method uint16, args []byte) error {
	return cn.write(wireFrame{Type: frameMethod, Channel: uint16(ch), Body: encodeMethod(uint16(class), uint16(method), args)})
}

func (cn *conn) write(f wireFrame) error {
	cn.wmu.Lock()
	defer cn.wmu.Unlock()
	return writeFrame(cn.c, f)
}

func (cn *conn) channelClose(ch uint16, code uint16, text string) error {
	args := putU16(nil, code)
	var err error
	args, err = appendShort(args, text)
	if err != nil {
		return err
	}
	args = putU16(args, 0)
	args = putU16(args, 0)
	cn.mu.Lock()
	if c := cn.chs[ch]; c != nil {
		c.dead = true
	}
	cn.mu.Unlock()
	return cn.writeMethod(ch, 20, 40, args)
}

func (cn *conn) connClose(code uint16, text string) error {
	args := putU16(nil, code)
	var err error
	args, err = appendShort(args, text)
	if err != nil {
		return err
	}
	args = putU16(args, 0)
	args = putU16(args, 0)
	cn.mu.Lock()
	cn.dead = true
	cn.mu.Unlock()
	if err := cn.writeMethod(0, 10, 50, args); err != nil {
		return err
	}
	_ = cn.c.Close()
	return io.EOF
}

func methodName(class, method uint16) string {
	switch uint32(class)<<16 | uint32(method) {
	case 10<<16 | 11:
		return "connection.start-ok"
	case 10<<16 | 31:
		return "connection.tune-ok"
	case 10<<16 | 40:
		return "connection.open"
	case 10<<16 | 50:
		return "connection.close"
	case 10<<16 | 51:
		return "connection.close-ok"
	case 20<<16 | 10:
		return "channel.open"
	case 20<<16 | 40:
		return "channel.close"
	case 20<<16 | 41:
		return "channel.close-ok"
	case 40<<16 | 10:
		return "exchange.declare"
	case 40<<16 | 20:
		return "exchange.delete"
	case 50<<16 | 10:
		return "queue.declare"
	case 50<<16 | 20:
		return "queue.bind"
	case 50<<16 | 30:
		return "queue.purge"
	case 50<<16 | 40:
		return "queue.delete"
	case 60<<16 | 10:
		return "basic.qos"
	case 60<<16 | 20:
		return "basic.consume"
	case 60<<16 | 30:
		return "basic.cancel"
	case 60<<16 | 40:
		return "basic.publish"
	case 60<<16 | 80:
		return "basic.ack"
	case 60<<16 | 90:
		return "basic.reject"
	case 60<<16 | 120:
		return "basic.nack"
	case 85<<16 | 10:
		return "confirm.select"
	default:
		return fmt.Sprintf("class-%d.%d", class, method)
	}
}

func matchTopic(pattern, key string) bool {
	return matchParts(strings.Split(pattern, "."), strings.Split(key, "."))
}

func matchParts(p, k []string) bool {
	for len(p) > 0 {
		if p[0] == "#" {
			if len(p) == 1 {
				return true
			}
			for i := 0; i <= len(k); i++ {
				if matchParts(p[1:], k[i:]) {
					return true
				}
			}
			return false
		}
		if len(k) == 0 {
			return false
		}
		if p[0] != "*" && p[0] != k[0] {
			return false
		}
		p, k = p[1:], k[1:]
	}
	return len(k) == 0
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func selfSigned() (tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "vegaload-test"},
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

// consumerCount is the number of live consumers. It is computed under the
// server lock by walking connections, so the method on queue stays a stub
// the declare reply replaces.
func (q *queue) consumersNow(s *Server) int {
	n := 0
	for _, c := range s.conns {
		c.mu.Lock()
		for _, ch := range c.chs {
			for _, cons := range ch.consumers {
				if cons.queue == q.name {
					n++
				}
			}
		}
		c.mu.Unlock()
	}
	return n
}
