package kafka

import (
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kmsg"
)

// fakeBroker is a small Kafka broker for tests. It is a cluster of one
// node. It speaks old, simple versions of the protocol: ApiVersions,
// Metadata, Produce, Fetch, ListOffsets, CreateTopics, DeleteTopics,
// ListGroups and SASL PLAIN. It stores record batches as they arrive and
// gives them back to a reader. It is not a real broker: it is just enough
// to test the driver without Docker.
type fakeBroker struct {
	t  *testing.T
	ln net.Listener

	// Optional SASL PLAIN login. Empty user means no login is needed.
	user, pass string

	mu     sync.Mutex
	topics map[string]*fakeTopic
	groups []string
	conns  map[net.Conn]bool
	nconn  int
	// produced counts the records the broker stored.
	produced int
}

type fakeTopic struct{ parts []*fakePartition }

type fakePartition struct {
	batches []fakeBatch
	next    int64
}

type fakeBatch struct {
	first, last int64
	raw         []byte
}

// Version limits the broker announces. They are low on purpose, so no
// message is "flexible" and the framing stays simple.
var fakeVersions = map[int16][2]int16{
	0:  {3, 3}, // Produce
	1:  {4, 4}, // Fetch
	2:  {1, 1}, // ListOffsets
	3:  {4, 4}, // Metadata
	16: {0, 0}, // ListGroups
	17: {1, 1}, // SaslHandshake
	18: {0, 2}, // ApiVersions
	19: {2, 2}, // CreateTopics
	20: {1, 1}, // DeleteTopics
	36: {0, 1}, // SaslAuthenticate
}

func startBroker(t *testing.T) *fakeBroker {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return newFakeBroker(t, ln)
}

func newFakeBroker(t *testing.T, ln net.Listener) *fakeBroker {
	t.Helper()
	b := &fakeBroker{t: t, ln: ln, topics: map[string]*fakeTopic{}, conns: map[net.Conn]bool{}}
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

func (b *fakeBroker) url() string { return "kafka://" + b.ln.Addr().String() }

func (b *fakeBroker) addTopic(name string, partitions int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t := &fakeTopic{}
	for i := 0; i < partitions; i++ {
		t.parts = append(t.parts, &fakePartition{})
	}
	b.topics[name] = t
}

func (b *fakeBroker) hasTopic(name string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.topics[name]
	return ok
}

func (b *fakeBroker) topicNames() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for n := range b.topics {
		out = append(out, n)
	}
	return out
}

func (b *fakeBroker) producedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.produced
}

func (b *fakeBroker) connCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.nconn
}

func (b *fakeBroker) accept() {
	for {
		c, err := b.ln.Accept()
		if err != nil {
			return
		}
		b.mu.Lock()
		b.conns[c] = true
		b.nconn++
		b.mu.Unlock()
		go b.serve(c)
	}
}

const (
	errNone         = 0
	errOffsetRange  = 1
	errUnknownTopic = 3
	errTopicExists  = 36
	errUnsupported  = 35
	errAuthFailed   = 58
)

