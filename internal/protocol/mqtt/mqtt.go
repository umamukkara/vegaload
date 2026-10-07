// Package mqtt implements the "mqtt" protocol.Protocol driver, for load
// testing an MQTT 3.1.1 broker. It uses the Eclipse Paho client.
//
// Each call to Do opens its own connection, does one job, and closes. That
// measures connect, publish and delivery time under load. It is not a
// long-lived session. The job is chosen with -opt mode=...:
//
//	publish    connect, publish the body, wait for the broker's ack. This
//	           is the default.
//	subscribe  connect, subscribe, wait for count messages.
//	roundtrip  connect, subscribe, publish the body, wait until the same
//	           message comes back. This measures delivery through the broker.
//
// Options (set with -opt key=value):
//
//	mode           publish (default), subscribe, or roundtrip.
//	topic          The topic. Required. {id} is replaced by this
//	               iteration's unique client id, so each iteration can
//	               have its own topic. Publish and roundtrip topics must
//	               not have wildcards.
//	qos            0 (default), 1, or 2.
//	retain         true to publish the message as retained.
//	username       The user name.
//	password_env   The name of an environment variable that holds the
//	               password. The password is never put on the command line.
//	client_id      A prefix for the client ids (default "vegaload"). Each
//	               iteration adds a unique suffix, because a broker closes
//	               an older connection that has the same id.
//	count          subscribe and roundtrip: messages to wait for (default 1).
//	expect         subscribe: every message must contain this text.
//	clean          false to ask for a persistent session (default true).
//	keepalive      Keep-alive time, such as 30s (default 30s).
//
// The body is the message payload. {id} in it is replaced too. Use an
// mqtts:// target for TLS.
package mqtt

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/vegaload/vegaload/internal/protocol"
)

// Options are the -opt keys this driver accepts. The command's protocol
// table uses the same list, so the two cannot differ.
var Options = []string{
	"mode", "topic", "qos", "retain", "username", "password_env",
	"client_id", "count", "expect", "clean", "keepalive",
}

const (
	modePublish   = "publish"
	modeSubscribe = "subscribe"
	modeRoundtrip = "roundtrip"

	maxCount = 100000
)

// Driver is an MQTT protocol.Protocol.
type Driver struct {
	broker   string // tcp://host:port or ssl://host:port, as Paho wants it
	target   protocol.Target
	timeout  time.Duration
	mode     string
	topic    string
	qos      byte
	retain   bool
	username string
	password string
	prefix   string
	count    int
	expect   []byte
	clean    bool
	alive    time.Duration

	salt string // random, per Driver, so two runs never share client ids
	seq  atomic.Uint64
}

