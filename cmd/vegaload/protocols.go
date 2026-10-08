package main

import (
	"strings"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/protocol/grpc"
	"github.com/vegaload/vegaload/internal/protocol/http1"
	"github.com/vegaload/vegaload/internal/protocol/http2"
	"github.com/vegaload/vegaload/internal/protocol/kafka"
	"github.com/vegaload/vegaload/internal/protocol/mqtt"
	"github.com/vegaload/vegaload/internal/protocol/mysql"
	"github.com/vegaload/vegaload/internal/protocol/postgres"
	"github.com/vegaload/vegaload/internal/protocol/socket"
	"github.com/vegaload/vegaload/internal/protocol/websocket"
)

// driverSpec describes one -protocol value. The drivers table below is
// the only place a driver is wired in: the name, the -opt keys it accepts,
// and how to build it. Help text, the unknown-protocol error and the -opt
// check all read from it, so they cannot drift apart.
type driverSpec struct {
	name    string
	options []string // -opt keys this driver accepts; nil if it takes none
	build   func(protocol.Target, time.Duration) (protocol.Protocol, error)
}

var drivers = []driverSpec{
	{name: "http1", build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		return http1.New(t, to), nil
	}},
	{name: "http2", build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := http2.New(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	{name: "grpc", build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := grpc.New(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	{name: "websocket", build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := websocket.New(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	{name: "mqtt", options: mqtt.Options, build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := mqtt.New(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	{name: "kafka", options: kafka.Options, build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := kafka.New(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	{name: "postgres", options: postgres.Options, build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := postgres.New(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	{name: "mysql", options: mysql.Options, build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := mysql.New(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	{name: "tcp", options: socket.TCPOptions, build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := socket.NewTCP(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	{name: "udp", options: socket.UDPOptions, build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := socket.NewUDP(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
}

func findDriver(name string) (driverSpec, bool) {
	for _, d := range drivers {
		if d.name == name {
			return d, true
		}
	}
	return driverSpec{}, false
}

// protocolNames lists the -protocol values, in table order.
func protocolNames() []string {
	names := make([]string, len(drivers))
	for i, d := range drivers {
		names[i] = d.name
	}
	return names
}

// protocolList renders the -protocol values as "a, b, or c" for help text
// and error messages.
func protocolList() string { return joinOr(protocolNames()) }

func joinOr(names []string) string {
	n := len(names)
	switch n {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " or " + names[1]
	}
	return strings.Join(names[:n-1], ", ") + ", or " + names[n-1]
}
