// Package rabbitmqtest is a small AMQP 0-9-1 broker for tests.
//
// It does not implement headers exchanges (declare returns 540
// NOT_IMPLEMENTED), message TTL, priority ordering, queue-type behaviour,
// or persistence. Those values are stored and, where a reply echoes them,
// sent back unchanged.
package rabbitmqtest

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"time"
)

const (
	frameMethod    = 1
	frameHeader    = 2
	frameBody      = 3
	frameHeartbeat = 8
	frameEnd       = 0xCE
	frameMax       = 131072

	flagContentType     = 0x8000
	flagContentEncoding = 0x4000
	flagHeaders         = 0x2000
	flagDeliveryMode    = 0x1000
	flagPriority        = 0x0800
	flagCorrelationID   = 0x0400
	flagReplyTo         = 0x0200
	flagExpiration      = 0x0100
	flagMessageID       = 0x0080
	flagTimestamp       = 0x0040
	flagType            = 0x0020
	flagUserID          = 0x0010
	flagAppID           = 0x0008
)

// wireFrame is one AMQP frame. Body is the payload without the 0xCE end byte.
type wireFrame struct {
	Type    byte
	Channel uint16
	Body    []byte
}

func writeFrame(w io.Writer, f wireFrame) error {
	var hdr [7]byte
	hdr[0] = f.Type
	binary.BigEndian.PutUint16(hdr[1:3], f.Channel)
	binary.BigEndian.PutUint32(hdr[3:7], uint32(len(f.Body)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.Write(f.Body); err != nil {
		return err
	}
	_, err := w.Write([]byte{frameEnd})
	return err
}

func readFrame(r io.Reader) (wireFrame, error) {
	var hdr [7]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return wireFrame{}, err
	}
	size := binary.BigEndian.Uint32(hdr[3:7])
	if size > frameMax {
		return wireFrame{}, fmt.Errorf("frame size %d is above %d", size, frameMax)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		return wireFrame{}, err
	}
	var end [1]byte
	if _, err := io.ReadFull(r, end[:]); err != nil {
		return wireFrame{}, err
	}
	if end[0] != frameEnd {
		return wireFrame{}, fmt.Errorf("frame end byte is %d, want %d", end[0], frameEnd)
	}
	return wireFrame{Type: hdr[0], Channel: binary.BigEndian.Uint16(hdr[1:3]), Body: body}, nil
}

// splitBody cuts a content body into frames of at most frameMax-8 bytes,
// which is the largest payload that fits in one frame.
func splitBody(body []byte) [][]byte {
	n := frameMax - 8
	if n < 1 {
		n = 1
	}
	if len(body) == 0 {
		return [][]byte{{}}
	}
	var out [][]byte
	for len(body) > 0 {
		take := n
		if take > len(body) {
			take = len(body)
		}
		out = append(out, body[:take])
		body = body[take:]
	}
	return out
}

type props struct {
	ContentType   string
	DeliveryMode  uint8
	Priority      uint8
	Expiration    string
	MessageID     string
	Timestamp     time.Time
	Headers       map[string]any
	CorrelationID string
	ReplyTo       string
	Type          string
	UserID        string
	AppID         string
}

func encodeMethod(class, method uint16, args []byte) []byte {
	out := make([]byte, 4+len(args))
	binary.BigEndian.PutUint16(out[0:2], class)
	binary.BigEndian.PutUint16(out[2:4], method)
	copy(out[4:], args)
	return out
}

func decodeMethod(body []byte) (class, method uint16, args []byte, err error) {
	if len(body) < 4 {
		return 0, 0, nil, io.ErrUnexpectedEOF
	}
	return binary.BigEndian.Uint16(body[0:2]), binary.BigEndian.Uint16(body[2:4]), body[4:], nil
}

func encodeHeader(classID uint16, bodySize uint64, p props) ([]byte, error) {
	var buf []byte
	put := func(b []byte) { buf = append(buf, b...) }
	var u16 [2]byte
	var u64 [8]byte
	binary.BigEndian.PutUint16(u16[:], classID)
	put(u16[:])
	put([]byte{0, 0}) // weight
	binary.BigEndian.PutUint64(u64[:], bodySize)
	put(u64[:])

	var mask uint16
	if p.ContentType != "" {
		mask |= flagContentType
	}
	if len(p.Headers) > 0 {
		mask |= flagHeaders
	}
	if p.DeliveryMode > 0 {
		mask |= flagDeliveryMode
	}
	if p.Priority > 0 {
		mask |= flagPriority
	}
	if p.CorrelationID != "" {
		mask |= flagCorrelationID
	}
	if p.ReplyTo != "" {
		mask |= flagReplyTo
	}
	if p.Expiration != "" {
		mask |= flagExpiration
	}
	if p.MessageID != "" {
		mask |= flagMessageID
	}
	if !p.Timestamp.IsZero() {
		mask |= flagTimestamp
	}
	if p.Type != "" {
		mask |= flagType
	}
	if p.UserID != "" {
		mask |= flagUserID
	}
	if p.AppID != "" {
		mask |= flagAppID
	}
	binary.BigEndian.PutUint16(u16[:], mask)
	put(u16[:])

	var err error
	addShort := func(s string) {
		if err != nil {
			return
		}
		b, e := appendShort(nil, s)
		if e != nil {
			err = e
			return
		}
		put(b)
	}
	if p.ContentType != "" {
		addShort(p.ContentType)
	}
	if len(p.Headers) > 0 {
		b, e := appendTable(nil, p.Headers)
		if e != nil {
			return nil, e
		}
		put(b)
	}
	if p.DeliveryMode > 0 {
		put([]byte{p.DeliveryMode})
	}
	if p.Priority > 0 {
		put([]byte{p.Priority})
	}
	if p.CorrelationID != "" {
		addShort(p.CorrelationID)
	}
	if p.ReplyTo != "" {
		addShort(p.ReplyTo)
	}
	if p.Expiration != "" {
		addShort(p.Expiration)
	}
	if p.MessageID != "" {
		addShort(p.MessageID)
	}
	if !p.Timestamp.IsZero() {
		binary.BigEndian.PutUint64(u64[:], uint64(p.Timestamp.Unix()))
		put(u64[:])
	}
	if p.Type != "" {
		addShort(p.Type)
	}
	if p.UserID != "" {
		addShort(p.UserID)
	}
	if p.AppID != "" {
		addShort(p.AppID)
	}
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func decodeHeader(body []byte) (classID uint16, size uint64, p props, err error) {
	if len(body) < 14 {
		return 0, 0, props{}, io.ErrUnexpectedEOF
	}
	classID = binary.BigEndian.Uint16(body[0:2])
	size = binary.BigEndian.Uint64(body[4:12])
	mask := binary.BigEndian.Uint16(body[12:14])
	rest := body[14:]
	takeShort := func() (string, error) {
		s, n, err := readShort(rest)
		if err != nil {
			return "", err
		}
		rest = rest[n:]
		return s, nil
	}
	if mask&flagContentType != 0 {
		if p.ContentType, err = takeShort(); err != nil {
			return
		}
	}
	if mask&flagContentEncoding != 0 {
		if _, err = takeShort(); err != nil {
			return
		}
	}
	if mask&flagHeaders != 0 {
		var n int
		p.Headers, n, err = readTable(rest)
		if err != nil {
			return
		}
		rest = rest[n:]
	}
	if mask&flagDeliveryMode != 0 {
		if len(rest) < 1 {
			return 0, 0, props{}, io.ErrUnexpectedEOF
		}
		p.DeliveryMode = rest[0]
		rest = rest[1:]
	}
	if mask&flagPriority != 0 {
		if len(rest) < 1 {
			return 0, 0, props{}, io.ErrUnexpectedEOF
		}
		p.Priority = rest[0]
		rest = rest[1:]
	}
	if mask&flagCorrelationID != 0 {
		if p.CorrelationID, err = takeShort(); err != nil {
			return
		}
	}
	if mask&flagReplyTo != 0 {
		if p.ReplyTo, err = takeShort(); err != nil {
			return
		}
	}
	if mask&flagExpiration != 0 {
		if p.Expiration, err = takeShort(); err != nil {
			return
		}
	}
	if mask&flagMessageID != 0 {
		if p.MessageID, err = takeShort(); err != nil {
			return
		}
	}
	if mask&flagTimestamp != 0 {
		if len(rest) < 8 {
			return 0, 0, props{}, io.ErrUnexpectedEOF
		}
		p.Timestamp = time.Unix(int64(binary.BigEndian.Uint64(rest[:8])), 0).UTC()
		rest = rest[8:]
	}
	if mask&flagType != 0 {
		if p.Type, err = takeShort(); err != nil {
			return
		}
	}
	if mask&flagUserID != 0 {
		if p.UserID, err = takeShort(); err != nil {
			return
		}
	}
	if mask&flagAppID != 0 {
		if p.AppID, err = takeShort(); err != nil {
			return
		}
	}
	return classID, size, p, nil
}

func appendShort(dst []byte, s string) ([]byte, error) {
	if len(s) > 255 {
		return nil, fmt.Errorf("short string is %d bytes", len(s))
	}
	dst = append(dst, byte(len(s)))
	return append(dst, s...), nil
}

func readShort(b []byte) (string, int, error) {
	if len(b) < 1 {
		return "", 0, io.ErrUnexpectedEOF
	}
	n := int(b[0])
	if len(b) < 1+n {
		return "", 0, io.ErrUnexpectedEOF
	}
	return string(b[1 : 1+n]), 1 + n, nil
}

func appendLong(dst []byte, s string) []byte {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(s)))
	dst = append(dst, n[:]...)
	return append(dst, s...)
}