func (b *fakeBroker) serve(c net.Conn) {
	defer c.Close()
	needAuth := b.user != ""
	for {
		var sz [4]byte
		if _, err := io.ReadFull(c, sz[:]); err != nil {
			return
		}
		frame := make([]byte, binary.BigEndian.Uint32(sz[:]))
		if _, err := io.ReadFull(c, frame); err != nil {
			return
		}
		if len(frame) < 10 {
			return
		}
		key := int16(binary.BigEndian.Uint16(frame[0:]))
		ver := int16(binary.BigEndian.Uint16(frame[2:]))
		corr := binary.BigEndian.Uint32(frame[4:])
		idLen := int(int16(binary.BigEndian.Uint16(frame[8:])))
		body := frame[10:]
		if idLen > 0 {
			body = body[idLen:]
		}

		lim, known := fakeVersions[key]
		reply := func(resp kmsg.Response) {
			out := binary.BigEndian.AppendUint32(nil, 0)
			out = binary.BigEndian.AppendUint32(out, corr)
			out = resp.AppendTo(out)
			binary.BigEndian.PutUint32(out, uint32(len(out)-4))
			_, _ = c.Write(out)
		}

		if key == 18 && (!known || ver > lim[1]) {
			// Too new: answer in the oldest format, with our versions.
			r := kmsg.NewPtrApiVersionsResponse()
			r.Version = 0
			r.ErrorCode = errUnsupported
			r.ApiKeys = fakeAPIKeys()
			reply(r)
			continue
		}
		if !known || ver < lim[0] || ver > lim[1] {
			return
		}
		req := kmsg.RequestForKey(key)
		req.SetVersion(ver)
		if err := req.ReadFrom(body); err != nil {
			b.t.Errorf("fake broker: reading request %d v%d: %v", key, ver, err)
			return
		}
		if needAuth && key != 18 && key != 17 && key != 36 {
			return // not logged in: drop the connection
		}

		switch r := req.(type) {
		case *kmsg.ApiVersionsRequest:
			resp := kmsg.NewPtrApiVersionsResponse()
			resp.Version = ver
			resp.ApiKeys = fakeAPIKeys()
			reply(resp)
		case *kmsg.SASLHandshakeRequest:
			resp := kmsg.NewPtrSASLHandshakeResponse()
			resp.Version = ver
			resp.SupportedMechanisms = []string{"PLAIN"}
			if r.Mechanism != "PLAIN" {
				resp.ErrorCode = 33 // unsupported mechanism
			}
			reply(resp)
		case *kmsg.SASLAuthenticateRequest:
			resp := kmsg.NewPtrSASLAuthenticateResponse()
			resp.Version = ver
			want := "\x00" + b.user + "\x00" + b.pass
			if string(r.SASLAuthBytes) != want {
				resp.ErrorCode = errAuthFailed
				msg := "wrong user name or password"
				resp.ErrorMessage = &msg
				reply(resp)
				return
			}
			needAuth = false
			reply(resp)
		case *kmsg.MetadataRequest:
			reply(b.metadata(r))
		case *kmsg.ProduceRequest:
			resp := b.produce(r)
			if r.Acks == 0 {
				continue // acks=0 gets no response
			}
			reply(resp)
		case *kmsg.FetchRequest:
			reply(b.fetch(r))
		case *kmsg.ListOffsetsRequest:
			reply(b.listOffsets(r))
		case *kmsg.CreateTopicsRequest:
			reply(b.createTopics(r))
		case *kmsg.DeleteTopicsRequest:
			reply(b.deleteTopics(r))
		case *kmsg.ListGroupsRequest:
			resp := kmsg.NewPtrListGroupsResponse()
			resp.Version = ver
			b.mu.Lock()
			for _, g := range b.groups {
				resp.Groups = append(resp.Groups, kmsg.ListGroupsResponseGroup{Group: g, ProtocolType: "consumer"})
			}
			b.mu.Unlock()
			reply(resp)
		}
	}
}

func fakeAPIKeys() []kmsg.ApiVersionsResponseApiKey {
	var out []kmsg.ApiVersionsResponseApiKey
	for k, v := range fakeVersions {
		out = append(out, kmsg.ApiVersionsResponseApiKey{ApiKey: k, MinVersion: v[0], MaxVersion: v[1]})
	}
	return out
}

func (b *fakeBroker) hostPort() (string, int32) {
	h, p, _ := net.SplitHostPort(b.ln.Addr().String())
	n, _ := strconv.Atoi(p)
	return h, int32(n)
}

func (b *fakeBroker) metadata(r *kmsg.MetadataRequest) *kmsg.MetadataResponse {
	resp := kmsg.NewPtrMetadataResponse()
	resp.Version = r.Version
	host, port := b.hostPort()
	resp.Brokers = []kmsg.MetadataResponseBroker{{NodeID: 1, Host: host, Port: port}}
	cid := "fake-cluster"
	resp.ClusterID = &cid
	resp.ControllerID = 1

	b.mu.Lock()
	defer b.mu.Unlock()
	add := func(name string) {
		t, ok := b.topics[name]
		n := name
		mt := kmsg.MetadataResponseTopic{Topic: &n}
		if !ok {
			mt.ErrorCode = errUnknownTopic
		} else {
			for i := range t.parts {
				mt.Partitions = append(mt.Partitions, kmsg.MetadataResponseTopicPartition{
					Partition: int32(i), Leader: 1, Replicas: []int32{1}, ISR: []int32{1},
				})
			}
		}
		resp.Topics = append(resp.Topics, mt)
	}
	if r.Topics == nil {
		for n := range b.topics {
			add(n)
		}
	} else {
		for _, t := range r.Topics {
			if t.Topic != nil {
				add(*t.Topic)
			}
		}
	}
	return resp
}

