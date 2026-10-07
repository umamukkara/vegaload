package netapi

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/protocol/mqtt"
	"github.com/vegaload/vegaload/internal/protocol/socket"
)

// defaultProtoTimeout is used when the caller gives no timeout at all.
const defaultProtoTimeout = 10 * time.Second

// ProtoCall is one call a script makes to a tcp, udp or mqtt target.
type ProtoCall struct {
	// URL is the target. tcp and udp accept a bare host:port too.
	URL string
	// Body is the data to send.
	Body []byte
	// Options are the driver's own options, the same keys as -opt.
	Options map[string]string
	// Insecure turns off TLS certificate checks.
	Insecure bool
	// Timeout bounds the whole call. Zero means the client's default.
	Timeout time.Duration
	// Password is the MQTT password. nil means none. A script passes it
	// here, from `env`, so it never has to be put in Options.
	Password *string
}

// ProtoMessage is one MQTT message a call received.
type ProtoMessage struct {
	Topic string
	Body  []byte
}

// ProtoReply is what a call hands back. A call that fails on the network
// (refused, timed out, no reply) is a reply with OK false and Error set,
// not a Go error: a script is expected to look at it.
type ProtoReply struct {
	OK            bool
	Error         string
	BytesSent     int64
	BytesReceived int64
	// Body is the reply read from a tcp or udp target. It is set even when
	// the call failed after some bytes came in.
	Body []byte
	// Messages are the messages an mqtt subscribe or roundtrip received.
	Messages []ProtoMessage
}

// ProtoClient makes tcp, udp and mqtt calls for one VU. Each call opens
// its own connection, the same as the load-test drivers do, so the client
// holds no connection and needs no Close.
type ProtoClient struct {
	check   SafetyCheck
	timeout time.Duration
}

// NewProtoClient returns a ProtoClient. check, when not nil, is run on the
// host of every call before it connects. timeout is the default for a call.
func NewProtoClient(check SafetyCheck, timeout time.Duration) *ProtoClient {
	if timeout <= 0 {
		timeout = defaultProtoTimeout
	}
	return &ProtoClient{check: check, timeout: timeout}
}

// TCP sends over one TCP connection and reads the reply the options ask for.
func (c *ProtoClient) TCP(ctx context.Context, call ProtoCall) (*ProtoReply, error) {
	return c.socket(ctx, "tcp", call)
}

// UDP sends one datagram, and waits for a reply if the options ask for it.
func (c *ProtoClient) UDP(ctx context.Context, call ProtoCall) (*ProtoReply, error) {
	return c.socket(ctx, "udp", call)
}

// MQTT does one mqtt job. mode is publish, subscribe or roundtrip.
func (c *ProtoClient) MQTT(ctx context.Context, mode string, call ProtoCall) (*ProtoReply, error) {
	if err := c.checkTarget(call.URL, "mqtt"); err != nil {
		return nil, err
	}
	opts := cloneOptions(call.Options)
	if _, ok := opts["mode"]; ok {
		return nil, errors.New("mqtt: option mode is not allowed here, the function you called sets it")
	}
	opts["mode"] = mode
	target := protocol.Target{URL: call.URL, Body: call.Body, Options: opts, InsecureSkipVerify: call.Insecure}

	var (
		d   *mqtt.Driver
		err error
	)
	if call.Password != nil {
		d, err = mqtt.NewWithPassword(target, c.timeoutFor(call), *call.Password)
	} else {
		d, err = mqtt.New(target, c.timeoutFor(call))
	}
	if err != nil {
		return nil, err
	}
	res, msgs := d.Run(ctx)
	r := fromResult(res)
	for _, m := range msgs {
		r.Messages = append(r.Messages, ProtoMessage{Topic: m.Topic, Body: m.Payload})
	}
	return r, nil
}

func (c *ProtoClient) socket(ctx context.Context, network string, call ProtoCall) (*ProtoReply, error) {
	if err := c.checkTarget(call.URL, network); err != nil {
		return nil, err
	}
	opts := cloneOptions(call.Options)
	// A script writes real newlines and \x00 itself. Turning on the
	// command line's backslash escapes by default would change what it sent.
	if _, ok := opts["escape"]; !ok {
		opts["escape"] = "false"
	}
	target := protocol.Target{URL: call.URL, Body: call.Body, Options: opts, InsecureSkipVerify: call.Insecure}
	newDriver := socket.NewTCP
	if network == "udp" {
		newDriver = socket.NewUDP
	}
	d, err := newDriver(target, c.timeoutFor(call))
	if err != nil {
		return nil, err
	}
	res, body := d.Run(ctx)
	r := fromResult(res)
	r.Body = body
	return r, nil
}

func (c *ProtoClient) timeoutFor(call ProtoCall) time.Duration {
	if call.Timeout > 0 {
		return call.Timeout
	}
	return c.timeout
}

func fromResult(res protocol.Result) *ProtoReply {
	r := &ProtoReply{OK: res.Success, BytesSent: res.BytesSent, BytesReceived: res.BytesReceived}
	if res.Err != nil {
		r.Error = res.Err.Error()
	}
	return r
}

func cloneOptions(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

// checkTarget runs the safety check on the call's host. A target that has
// no host is refused here, because the check cannot judge it.
func (c *ProtoClient) checkTarget(rawURL, scheme string) error {
	if c.check == nil {
		return nil
	}
	if rawURL == "" {
		return fmt.Errorf("%s: a url is required", scheme)
	}
	if !strings.Contains(rawURL, "://") {
		rawURL = scheme + "://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%s: parsing url: %w", scheme, err)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("%s: url %q has no host", scheme, rawURL)
	}
	return c.check(u.Hostname())
}