func readLong(b []byte) (string, int, error) {
	if len(b) < 4 {
		return "", 0, io.ErrUnexpectedEOF
	}
	n := int(binary.BigEndian.Uint32(b[:4]))
	if n < 0 || len(b) < 4+n {
		return "", 0, io.ErrUnexpectedEOF
	}
	return string(b[4 : 4+n]), 4 + n, nil
}

func appendTable(dst []byte, m map[string]any) ([]byte, error) {
	body, err := appendFields(nil, m)
	if err != nil {
		return nil, err
	}
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(body)))
	dst = append(dst, n[:]...)
	return append(dst, body...), nil
}

func appendFields(dst []byte, m map[string]any) ([]byte, error) {
	for k, v := range m {
		var err error
		dst, err = appendShort(dst, k)
		if err != nil {
			return nil, err
		}
		dst, err = appendValue(dst, v)
		if err != nil {
			return nil, err
		}
	}
	return dst, nil
}

func appendValue(dst []byte, v any) ([]byte, error) {
	switch t := v.(type) {
	case bool:
		b := byte(0)
		if t {
			b = 1
		}
		return append(dst, 't', b), nil
	case int8:
		return append(dst, 'b', byte(t)), nil
	case uint8:
		return append(dst, 'B', t), nil
	case int16:
		var n [2]byte
		binary.BigEndian.PutUint16(n[:], uint16(t))
		return append(append(dst, 's'), n[:]...), nil
	case int32:
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(t))
		return append(append(dst, 'I'), n[:]...), nil
	case int:
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(t))
		return append(append(dst, 'I'), n[:]...), nil
	case int64:
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(t))
		return append(append(dst, 'l'), n[:]...), nil
	case float32:
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], math.Float32bits(t))
		return append(append(dst, 'f'), n[:]...), nil
	case float64:
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], math.Float64bits(t))
		return append(append(dst, 'd'), n[:]...), nil
	case string:
		dst = append(dst, 'S')
		return appendLong(dst, t), nil
	case []byte:
		dst = append(dst, 'x')
		return appendLong(dst, string(t)), nil
	case time.Time:
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(t.Unix()))
		return append(append(dst, 'T'), n[:]...), nil
	case map[string]any:
		dst = append(dst, 'F')
		return appendTable(dst, t)
	case nil:
		return append(dst, 'V'), nil
	default:
		return nil, fmt.Errorf("field type %T", v)
	}
}

