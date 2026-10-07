package mqtt

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
)

// fakeBroker is a small MQTT 3.1.1 broker for tests. It handles CONNECT,
// PUBLISH at QoS 0, 1 and 2, SUBSCRIBE, PINGREQ and DISCONNECT. It routes
// a published message to every matching subscriber and keeps retained
// messages. It is not a real broker: it is just enough to test the driver
// without Docker.
type fakeBroker struct {
	t  *testing.T
	ln net.Listener

	// Optional login check. Empty user means no check.
	user, pass string

	// Optional. When set, subscribers get this payload instead of the real
	// one. It stands for another user's message on the same topic.
	decoy []byte

	// Optional. When set, this is added to the end of every payload. With
	// ids like x-1 and x-10, the message x-10 starts with x-1.
	suffix []byte

	mu        sync.Mutex
	clientIDs []string
	published []pubRecord
	subs      []*subscription
	retained  map[string][]byte
	conns     map[net.Conn]bool
}

type pubRecord struct {
	topic   string
	payload []byte
	qos     byte
	retain  bool
}

type subscription struct {
	conn   *brokerConn
	filter string
}

type brokerConn struct {
	c  net.Conn
	mu sync.Mutex
}

func (b *brokerConn) write(p []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, _ = b.c.Write(p)
}

func newFakeBroker(t *testing.T, ln net.Listener) *fakeBroker {
	t.Helper()
	b := &fakeBroker{t: t, ln: ln, retained: map[string][]byte{}, conns: map[net.Conn]bool{}}
	go b.accept()
	t.Cleanup(func() {
		ln.Close()
		b.mu.Lock()
		for c := range b.conns {
			c.Close()
		}
		b.mu.Unlock()
	})
	return b
}

func startBroker(t *testing.T) *fakeBroker {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return newFakeBroker(t, ln)
}

func (b *fakeBroker) url() string { return "mqtt://" + b.ln.Addr().String() }

func (b *fakeBroker) accept() {
	for {
		c, err := b.ln.Accept()
		if err != nil {
			return
		}
		b.mu.Lock()
		b.conns[c] = true
		b.mu.Unlock()
		go b.serve(&brokerConn{c: c})
	}
}

func readVarInt(r *bufio.Reader) (int, error) {
	n, mul := 0, 1
	for i := 0; i < 4; i++ {
		x, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		n += int(x&0x7f) * mul
		if x&0x80 == 0 {
			return n, nil
		}
		mul *= 128
	}
	return 0, io.ErrUnexpectedEOF
}

func varInt(n int) []byte {
	var out []byte
	for {
		x := byte(n % 128)
		n /= 128
		if n > 0 {
			x |= 0x80
		}
		out = append(out, x)
		if n == 0 {
			return out
		}
	}
}

func str(p []byte) (string, []byte) {
	l := int(binary.BigEndian.Uint16(p))
	return string(p[2 : 2+l]), p[2+l:]
}

