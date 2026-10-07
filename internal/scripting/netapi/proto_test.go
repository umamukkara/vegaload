package netapi

import (
	"context"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol/kafka/kafkatest"
)

func TestKafka_ClientIsKeptPerConnectionSetup(t *testing.T) {
	b := kafkatest.Start(t)
	b.AddTopic("t", 1)
	c := NewProtoClient(nil, 5*time.Second)
	defer c.Close()
	for i := 0; i < 5; i++ {
		r, err := c.Kafka(context.Background(), "produce", ProtoCall{
			URL: b.URL(), Body: []byte("x"), Options: map[string]string{"topic": "t"},
		})
		if err != nil || !r.OK {
			t.Fatalf("call %d: %v %+v", i, err, r)
		}
	}
	if n := b.ConnCount(); n > 3 {
		t.Errorf("%d connections for 5 calls, want the client reused", n)
	}
	if len(c.kafkaConns) != 1 {
		t.Errorf("%d clients kept, want 1", len(c.kafkaConns))
	}
	// A different topic is a different job, not a different client.
	b.AddTopic("u", 1)
	if r, err := c.Kafka(context.Background(), "produce", ProtoCall{URL: b.URL(), Options: map[string]string{"topic": "u"}}); err != nil || !r.OK {
		t.Fatalf("%v %+v", err, r)
	}
	if len(c.kafkaConns) != 1 {
		t.Errorf("a new topic made a new client: %d", len(c.kafkaConns))
	}
	// A different login or setting is a different client.
	if _, err := c.Kafka(context.Background(), "produce", ProtoCall{URL: b.URL(), Options: map[string]string{"topic": "t", "acks": "leader"}}); err != nil {
		t.Fatal(err)
	}
	if len(c.kafkaConns) != 2 {
		t.Errorf("%d clients kept after acks=leader, want 2", len(c.kafkaConns))
	}
	c.Close()
	if len(c.kafkaConns) != 0 {
		t.Error("Close must release the clients")
	}
}