func readTable(b []byte) (map[string]any, int, error) {
	s, n, err := readLong(b)
	if err != nil {
		return nil, 0, err
	}
	m, err := readFields([]byte(s))
	if err != nil {
		return nil, 0, err
	}
	return m, n, nil
}

func readFields(b []byte) (map[string]any, error) {
	m := map[string]any{}
	for len(b) > 0 {
		k, n, err := readShort(b)
		if err != nil {
			return nil, err
		}
		b = b[n:]
		v, n, err := readValue(b)
		if err != nil {
			return nil, err
		}
		b = b[n:]
		m[k] = v
	}
	return m, nil
}

func readValue(b []byte) (any, int, error) {
	if len(b) < 1 {
		return nil, 0, io.ErrUnexpectedEOF
	}
	kind := b[0]
	b = b[1:]
	need := func(n int) error {
		if len(b) < n {
			return io.ErrUnexpectedEOF
		}
		return nil
	}
	switch kind {
	case 't':
		if err := need(1); err != nil {
			return nil, 0, err
		}
		return b[0] != 0, 2, nil
	case 'b':
		if err := need(1); err != nil {
			return nil, 0, err
		}
		return int8(b[0]), 2, nil
	case 'B':
		if err := need(1); err != nil {
			return nil, 0, err
		}
		return b[0], 2, nil
	case 's':
		if err := need(2); err != nil {
			return nil, 0, err
		}
		return int16(binary.BigEndian.Uint16(b[:2])), 3, nil
	case 'u':
		if err := need(2); err != nil {
			return nil, 0, err
		}
		return binary.BigEndian.Uint16(b[:2]), 3, nil
	case 'I':
		if err := need(4); err != nil {
			return nil, 0, err
		}
		return int32(binary.BigEndian.Uint32(b[:4])), 5, nil
	case 'i':
		if err := need(4); err != nil {
			return nil, 0, err
		}
		return binary.BigEndian.Uint32(b[:4]), 5, nil
	case 'l':
		if err := need(8); err != nil {
			return nil, 0, err
		}
		return int64(binary.BigEndian.Uint64(b[:8])), 9, nil
	case 'f':
		if err := need(4); err != nil {
			return nil, 0, err
		}
		return math.Float32frombits(binary.BigEndian.Uint32(b[:4])), 5, nil
	case 'd':
		if err := need(8); err != nil {
			return nil, 0, err
		}
		return math.Float64frombits(binary.BigEndian.Uint64(b[:8])), 9, nil
	case 'D':
		if err := need(5); err != nil {
			return nil, 0, err
		}
		return map[string]any{"scale": b[0], "value": int32(binary.BigEndian.Uint32(b[1:5]))}, 6, nil
	case 'S':
		s, n, err := readLong(b)
		if err != nil {
			return nil, 0, err
		}
		return s, 1 + n, nil
	case 'x':
		s, n, err := readLong(b)
		if err != nil {
			return nil, 0, err
		}
		return []byte(s), 1 + n, nil
	case 'T':
		if err := need(8); err != nil {
			return nil, 0, err
		}
		return time.Unix(int64(binary.BigEndian.Uint64(b[:8])), 0).UTC(), 9, nil
	case 'F':
		m, n, err := readTable(b)
		if err != nil {
			return nil, 0, err
		}
		return m, 1 + n, nil
	case 'A':
		s, n, err := readLong(b)
		if err != nil {
			return nil, 0, err
		}
		rest := []byte(s)
		var arr []any
		for len(rest) > 0 {
			v, vn, err := readValue(rest)
			if err != nil {
				return nil, 0, err
			}
			rest = rest[vn:]
			arr = append(arr, v)
		}
		return arr, 1 + n, nil
	case 'V':
		return nil, 1, nil
	default:
		return nil, 0, fmt.Errorf("unknown field type %q", kind)
	}
}

