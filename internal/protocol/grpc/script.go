package grpc

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/vegaload/vegaload/internal/protocol"
)

// Script mode (FR-CLI-19). A scenario script calls a gRPC method with JSON.
// The connection asks the server for its descriptors (server reflection) the
// first time a method is used, so the script needs no proto files. A server
// without reflection can still be called with base64 bodies.

// Encodings of a script call's body and reply.
const (
	EncodingJSON   = "json"
	EncodingBase64 = "base64"
)

// Conn is a gRPC client for scripts. It dials lazily, keeps one connection
// for every call, and remembers each method's message types. It is safe for
// concurrent use.
type Conn struct {
	cc *grpc.ClientConn

	mu      sync.Mutex
	methods map[string]*methodTypes
}

// methodTypes are the message types of one method.
type methodTypes struct {
	in, out protoreflect.MessageDescriptor
}

// NewConn returns a Conn for target (a bare host:port, grpc:// or grpcs://).
// It never fails because the server is unreachable, only for a bad target.
func NewConn(target string, insecureSkipVerify bool) (*Conn, error) {
	addr, creds, err := parseAddr(target, insecureSkipVerify)
	if err != nil {
		return nil, fmt.Errorf("grpc: %w", err)
	}
	cc, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("grpc: %w", err)
	}
	return &Conn{cc: cc, methods: map[string]*methodTypes{}}, nil
}

// Close closes the connection.
func (c *Conn) Close() error { return c.cc.Close() }

// Call is one unary call from a script.
type Call struct {
	// Method is the full method, "/pkg.Service/Method" (the leading slash
	// is optional).
	Method string
	// Body is the request: JSON text, or base64 of the encoded message when
	// Encoding is "base64". Empty JSON means an empty message.
	Body []byte
	// Encoding is "json" (the default) or "base64".
	Encoding string
	// Headers are sent as gRPC metadata.
	Headers map[string]string
	// Timeout bounds the whole call, reflection included. Zero means none.
	Timeout time.Duration
}

// Reply is what a call got back. A call the server answered with an error
// status, or that failed on the network, is a Reply with Err set, not a Go
// error: a script is expected to look at it.
type Reply struct {
	// Code is the gRPC status code (0, OK, when the call worked).
	Code codes.Code
	// Body is the reply: JSON text, or base64 when Encoding was "base64".
	// It is empty when the call failed.
	Body          []byte
	Err           error
	BytesSent     int64
	BytesReceived int64
}

// Do makes the call. It returns an error only when the call is set up
// wrongly: a bad method name, an unknown method, a streaming method, a body
// that is not valid for the method's message, or a server that offers no
// reflection while the body is JSON. Everything the network or the server
// does is in the Reply.
func (c *Conn) Do(ctx context.Context, call Call) (Reply, error) {
	if call.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, call.Timeout)
		defer cancel()
	}
	service, method, err := splitMethod(call.Method)
	if err != nil {
		return Reply{}, err
	}
	full := "/" + service + "/" + method

	var req []byte
	var types *methodTypes
	switch call.Encoding {
	case "", EncodingJSON:
		types, err = c.types(ctx, service, method)
		if err != nil {
			var failed *callFailure
			if errors.As(err, &failed) {
				return Reply{Code: failed.code, Err: failed.err}, nil
			}
			return Reply{}, err
		}
		msg := dynamicpb.NewMessage(types.in)
		body := strings.TrimSpace(string(call.Body))
		if body != "" {
			if err := (protojson.UnmarshalOptions{}).Unmarshal([]byte(body), msg); err != nil {
				return Reply{}, fmt.Errorf("grpc: body is not a valid %s as JSON: %w", types.in.FullName(), err)
			}
		}
		req, err = proto.Marshal(msg)
		if err != nil {
			return Reply{}, fmt.Errorf("grpc: encoding the request: %w", err)
		}
	case EncodingBase64:
		req, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(call.Body)))
		if err != nil {
			return Reply{}, fmt.Errorf("grpc: body is not valid base64: %w", err)
		}
	default:
		return Reply{}, fmt.Errorf("grpc: encoding %q: want json or base64", call.Encoding)
	}

	if len(call.Headers) > 0 {
		ctx = metadata.NewOutgoingContext(ctx, metadata.New(call.Headers))
	}
	var resp []byte
	err = c.cc.Invoke(ctx, full, req, &resp, grpc.ForceCodec(rawCodec{}), grpc.CallContentSubtype("proto"))
	if err != nil {
		st, _ := status.FromError(err)
		return Reply{Code: st.Code(), Err: err, BytesSent: int64(len(req))}, nil
	}
	out := Reply{BytesSent: int64(len(req)), BytesReceived: int64(len(resp))}
	if types == nil {
		out.Body = []byte(base64.StdEncoding.EncodeToString(resp))
		return out, nil
	}
	msg := dynamicpb.NewMessage(types.out)
	if err := proto.Unmarshal(resp, msg); err != nil {
		return Reply{}, fmt.Errorf("grpc: the reply is not a valid %s: %w", types.out.FullName(), err)
	}
	out.Body, err = protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(msg)
	if err != nil {
		return Reply{}, fmt.Errorf("grpc: writing the reply as JSON: %w", err)
	}
	return out, nil
}