// New returns a ready-to-use Driver for target. target.URL is mqtt://host
// or mqtts://host, with an optional :port (1883, or 8883 for mqtts).
//
// New returns an error only for a configuration problem, never for the
// broker being unreachable, which Do reports per call.
func New(target protocol.Target, timeout time.Duration) (*Driver, error) {
	d := &Driver{target: target, timeout: timeout}

	u, err := url.Parse(target.URL)
	if err != nil {
		return nil, fmt.Errorf("mqtt: parsing target URL: %w", err)
	}
	port := u.Port()
	switch u.Scheme {
	case "mqtt":
		if port == "" {
			port = "1883"
		}
		d.broker = "tcp://" + net.JoinHostPort(u.Hostname(), port)
	case "mqtts":
		if port == "" {
			port = "8883"
		}
		d.broker = "ssl://" + net.JoinHostPort(u.Hostname(), port)
	default:
		return nil, fmt.Errorf("mqtt: unsupported scheme %q, want mqtt:// or mqtts://", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("mqtt: target %q has no host", target.URL)
	}

	if err := target.RejectUnknownOptions(Options...); err != nil {
		return nil, fmt.Errorf("mqtt: %w", err)
	}

	d.mode = target.Option("mode", modePublish)
	switch d.mode {
	case modePublish, modeSubscribe, modeRoundtrip:
	default:
		return nil, fmt.Errorf("mqtt: mode=%q, want publish, subscribe, or roundtrip", d.mode)
	}

	d.topic = target.Option("topic", "")
	if d.topic == "" {
		return nil, errors.New("mqtt: a topic is required (pass -opt topic=...)")
	}
	if d.mode != modeSubscribe && strings.ContainsAny(d.topic, "+#") {
		return nil, fmt.Errorf("mqtt: topic %q has a wildcard, which is only allowed in subscribe mode", d.topic)
	}

	qos, err := target.OptionInt("qos", 0)
	if err != nil {
		return nil, fmt.Errorf("mqtt: %w", err)
	}
	if qos < 0 || qos > 2 {
		return nil, fmt.Errorf("mqtt: qos=%d, want 0, 1, or 2", qos)
	}
	d.qos = byte(qos)

	if d.retain, err = target.OptionBool("retain", false); err != nil {
		return nil, fmt.Errorf("mqtt: %w", err)
	}
	if d.clean, err = target.OptionBool("clean", true); err != nil {
		return nil, fmt.Errorf("mqtt: %w", err)
	}
	if d.count, err = target.OptionInt("count", 1); err != nil {
		return nil, fmt.Errorf("mqtt: %w", err)
	}
	if d.count < 1 || d.count > maxCount {
		return nil, fmt.Errorf("mqtt: count=%d, want 1 to %d", d.count, maxCount)
	}
	if d.alive, err = target.OptionDuration("keepalive", 30*time.Second); err != nil {
		return nil, fmt.Errorf("mqtt: %w", err)
	}
	if d.alive < time.Second {
		return nil, fmt.Errorf("mqtt: keepalive=%s, want at least 1s", d.alive)
	}

	d.username = target.Option("username", "")
	if env := target.Option("password_env", ""); env != "" {
		pw, ok := os.LookupEnv(env)
		if !ok || pw == "" {
			return nil, fmt.Errorf("mqtt: password_env=%s, but that environment variable is not set", env)
		}
		d.password = pw
	}
	d.prefix = target.Option("client_id", "vegaload")
	if v, ok := target.Options["expect"]; ok {
		d.expect = []byte(v)
	}
	if len(d.expect) > 0 && d.mode != modeSubscribe {
		return nil, errors.New("mqtt: expect is only used in subscribe mode")
	}

	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, fmt.Errorf("mqtt: making a client id: %w", err)
	}
	d.salt = hex.EncodeToString(b[:])
	return d, nil
}

// Name implements protocol.Protocol.
func (d *Driver) Name() string { return "mqtt" }

// Close implements protocol.Protocol. Do opens and closes its own
// connection every call, so Driver holds nothing to release.
func (d *Driver) Close() error { return nil }

// clientID returns a new, unique id for one iteration.
func (d *Driver) clientID() string {
	return d.prefix + "-" + d.salt + "-" + strconv.FormatUint(d.seq.Add(1), 36)
}

func fail(sent, got int64, err error) protocol.Result {
	return protocol.Result{Success: false, BytesSent: sent, BytesReceived: got, Err: err}
}

var errTimeout = errors.New("mqtt: timed out")

