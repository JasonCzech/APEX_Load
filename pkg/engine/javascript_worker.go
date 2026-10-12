package engine

import (
	"encoding/json"
	"os"
	"time"

	"github.com/dop251/goja"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func init() {
	if len(os.Args) != 2 || os.Args[1] != nativeJSWorkerArgument {
		return
	}
	if nativeJSApplyLimits() != nil {
		os.Exit(2)
	}
	if runNativeJSWorker() != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func runNativeJSWorker() error {
	encoder := json.NewEncoder(os.Stdout)
	if encoder.Encode(nativeJSFrame{Kind: "ready"}) != nil {
		return nativeError("worker_protocol_failed")
	}
	scanner := nativeJSScanner(os.Stdin)
	if !scanner.Scan() {
		return nativeError("worker_protocol_failed")
	}
	var input nativeJSFrame
	if json.Unmarshal(scanner.Bytes(), &input) != nil || input.Kind != "execute" || len(input.Source) > dsl.NativeJavaScriptSourceBytes || len(input.Inputs) > dsl.NativeJavaScriptValueBytes {
		return nativeError("worker_input_invalid")
	}
	vm := goja.New()
	vm.SetMaxCallStackSize(128)
	// Goja exposes ECMAScript only; no filesystem, environment, process,
	// network, modules, timers or Go reflection objects are installed.
	stringify, ok := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("stringify"))
	if !ok {
		return nativeError("worker_unavailable")
	}
	parse, ok := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("parse"))
	if !ok {
		return nativeError("worker_unavailable")
	}
	inputValue, err := parse(goja.Undefined(), vm.ToValue(string(input.Inputs)))
	if err != nil {
		return nativeError("worker_input_invalid")
	}
	timer := time.AfterFunc(time.Second, func() { vm.Interrupt("native JavaScript execution limit") })
	defer timer.Stop()
	var sequence uint64
	call := func(value goja.Value) goja.Value {
		encoded, err := stringify(goja.Undefined(), value)
		if err != nil {
			panic(vm.NewTypeError("request_invalid"))
		}
		data := []byte(encoded.String())
		if _, err := protocol.DecodeNativeDataRequest(data); err != nil {
			panic(vm.NewTypeError("request_invalid"))
		}
		sequence++
		if sequence > 1024 {
			panic(vm.NewTypeError("call_capacity"))
		}
		// The OS CPU limit remains active while wall-clock interpreter timing
		// pauses during a legitimate synchronous live-table wait.
		timer.Stop()
		if encoder.Encode(nativeJSFrame{Kind: "call", Sequence: sequence, Request: data}) != nil || !scanner.Scan() {
			panic(vm.NewTypeError("service_unavailable"))
		}
		timer.Reset(time.Second)
		var reply nativeJSFrame
		if json.Unmarshal(scanner.Bytes(), &reply) != nil || reply.Kind != "reply" || reply.Sequence != sequence {
			panic(vm.NewTypeError("service_unavailable"))
		}
		if reply.Error != nil {
			exception := vm.NewTypeError(reply.Error.Code)
			_ = exception.Set("code", reply.Error.Code)
			_ = exception.Set("outcome_unknown", reply.Error.OutcomeUnknown)
			panic(exception)
		}
		if reply.Result == nil {
			panic(vm.NewTypeError("service_unavailable"))
		}
		result, err := json.Marshal(reply.Result)
		if err != nil {
			panic(vm.NewTypeError("service_unavailable"))
		}
		parsed, err := parse(goja.Undefined(), vm.ToValue(string(result)))
		if err != nil {
			panic(vm.NewTypeError("service_unavailable"))
		}
		return parsed
	}
	_ = vm.Set("__nativeDataCall", call)
	api, err := vm.RunString(`(function(call){
 "use strict";
 const operations = ["read","next","push","pop","lease","renew","ack","release","update","increment","clear","rotate","create_column","read_column"];
 function table(alias) {
   if (typeof alias !== "string") throw new TypeError("alias_invalid");
   const methods = Object.create(null);
   for (const operation of operations) {
     methods[operation] = Object.freeze(function(options) {
       if (operation === "push") options = {values: options};
       if (operation === "ack" || operation === "release") options = {lease_ref: options};
       if (options === undefined) options = {};
       if (!options || typeof options !== "object" || Array.isArray(options)) throw new TypeError("request_invalid");
       return call(Object.assign({}, options, {alias:alias, operation:operation}));
     });
   }
   return Object.freeze(methods);
 }
 return Object.freeze({data:Object.freeze({call:Object.freeze(function(request){return call(request);}),table:Object.freeze(table)})});
})(__nativeDataCall)`)
	_ = vm.GlobalObject().Delete("__nativeDataCall")
	if err != nil {
		return nativeError("worker_unavailable")
	}
	function, err := vm.RunString("(function(input,apex){\n\"use strict\";\n" + input.Source + "\n})")
	var output goja.Value
	if err == nil {
		if fn, ok := goja.AssertFunction(function); ok {
			output, err = fn(goja.Undefined(), inputValue, api)
		} else {
			err = nativeError("source_invalid")
		}
	}
	if err != nil {
		_ = encoder.Encode(nativeJSResultError(err))
		return err
	}
	if output == nil || goja.IsUndefined(output) {
		output = vm.NewObject()
	}
	serialized, err := stringify(goja.Undefined(), output)
	if err != nil {
		_ = encoder.Encode(nativeJSResultError(err))
		return err
	}
	data := []byte(serialized.String())
	if len(data) > dsl.NativeJavaScriptValueBytes || !json.Valid(data) {
		_ = encoder.Encode(nativeJSResultError(nativeError("output_invalid")))
		return nativeError("output_invalid")
	}
	return encoder.Encode(nativeJSFrame{Kind: "result", Output: data})
}