// callFailure is a failure of the network or the server while the call was
// still being set up (reflection). The script sees it as a failed call, not
// as a mistake of its own.
type callFailure struct {
	code codes.Code
	err  error
}

func (f *callFailure) Error() string { return f.err.Error() }
func (f *callFailure) Unwrap() error { return f.err }

// types returns the message types of a method, asking the server the first
// time.
func (c *Conn) types(ctx context.Context, service, method string) (*methodTypes, error) {
	key := service + "/" + method
	c.mu.Lock()
	t, ok := c.methods[key]
	c.mu.Unlock()
	if ok {
		return t, nil
	}
	files, err := fetchFiles(ctx, c.cc, service)
	if err != nil {
		st, _ := status.FromError(err)
		switch st.Code() {
		case codes.Unimplemented:
			return nil, fmt.Errorf("grpc: the server does not offer reflection, so a JSON body cannot be encoded. Pass encoding: \"base64\" with the encoded message")
		case codes.NotFound:
			return nil, fmt.Errorf("grpc: the server does not know the service %s", service)
		}
		return nil, &callFailure{code: st.Code(), err: err}
	}
	d, err := files.FindDescriptorByName(protoreflect.FullName(service))
	if err != nil {
		return nil, fmt.Errorf("grpc: the server does not know the service %s", service)
	}
	sd, ok := d.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("grpc: %s is not a service", service)
	}
	md := sd.Methods().ByName(protoreflect.Name(method))
	if md == nil {
		return nil, fmt.Errorf("grpc: the service %s has no method %s", service, method)
	}
	if md.IsStreamingClient() || md.IsStreamingServer() {
		return nil, fmt.Errorf("grpc: %s/%s is a streaming method, and only unary methods can be called from a script", service, method)
	}
	t = &methodTypes{in: md.Input(), out: md.Output()}
	c.mu.Lock()
	c.methods[key] = t
	c.mu.Unlock()
	return t, nil
}

// splitMethod turns "/pkg.Service/Method" (or without the slash) into its
// two parts.
func splitMethod(m string) (service, method string, err error) {
	m = strings.TrimPrefix(strings.TrimSpace(m), "/")
	i := strings.LastIndex(m, "/")
	if i <= 0 || i == len(m)-1 {
		return "", "", fmt.Errorf("grpc: method %q: want /package.Service/Method", m)
	}
	return m[:i], m[i+1:], nil
}

// ResultOf turns a Reply into a protocol.Result.
func ResultOf(r Reply) protocol.Result {
	return protocol.Result{
		Success:       r.Err == nil,
		StatusCode:    int(r.Code),
		BytesSent:     r.BytesSent,
		BytesReceived: r.BytesReceived,
		Err:           r.Err,
	}
}
