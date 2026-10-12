package main

import (
	"strings"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/protocol/ftp"
	"github.com/vegaload/vegaload/internal/protocol/grpc"
	"github.com/vegaload/vegaload/internal/protocol/http1"
	"github.com/vegaload/vegaload/internal/protocol/http2"
	"github.com/vegaload/vegaload/internal/protocol/kafka"
	"github.com/vegaload/vegaload/internal/protocol/mqtt"
	"github.com/vegaload/vegaload/internal/protocol/mysql"
	"github.com/vegaload/vegaload/internal/protocol/postgres"
	"github.com/vegaload/vegaload/internal/protocol/rabbitmq"
	"github.com/vegaload/vegaload/internal/protocol/redis"
	"github.com/vegaload/vegaload/internal/protocol/sftp"
	"github.com/vegaload/vegaload/internal/protocol/socket"
	"github.com/vegaload/vegaload/internal/protocol/websocket"
)

// driverSpec describes one -protocol value. The drivers table below is
// the only place a driver is wired in: the name, the -opt keys it accepts,
// and how to build it. Help text, the unknown-protocol error, the -opt
// check and `vegaload protocols` all read from it, so they cannot drift apart.
type driverSpec struct {
	name         string
	summary      string   // one plain sentence
	since        string   // first release, X.Y.Z
	modes        []string // values of -opt mode; nil when the driver has no modes
	tls          bool     // accepts ca_file, cert_file, key_file, tls_server_name
	writeFlags   []string // options that must be set before a write or a delete
	examples     []string // paths under examples/scenarios, JavaScript then Python
	noExample    string   // why there is no example; printed as "(none yet)"
	scriptGlobal string   // "http" or "ws"; empty when the calls come from netapi
	options      []string // -opt keys this driver accepts, in the driver's order
	build        func(protocol.Target, time.Duration) (protocol.Protocol, error)
}

var drivers = []driverSpec{
	{name: "http1", summary: "HTTP/1.1 requests.", since: "0.1.0", tls: true, scriptGlobal: "http", examples: []string{"examples/scenarios/crud-flow.vl.js", "examples/scenarios/crud_flow.py"}, options: http1.Options, build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := http1.New(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	{name: "http2", summary: "HTTP/2 requests over TLS.", since: "0.1.0", tls: true, scriptGlobal: "http", examples: []string{"examples/scenarios/crud-flow.vl.js", "examples/scenarios/crud_flow.py"}, options: http2.Options, build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := http2.New(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	{name: "grpc", summary: "gRPC calls. JSON in and out when the server offers reflection.", since: "0.1.0", tls: true, noExample: "no example scenario yet", options: grpc.Options, build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := grpc.New(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	{name: "websocket", summary: "WebSocket connections: connect, send and receive.", since: "0.1.0", tls: true, scriptGlobal: "ws", examples: []string{"examples/scenarios/http-ws-chain.vl.js", "examples/scenarios/http_ws_chain.py"}, options: websocket.Options, build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := websocket.New(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	{name: "mqtt", summary: "MQTT publish, subscribe and roundtrip.", since: "0.6.0", modes: []string{"publish", "subscribe", "roundtrip"}, tls: true, examples: []string{"examples/scenarios/mqtt-tcp.vl.js", "examples/scenarios/mqtt_tcp.py"}, options: mqtt.Options, build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := mqtt.New(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	{name: "kafka", summary: "Kafka produce, consume, roundtrip and admin.", since: "0.6.0", modes: []string{"produce", "consume", "roundtrip", "admin"}, tls: true, examples: []string{"examples/scenarios/kafka-orders.vl.js"}, options: kafka.Options, build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := kafka.New(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	{name: "postgres", summary: "PostgreSQL queries. Writes need allow_writes.", since: "0.7.0", tls: true, writeFlags: []string{"allow_writes"}, examples: []string{"examples/scenarios/postgres-orders.vl.js", "examples/scenarios/postgres_orders.py"}, options: postgres.Options, build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := postgres.New(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	{name: "mysql", summary: "MySQL queries. Writes need allow_writes.", since: "0.7.0", tls: true, writeFlags: []string{"allow_writes"}, examples: []string{"examples/scenarios/mysql-orders.vl.js", "examples/scenarios/mysql_orders.py"}, options: mysql.Options, build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := mysql.New(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	{name: "redis", summary: "Redis commands. Writes need allow_writes.", since: "0.7.0", tls: true, writeFlags: []string{"allow_writes", "allow_admin"}, examples: []string{"examples/scenarios/redis-cache.vl.js", "examples/scenarios/redis_cache.py"}, options: redis.Options, build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := redis.New(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	{name: "rabbitmq", summary: "RabbitMQ publish, consume, roundtrip and admin.", since: "0.7.0", modes: []string{"publish", "consume", "roundtrip", "admin"}, tls: true, writeFlags: []string{"allow_writes", "allow_admin"}, examples: []string{"examples/scenarios/rabbitmq-orders.vl.js", "examples/scenarios/rabbitmq_orders.py"}, options: rabbitmq.Options, build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := rabbitmq.New(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	{name: "ftp", summary: "FTP and FTPS: upload, download, list, stat, delete and roundtrip.", since: "0.7.0", modes: []string{"connect", "download", "upload", "list", "stat", "delete", "roundtrip"}, tls: true, writeFlags: []string{"allow_writes", "allow_admin"}, examples: []string{"examples/scenarios/ftp-transfer.vl.js", "examples/scenarios/ftp_transfer.py"}, options: ftp.Options, build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := ftp.New(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	{name: "sftp", summary: "SFTP over SSH: upload, download, list, stat, delete and roundtrip.", since: "0.9.0", modes: []string{"connect", "download", "upload", "list", "stat", "delete", "roundtrip"}, writeFlags: []string{"allow_writes", "allow_admin"}, examples: []string{"examples/scenarios/sftp-transfer.vl.js", "examples/scenarios/sftp_transfer.py"}, options: sftp.Options, build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := sftp.New(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	{name: "tcp", summary: "A raw TCP connection: send one body and read a reply.", since: "0.6.0", tls: true, noExample: "no example scenario yet", options: socket.TCPOptions, build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := socket.NewTCP(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
	// UDP lists the TLS file keys and then rejects them. tls stays false.
	{name: "udp", summary: "A raw UDP datagram: send one body and optionally read one reply.", since: "0.6.0", noExample: "no example scenario yet", options: socket.UDPOptions, build: func(t protocol.Target, to time.Duration) (protocol.Protocol, error) {
		d, err := socket.NewUDP(t, to)
		if err != nil {
			return nil, err
		}
		return d, nil
	}},
}

func (d driverSpec) hasMode(mode string) bool {
	for _, m := range d.modes {
		if m == mode {
			return true
		}
	}
	return false
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
