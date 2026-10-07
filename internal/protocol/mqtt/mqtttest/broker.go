// Package mqtttest is a very small MQTT 3.1.1 broker for tests in other
// packages, so scripts can be tested without a real broker. It is only
// imported by tests, so it is not part of the vegaload binary.
package mqtttest

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
)

// Broker is just enough MQTT 3.1.1 for these tests: CONNECT, SUBSCRIBE
// and PUBLISH (QoS 0 and 1) with exact topic matching, and optional login.
type Broker struct {
	ln net.Listener
	// User and Pass, when User is not empty, are the login the broker asks for.
	User, Pass string
	mu         sync.Mutex
	subs       map[net.Conn][]string
	wmu        sync.Mutex
	pubs       []string // "topic=payload"
}

func Start(t *testing.T) *Broker {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := &Broker{ln: ln, subs: map[net.Conn][]string{}}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go b.serve(c)
		}
	}()
	return b
}

// URL is the broker's address as an mqtt:// target.
func (b *Broker) URL() string { return "mqtt://" + b.ln.Addr().String() }

func mstr(p []byte) (string, []byte) {
	l := int(binary.BigEndian.Uint16(p))
	return string(p[2 : 2+l]), p[2+l:]
}

func mvar(n int) []byte {
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

func (b *Broker) send(c net.Conn, p []byte) {
	b.wmu.Lock()
	defer b.wmu.Unlock()
	_, _ = c.Write(p)
}

func (b *Broker) serve(c net.Conn) {
	defer c.Close()
	defer func() {
		b.mu.Lock()
		delete(b.subs, c)
		b.mu.Unlock()
	}()
	r := bufio.NewReader(c)
	for {
		hdr, err := r.ReadByte()
		if err != nil {
			return
		}
		n, mul := 0, 1
		for {
			x, err := r.ReadByte()
			if err != nil {
				return
			}
			n += int(x&0x7f) * mul
			if x&0x80 == 0 {
				break
			}
			mul *= 128
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(r, body); err != nil {
			return
		}
		switch hdr >> 4 {
		case 1:
			_, rest := mstr(body)
			flags := rest[1]
			rest = rest[4:]
			_, rest = mstr(rest)
			var user, pass string
			if flags&0x80 != 0 {
				user, rest = mstr(rest)
			}
			if flags&0x40 != 0 {
				pass, _ = mstr(rest)
			}
			rc := byte(0)
			if b.User != "" && (user != b.User || pass != b.Pass) {
				rc = 4
			}
			b.send(c, []byte{0x20, 2, 0, rc})
			if rc != 0 {
				return
			}
		case 8:
			pid, rest := body[:2], body[2:]
			var granted []byte
			for len(rest) > 0 {
				var f string
				f, rest = mstr(rest)
				rest = rest[1:]
				granted = append(granted, 0)
				b.mu.Lock()
				b.subs[c] = append(b.subs[c], f)
				b.mu.Unlock()
			}
			ack := append([]byte{0x90}, mvar(2+len(granted))...)
			b.send(c, append(append(ack, pid...), granted...))
		case 3:
			qos := byte(hdr>>1) & 3
			topic, rest := mstr(body)
			if qos > 0 {
				b.send(c, append([]byte{0x40, 2}, rest[:2]...))
				rest = rest[2:]
			}
			b.mu.Lock()
			b.pubs = append(b.pubs, topic+"="+string(rest))
			var targets []net.Conn
			for sc, fs := range b.subs {
				for _, f := range fs {
					if f == topic {
						targets = append(targets, sc)
					}
				}
			}
			b.mu.Unlock()
			pkt := append([]byte{byte(0x30)}, mvar(2+len(topic)+len(rest))...)
			pkt = append(pkt, byte(len(topic)>>8), byte(len(topic)))
			pkt = append(append(pkt, topic...), rest...)
			for _, sc := range targets {
				b.send(sc, pkt)
			}
		case 12:
			b.send(c, []byte{0xd0, 0})
		case 14:
			return
		}
	}
}

// Published lists what clients published, as "topic=payload".
func (b *Broker) Published() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.pubs...)
}
