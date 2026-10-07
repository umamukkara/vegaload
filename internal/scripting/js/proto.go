package js

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/vegaload/vegaload/internal/scripting/netapi"
)

// newProtoGlobals sets the tcp, udp and mqtt globals (FR-CLI-08, protocol
// scripting). Each function is one call that does one job and returns an
// object a script can read:
//
//	const r = tcp.send("tcp://localhost:6379", {body: "PING\r\n", until: "\r\n"});
//	if (r.ok) console.log(r.body);
//
//	mqtt.publish("mqtt://localhost", {topic: "t/1", body: "hi"});
//	const k = kafka.produce("kafka://localhost", {topic: "orders", key: "k", value: "v"});
//	k.records[0].partition, k.records[0].offset
//	const m = mqtt.subscribe("mqtt://localhost", {topic: "t/#", count: 2});
//	m.messages[0].topic, m.messages[0].body
//
// A call that fails on the network does not throw. It returns
// {ok: false, error: "..."}, so a script can check it and go on. A call that
// is set up wrongly (an unknown option, a missing topic) or whose host the
// safety check refuses does throw.
func (v *VU) newProtoGlobals(vm *goja.Runtime) map[string]*goja.Object {
	tcp := vm.NewObject()
	_ = tcp.Set("send", v.protoFunc(vm, "tcp.send", func(call netapi.ProtoCall) (*netapi.ProtoReply, error) {
		return v.proto.TCP(v.ctx, call)
	}))
	udp := vm.NewObject()
	_ = udp.Set("send", v.protoFunc(vm, "udp.send", func(call netapi.ProtoCall) (*netapi.ProtoReply, error) {
		return v.proto.UDP(v.ctx, call)
	}))
	mq := vm.NewObject()
	for _, mode := range []string{"publish", "subscribe", "roundtrip"} {
		mode := mode
		_ = mq.Set(mode, v.protoFunc(vm, "mqtt."+mode, func(call netapi.ProtoCall) (*netapi.ProtoReply, error) {
			return v.proto.MQTT(v.ctx, mode, call)
		}))
	}
	kf := vm.NewObject()
	for _, mode := range []string{"produce", "consume", "roundtrip", "admin"} {
		mode := mode
		_ = kf.Set(mode, v.protoFunc(vm, "kafka."+mode, func(call netapi.ProtoCall) (*netapi.ProtoReply, error) {
			return v.proto.Kafka(v.ctx, mode, call)
		}))
	}
	return map[string]*goja.Object{"tcp": tcp, "udp": udp, "mqtt": mq, "kafka": kf}
}

// protoFunc wraps one protocol call as a JS function f(url[, options]).
func (v *VU) protoFunc(vm *goja.Runtime, name string, run func(netapi.ProtoCall) (*netapi.ProtoReply, error)) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if v.ctx == nil {
			throw(vm, fmt.Errorf("js: %s called outside of an iteration", name))
		}
		if len(call.Arguments) < 1 || goja.IsUndefined(call.Arguments[0]) || goja.IsNull(call.Arguments[0]) {
			throw(vm, fmt.Errorf("js: %s(url[, options]) requires a url argument", name))
		}
		pc, err := parseProtoCall(vm, call, strings.HasPrefix(name, "kafka."))
		if err != nil {
			throw(vm, fmt.Errorf("js: %s: %w", name, err))
		}
		reply, err := run(pc)
		if err != nil {
			throw(vm, err)
		}
		return wrapProtoReply(vm, reply)
	}
}

