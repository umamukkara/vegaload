package mqtttest

import (
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"
	"time"
)

// A QoS 1 client returns as soon as it has read PUBACK. The publish must
// already be in Published by then, including when the caller checks in the
// same goroutine that read the ack.
func TestQoS1PublishIsVisibleWhenPUBACKArrives(t *testing.T) {
	b := Start(t)
	c, err := net.Dial("tcp", b.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 200; i++ {
		payload := "m" + strconv.Itoa(i)
		id := uint16(i + 1)
		if _, err := c.Write(publishQoS1("t", payload, id)); err != nil {
			t.Fatal(err)
		}
		ack := make([]byte, 4)
		if _, err := io.ReadFull(c, ack); err != nil {
			t.Fatalf("puback %d: %v", i, err)
		}
		if ack[0] != 0x40 || ack[1] != 2 || binary.BigEndian.Uint16(ack[2:]) != id {
			t.Fatalf("puback %d = %x", i, ack)
		}
		want := "t=" + payload
		got := b.Published()
		if len(got) != i+1 || got[i] != want {
			t.Fatalf("after publish %d, broker saw %v, want %s", i, got, want)
		}
	}
}

func publishQoS1(topic, payload string, id uint16) []byte {
	body := make([]byte, 2+len(topic)+2+len(payload))
	binary.BigEndian.PutUint16(body, uint16(len(topic)))
	copy(body[2:], topic)
	binary.BigEndian.PutUint16(body[2+len(topic):], id)
	copy(body[4+len(topic):], payload)
	return append(append([]byte{0x32}, mvar(len(body))...), body...)
}
