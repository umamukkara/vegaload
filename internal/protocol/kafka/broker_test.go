package kafka

import (
	"net"
	"testing"

	"github.com/vegaload/vegaload/internal/protocol/kafka/kafkatest"
)

type fakeBroker = kafkatest.Broker

func startBroker(t *testing.T) *fakeBroker { return kafkatest.Start(t) }

func newFakeBroker(t *testing.T, ln net.Listener) *fakeBroker { return kafkatest.New(t, ln) }
