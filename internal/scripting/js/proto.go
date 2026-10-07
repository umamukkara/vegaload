package js

import (
	"fmt"

	"github.com/dop251/goja"

	"github.com/vegaload/vegaload/internal/scripting/netapi"
)

// newProtoGlobals sets the tcp, udp, mqtt and kafka globals (FR-CLI-08, protocol
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
	out := map[string]*goja.Object{}
	for ns, funcs := range netapi.ProtoFunctions() {
		obj := vm.NewObject()
		for _, fn := range funcs {
			_ = obj.Set(fn, v.protoFunc(vm, ns+"."+fn))
		}
		out[ns] = obj
	}
	return out
}

// protoFunc wraps one protocol call as a JS function f(url[, options]).
func (v *VU) protoFunc(vm *goja.Runtime, name string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if v.ctx == nil {
			throw(vm, fmt.Errorf("js: %s called outside of an iteration", name))
		}
		if len(call.Arguments) < 1 || goja.IsUndefined(call.Arguments[0]) || goja.IsNull(call.Arguments[0]) {
			throw(vm, fmt.Errorf("js: %s(url[, options]) requires a url argument", name))
		}
		var args map[string]any
		if len(call.Arguments) >= 2 && !goja.IsUndefined(call.Arguments[1]) && !goja.IsNull(call.Arguments[1]) {
			if err := vm.ExportTo(call.Arguments[1], &args); err != nil {
				throw(vm, fmt.Errorf("js: %s: options must be an object like {body: \"...\"}: %w", name, err))
			}
		}
		pc, err := netapi.ProtoCallFromArgs(name, call.Arguments[0].String(), args)
		if err != nil {
			throw(vm, fmt.Errorf("js: %s: %w", name, err))
		}
		reply, err := v.proto.Call(v.ctx, name, pc)
		if err != nil {
			throw(vm, err)
		}
		return vm.ToValue(reply.Fields())
	}
}