func (b *fakeBroker) produce(r *kmsg.ProduceRequest) *kmsg.ProduceResponse {
	resp := kmsg.NewPtrProduceResponse()
	resp.Version = r.Version
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, rt := range r.Topics {
		out := kmsg.ProduceResponseTopic{Topic: rt.Topic}
		t := b.topics[rt.Topic]
		for _, rp := range rt.Partitions {
			p := kmsg.ProduceResponseTopicPartition{Partition: rp.Partition, LogAppendTime: -1}
			if t == nil || int(rp.Partition) >= len(t.parts) || rp.Partition < 0 {
				p.ErrorCode = errUnknownTopic
				p.BaseOffset = -1
			} else {
				part := t.parts[rp.Partition]
				p.BaseOffset = part.next
				raw := rp.Records
				for len(raw) >= 61 {
					total := 12 + int(binary.BigEndian.Uint32(raw[8:]))
					if total > len(raw) {
						break
					}
					batch := append([]byte(nil), raw[:total]...)
					delta := int64(binary.BigEndian.Uint32(batch[23:]))
					binary.BigEndian.PutUint64(batch, uint64(part.next))
					part.batches = append(part.batches, fakeBatch{first: part.next, last: part.next + delta, raw: batch})
					part.next += delta + 1
					b.produced += int(delta) + 1
					raw = raw[total:]
				}
			}
			out.Partitions = append(out.Partitions, p)
		}
		resp.Topics = append(resp.Topics, out)
	}
	return resp
}

func (b *fakeBroker) fetch(r *kmsg.FetchRequest) *kmsg.FetchResponse {
	wait := time.Duration(r.MaxWaitMillis) * time.Millisecond
	deadline := time.Now().Add(wait)
	for {
		resp, any := b.fetchOnce(r)
		if any || time.Now().After(deadline) {
			return resp
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (b *fakeBroker) fetchOnce(r *kmsg.FetchRequest) (*kmsg.FetchResponse, bool) {
	resp := kmsg.NewPtrFetchResponse()
	resp.Version = r.Version
	any := false
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, rt := range r.Topics {
		out := kmsg.FetchResponseTopic{Topic: rt.Topic}
		t := b.topics[rt.Topic]
		for _, rp := range rt.Partitions {
			p := kmsg.FetchResponseTopicPartition{Partition: rp.Partition}
			if t == nil || int(rp.Partition) >= len(t.parts) || rp.Partition < 0 {
				p.ErrorCode = errUnknownTopic
				any = true
			} else {
				part := t.parts[rp.Partition]
				p.HighWatermark, p.LastStableOffset, p.LogStartOffset = part.next, part.next, 0
				if rp.FetchOffset > part.next {
					p.ErrorCode = errOffsetRange
					any = true
				}
				for _, bt := range part.batches {
					if bt.last >= rp.FetchOffset {
						p.RecordBatches = append(p.RecordBatches, bt.raw...)
						any = true
					}
				}
			}
			out.Partitions = append(out.Partitions, p)
		}
		resp.Topics = append(resp.Topics, out)
	}
	return resp, any
}

func (b *fakeBroker) listOffsets(r *kmsg.ListOffsetsRequest) *kmsg.ListOffsetsResponse {
	resp := kmsg.NewPtrListOffsetsResponse()
	resp.Version = r.Version
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, rt := range r.Topics {
		out := kmsg.ListOffsetsResponseTopic{Topic: rt.Topic}
		t := b.topics[rt.Topic]
		for _, rp := range rt.Partitions {
			p := kmsg.ListOffsetsResponseTopicPartition{Partition: rp.Partition, Timestamp: -1}
			if t == nil || int(rp.Partition) >= len(t.parts) || rp.Partition < 0 {
				p.ErrorCode = errUnknownTopic
			} else if rp.Timestamp == -2 { // earliest
				p.Offset = 0
			} else {
				p.Offset = t.parts[rp.Partition].next
			}
			out.Partitions = append(out.Partitions, p)
		}
		resp.Topics = append(resp.Topics, out)
	}
	return resp
}

func (b *fakeBroker) createTopics(r *kmsg.CreateTopicsRequest) *kmsg.CreateTopicsResponse {
	resp := kmsg.NewPtrCreateTopicsResponse()
	resp.Version = r.Version
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, rt := range r.Topics {
		out := kmsg.CreateTopicsResponseTopic{Topic: rt.Topic}
		if _, ok := b.topics[rt.Topic]; ok {
			out.ErrorCode = errTopicExists
			msg := "topic already exists"
			out.ErrorMessage = &msg
		} else {
			t := &fakeTopic{}
			for i := int32(0); i < rt.NumPartitions; i++ {
				t.parts = append(t.parts, &fakePartition{})
			}
			b.topics[rt.Topic] = t
		}
		resp.Topics = append(resp.Topics, out)
	}
	return resp
}

func (b *fakeBroker) deleteTopics(r *kmsg.DeleteTopicsRequest) *kmsg.DeleteTopicsResponse {
	resp := kmsg.NewPtrDeleteTopicsResponse()
	resp.Version = r.Version
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, name := range r.TopicNames {
		n := name
		out := kmsg.DeleteTopicsResponseTopic{Topic: &n}
		if _, ok := b.topics[name]; !ok {
			out.ErrorCode = errUnknownTopic
		}
		delete(b.topics, name)
		resp.Topics = append(resp.Topics, out)
	}
	return resp
}