// parseProtoCall reads (url, options). body, insecure, timeout and
// password are the script's own keys. Every other key is passed on to the
// driver as an option, and the driver rejects the ones it does not know.
func parseProtoCall(vm *goja.Runtime, call goja.FunctionCall, valueIsBody bool) (netapi.ProtoCall, error) {
	pc := netapi.ProtoCall{URL: call.Arguments[0].String()}
	if len(call.Arguments) < 2 || goja.IsUndefined(call.Arguments[1]) || goja.IsNull(call.Arguments[1]) {
		return pc, nil
	}
	var raw map[string]any
	if err := vm.ExportTo(call.Arguments[1], &raw); err != nil {
		return pc, fmt.Errorf("options must be an object like {body: \"...\"}: %w", err)
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		val := raw[k]
		if val == nil {
			continue
		}
		switch k {
		case "body", "value":
			if k == "value" && !valueIsBody {
				return pc, errors.New("unknown option value (use body)")
			}
			if pc.Body != nil {
				return pc, errors.New("give body or value, not both")
			}
			s, err := optionText(k, val)
			if err != nil {
				return pc, err
			}
			pc.Body = []byte(s)
		case "insecure":
			b, ok := val.(bool)
			if !ok {
				return pc, errors.New("option insecure must be true or false")
			}
			pc.Insecure = b
		case "timeout":
			d, err := optionTimeout(val)
			if err != nil {
				return pc, err
			}
			pc.Timeout = d
		case "password":
			s, err := optionText(k, val)
			if err != nil {
				return pc, err
			}
			pc.Password = &s
		default:
			s, err := optionText(k, val)
			if err != nil {
				return pc, err
			}
			if pc.Options == nil {
				pc.Options = map[string]string{}
			}
			pc.Options[k] = s
		}
	}
	return pc, nil
}

// optionText turns a script value into the text a driver option holds.
func optionText(key string, val any) (string, error) {
	switch x := val.(type) {
	case string:
		return x, nil
	case bool:
		return strconv.FormatBool(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10), nil
		}
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	}
	return "", fmt.Errorf("option %s must be a string, number or boolean", key)
}

// optionTimeout reads timeout as milliseconds (a number) or as a duration
// text such as "2s".
func optionTimeout(val any) (time.Duration, error) {
	var d time.Duration
	switch x := val.(type) {
	case int64:
		d = time.Duration(x) * time.Millisecond
	case float64:
		d = time.Duration(x * float64(time.Millisecond))
	case string:
		var err error
		if d, err = time.ParseDuration(strings.TrimSpace(x)); err != nil {
			return 0, fmt.Errorf("option timeout=%q: want milliseconds or a duration such as 2s", x)
		}
	default:
		return 0, errors.New("option timeout must be milliseconds or a duration such as \"2s\"")
	}
	if d <= 0 {
		return 0, errors.New("option timeout must be more than zero")
	}
	return d, nil
}

// wrapProtoReply builds {ok, error, bytesSent, bytesReceived, body,
// messages}, and for kafka also {records, text}. body is the reply of tcp and udp. messages is for mqtt, each
// {topic, body}. error is "" when the call worked.
func wrapProtoReply(vm *goja.Runtime, r *netapi.ProtoReply) *goja.Object {
	o := vm.NewObject()
	_ = o.Set("ok", r.OK)
	_ = o.Set("error", r.Error)
	_ = o.Set("bytesSent", r.BytesSent)
	_ = o.Set("bytesReceived", r.BytesReceived)
	_ = o.Set("body", string(r.Body))
	msgs := make([]any, 0, len(r.Messages))
	for _, m := range r.Messages {
		mo := vm.NewObject()
		_ = mo.Set("topic", m.Topic)
		_ = mo.Set("body", string(m.Body))
		msgs = append(msgs, mo)
	}
	_ = o.Set("messages", vm.NewArray(msgs...))
	if r.IsKafka {
		recs := make([]any, 0, len(r.Records))
		for _, rec := range r.Records {
			ro := vm.NewObject()
			_ = ro.Set("topic", rec.Topic)
			_ = ro.Set("partition", rec.Partition)
			_ = ro.Set("offset", rec.Offset)
			_ = ro.Set("key", string(rec.Key))
			_ = ro.Set("value", string(rec.Value))
			recs = append(recs, ro)
		}
		_ = o.Set("records", vm.NewArray(recs...))
		_ = o.Set("text", r.Text)
	}
	return o
}