func putU16(b []byte, v uint16) []byte {
	var n [2]byte
	binary.BigEndian.PutUint16(n[:], v)
	return append(b, n[:]...)
}

func putU32(b []byte, v uint32) []byte {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], v)
	return append(b, n[:]...)
}

func putU64(b []byte, v uint64) []byte {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], v)
	return append(b, n[:]...)
}

func u16(b []byte) (uint16, []byte, error) {
	if len(b) < 2 {
		return 0, nil, io.ErrUnexpectedEOF
	}
	return binary.BigEndian.Uint16(b[:2]), b[2:], nil
}

func u32(b []byte) (uint32, []byte, error) {
	if len(b) < 4 {
		return 0, nil, io.ErrUnexpectedEOF
	}
	return binary.BigEndian.Uint32(b[:4]), b[4:], nil
}

func u64(b []byte) (uint64, []byte, error) {
	if len(b) < 8 {
		return 0, nil, io.ErrUnexpectedEOF
	}
	return binary.BigEndian.Uint64(b[:8]), b[8:], nil
}

func shortStr(b []byte) (string, []byte, error) {
	s, n, err := readShort(b)
	if err != nil {
		return "", nil, err
	}
	return s, b[n:], nil
}

func longStr(b []byte) (string, []byte, error) {
	s, n, err := readLong(b)
	if err != nil {
		return "", nil, err
	}
	return s, b[n:], nil
}

func oneBit(b []byte) (bool, []byte, error) {
	if len(b) < 1 {
		return false, nil, io.ErrUnexpectedEOF
	}
	return b[0]&1 != 0, b[1:], nil
}
