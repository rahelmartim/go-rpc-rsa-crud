//go:build js && wasm

// The WebAssembly build of The RPC Gazette. It runs the real server and
// client in the browser, over an in-memory connection.
//
// It sets a global object "gazette" with two functions that return
// promises of JSON text:
//
//	gazette.do(action, argsJSON) → {"ok": true, "state": {...}} or {"ok": false, "error": "...", "state": {...}}
//	gazette.exec(line)           → the same, with "output"
//
// It dispatches the "gazette-ready" event when the keys are ready.
package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/rahelmartim/go-rpc-rsa-crud/gazette"
)

type reply struct {
	OK     bool          `json:"ok"`
	Error  string        `json:"error,omitempty"`
	Output string        `json:"output,omitempty"`
	State  gazette.State `json:"state"`
}

func encode(r reply, err error) string {
	if err != nil {
		r.Error = err.Error()
	} else {
		r.OK = true
	}
	data, _ := json.Marshal(r)
	return string(data)
}

// promise runs fn in a goroutine, so that the page does not wait.
func promise(fn func() string) js.Value {
	executor := js.FuncOf(func(_ js.Value, args []js.Value) any {
		resolve := args[0]
		go func() { resolve.Invoke(fn()) }()
		return nil
	})
	defer executor.Release()
	return js.Global().Get("Promise").New(executor)
}

func main() {
	engine, err := gazette.New()
	if err != nil {
		js.Global().Get("console").Call("error", "gazette: "+err.Error())
		return
	}
	api := js.Global().Get("Object").New()
	api.Set("do", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) < 1 {
			return nil
		}
		action, raw := args[0].String(), "{}"
		if len(args) > 1 {
			raw = args[1].String()
		}
		return promise(func() string {
			var in map[string]string
			if err := json.Unmarshal([]byte(raw), &in); err != nil {
				return encode(reply{State: engine.State()}, err)
			}
			state, err := engine.Do(action, in)
			return encode(reply{State: state}, err)
		})
	}))
	api.Set("exec", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) < 1 {
			return nil
		}
		line := args[0].String()
		return promise(func() string {
			out, state, err := engine.Exec(line)
			return encode(reply{Output: out, State: state}, err)
		})
	}))
	js.Global().Set("gazette", api)
	js.Global().Call("dispatchEvent", js.Global().Get("Event").New("gazette-ready"))
	select {}
}