func (b *fakeBroker) serve(bc *brokerConn) {
	defer bc.c.Close()
	defer func() {
		b.mu.Lock()
		keep := b.subs[:0]
		for _, s := range b.subs {
			if s.conn != bc {
				keep = append(keep, s)
			}
		}
		b.subs = keep
		b.mu.Unlock()
	}()
	r := bufio.NewReader(bc.c)
	for {
		hdr, err := r.ReadByte()
		if err != nil {
			return
		}
		n, err := readVarInt(r)
		if err != nil {
			return
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(r, body); err != nil {
			return
		}
		switch hdr >> 4 {
		case 1: // CONNECT
			_, rest := str(body) // protocol name
			level, flags := rest[0], rest[1]
			_ = level
			rest = rest[4:] // level, flags, keepalive
			id, rest := str(rest)
			if flags&0x04 != 0 { // will
				_, rest = str(rest)
				_, rest = str(rest)
			}
			var user, pass string
			if flags&0x80 != 0 {
				user, rest = str(rest)
			}
			if flags&0x40 != 0 {
				pass, _ = str(rest)
			}
			b.mu.Lock()
			b.clientIDs = append(b.clientIDs, id)
			b.mu.Unlock()
			rc := byte(0)
			if b.user != "" && (user != b.user || pass != b.pass) {
				rc = 4 // bad user name or password
			}
			bc.write([]byte{0x20, 2, 0, rc})
			if rc != 0 {
				return
			}
		case 3: // PUBLISH
			qos := byte(hdr>>1) & 3
			retain := hdr&1 == 1
			topic, rest := str(body)
			var pid []byte
			if qos > 0 {
				pid, rest = rest[:2], rest[2:]
			}
			payload := append([]byte(nil), rest...)
			switch qos {
			case 1:
				bc.write(append([]byte{0x40, 2}, pid...))
			case 2:
				bc.write(append([]byte{0x50, 2}, pid...))
			}
			b.route(topic, payload, qos, retain)
		case 6: // PUBREL
			bc.write(append([]byte{0x70, 2}, body[:2]...))
		case 8: // SUBSCRIBE
			pid := body[:2]
			rest := body[2:]
			var granted []byte
			var filters []string
			for len(rest) > 0 {
				var f string
				f, rest = str(rest)
				granted = append(granted, rest[0])
				rest = rest[1:]
				filters = append(filters, f)
			}
			ack := append([]byte{0x90}, varInt(2+len(granted))...)
			ack = append(ack, pid...)
			ack = append(ack, granted...)
			bc.write(ack)
			for _, f := range filters {
				b.mu.Lock()
				b.subs = append(b.subs, &subscription{conn: bc, filter: f})
				var rets [][2][]byte
				for t, p := range b.retained {
					if topicMatch(f, t) {
						rets = append(rets, [2][]byte{[]byte(t), p})
					}
				}
				b.mu.Unlock()
				for _, r := range rets {
					bc.write(publishPacket(string(r[0]), r[1], true))
				}
			}
		case 12: // PINGREQ
			bc.write([]byte{0xd0, 0})
		case 14: // DISCONNECT
			return
		}
	}
}

func publishPacket(topic string, payload []byte, retain bool) []byte {
	hdr := byte(0x30)
	if retain {
		hdr |= 1
	}
	body := append([]byte{byte(len(topic) >> 8), byte(len(topic))}, topic...)
	body = append(body, payload...)
	return append(append([]byte{hdr}, varInt(len(body))...), body...)
}

func (b *fakeBroker) route(topic string, payload []byte, qos byte, retain bool) {
	b.mu.Lock()
	b.published = append(b.published, pubRecord{topic, payload, qos, retain})
	if retain {
		b.retained[topic] = payload
	}
	var targets []*brokerConn
	for _, s := range b.subs {
		if topicMatch(s.filter, topic) {
			targets = append(targets, s.conn)
		}
	}
	b.mu.Unlock()
	if b.decoy != nil {
		payload = b.decoy
	}
	if b.suffix != nil {
		payload = append(append([]byte(nil), payload...), b.suffix...)
	}
	for _, c := range targets {
		c.write(publishPacket(topic, payload, false))
	}
}

// topicMatch reports whether an MQTT topic filter matches a topic.
func topicMatch(filter, topic string) bool {
	f := strings.Split(filter, "/")
	t := strings.Split(topic, "/")
	for i, part := range f {
		if part == "#" {
			return true
		}
		if i >= len(t) {
			return false
		}
		if part != "+" && part != t[i] {
			return false
		}
	}
	return len(f) == len(t)
}

func (b *fakeBroker) ids() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.clientIDs...)
}

func (b *fakeBroker) pubs() []pubRecord {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]pubRecord(nil), b.published...)
}

func (b *fakeBroker) setRetained(topic string, payload []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.retained[topic] = payload
}

func TestTopicMatch(t *testing.T) {
	cases := []struct {
		filter, topic string
		want          bool
	}{
		{"a/b", "a/b", true},
		{"a/b", "a/c", false},
		{"a/+", "a/x", true},
		{"a/+", "a/x/y", false},
		{"a/#", "a/x/y", true},
		{"#", "anything/at/all", true},
		{"a/b/c", "a/b", false},
	}
	for _, c := range cases {
		if got := topicMatch(c.filter, c.topic); got != c.want {
			t.Errorf("topicMatch(%q, %q) = %v, want %v", c.filter, c.topic, got, c.want)
		}
	}
}