// wait waits for a Paho token, the deadline, or the end of the run.
func wait(ctx context.Context, tok paho.Token, deadline time.Time, what string) error {
	t := time.NewTimer(time.Until(deadline))
	defer t.Stop()
	select {
	case <-tok.Done():
		if err := tok.Error(); err != nil {
			return fmt.Errorf("mqtt: %s: %w", what, err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return fmt.Errorf("%w waiting for %s", errTimeout, what)
	}
}

// Do implements protocol.Protocol.
func (d *Driver) Do(ctx context.Context) (protocol.Result, error) {
	deadline := time.Now().Add(d.timeout)
	if cd, ok := ctx.Deadline(); ok && cd.Before(deadline) {
		deadline = cd
	}

	id := d.clientID()
	topic := strings.ReplaceAll(d.topic, "{id}", id)
	payload := d.target.Body
	if bytes.Contains(payload, []byte("{id}")) {
		payload = bytes.ReplaceAll(payload, []byte("{id}"), []byte(id))
	}

	opts := paho.NewClientOptions().
		AddBroker(d.broker).
		SetClientID(id).
		SetCleanSession(d.clean).
		SetKeepAlive(d.alive).
		SetConnectTimeout(d.timeout).
		SetWriteTimeout(d.timeout).
		SetAutoReconnect(false).
		SetConnectRetry(false).
		SetOrderMatters(false).
		SetTLSConfig(&tls.Config{InsecureSkipVerify: d.target.InsecureSkipVerify}) //nolint:gosec // opt-in, see protocol.Target
	if d.username != "" {
		opts.SetUsername(d.username)
		opts.SetPassword(d.password)
	}

	// Messages that arrive for our subscription. A surplus message is
	// dropped, so a busy topic can never block Paho's delivery.
	size := d.count
	if d.mode == modeRoundtrip {
		size += 256 // room for other users' messages on a shared topic
	}
	msgs := make(chan []byte, size)
	handler := func(_ paho.Client, m paho.Message) {
		select {
		case msgs <- append([]byte(nil), m.Payload()...):
		default:
		}
	}

	c := paho.NewClient(opts)
	if err := wait(ctx, c.Connect(), deadline, "the connection"); err != nil {
		return fail(0, 0, err), nil
	}
	defer c.Disconnect(100)

	var sent, got int64
	subscribe := func() error {
		tok := c.Subscribe(topic, d.qos, handler)
		if err := wait(ctx, tok, deadline, "the subscription"); err != nil {
			return err
		}
		if st, ok := tok.(*paho.SubscribeToken); ok {
			if code, found := st.Result()[topic]; found && code == 0x80 {
				return fmt.Errorf("mqtt: the broker refused the subscription to %q", topic)
			}
		}
		return nil
	}
	publish := func() error {
		if err := wait(ctx, c.Publish(topic, d.qos, d.retain, payload), deadline, "the publish"); err != nil {
			return err
		}
		sent = int64(len(payload))
		return nil
	}

	switch d.mode {
	case modePublish:
		if err := publish(); err != nil {
			return fail(sent, got, err), nil
		}

	case modeSubscribe:
		if err := subscribe(); err != nil {
			return fail(sent, got, err), nil
		}
		for n := 0; n < d.count; n++ {
			m, err := d.receive(ctx, msgs, deadline)
			if err != nil {
				return fail(sent, got, err), nil
			}
			got += int64(len(m))
			if len(d.expect) > 0 && !bytes.Contains(m, d.expect) {
				return fail(sent, got, fmt.Errorf("mqtt: message %q did not contain %q", clip(m), d.expect)), nil
			}
		}

	case modeRoundtrip:
		if err := subscribe(); err != nil {
			return fail(sent, got, err), nil
		}
		if err := publish(); err != nil {
			return fail(sent, got, err), nil
		}
		// Wait for our own message to come back. Other messages on the
		// topic, from other users, are skipped.
		for n := 0; n < d.count; {
			m, err := d.receive(ctx, msgs, deadline)
			if err != nil {
				return fail(sent, got, err), nil
			}
			if bytes.Equal(m, payload) {
				n++
				got += int64(len(m))
			}
		}
	}
	return protocol.Result{Success: true, BytesSent: sent, BytesReceived: got}, nil
}

// receive waits for the next message.
func (d *Driver) receive(ctx context.Context, msgs <-chan []byte, deadline time.Time) ([]byte, error) {
	t := time.NewTimer(time.Until(deadline))
	defer t.Stop()
	select {
	case m := <-msgs:
		return m, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.C:
		return nil, fmt.Errorf("%w waiting for a message", errTimeout)
	}
}

func clip(b []byte) string {
	if len(b) > 40 {
		return string(b[:40]) + "..."
	}
	return string(b)
}
