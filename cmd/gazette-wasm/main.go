//go:build js && wasm

// The WebAssembly build of The RPC Gazette. It runs the real server and
// client in the browser, over an in-memory connection.
//
// It sets a global object "gazette" with functions that return promises
// of JSON text:
//
//	gazette.exec(line) → {"ok": true, "entry": {...}, "state": {...}}, or "ok": false with "error"
//	gazette.state()    → {"ok": true, "state": {...}}
//	gazette.reset()    → {"ok": true, "state": {...}}
//
// It dispatches the "gazette-ready" event when the keys are ready.
package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/rahelmartim/go-rpc-rsa-crud/gazette"
)

type reply struct {
	OK    bool           `json:"ok"`
	Error string         `json:"error,omitempty"`
	Entry *gazette.Entry `json:"entry,omitempty"`
	State gazette.State  `json:"state"`
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
	api.Set("exec", js.FuncOf(func(_ js.Value, args []js.Value) any {
		line := ""
		if len(args) > 0 {
			line = args[0].String()
		}
		return promise(func() string {
			entry, state, err := engine.Exec(line)
			return encode(reply{Entry: &entry, State: state}, err)
		})
	}))
	api.Set("state", js.FuncOf(func(js.Value, []js.Value) any {
		return promise(func() string { return encode(reply{State: engine.State()}, nil) })
	}))
	api.Set("reset", js.FuncOf(func(js.Value, []js.Value) any {
		return promise(func() string {
			state, err := engine.Reset()
			return encode(reply{State: state}, err)
		})
	}))
	js.Global().Set("gazette", api)
	js.Global().Call("dispatchEvent", js.Global().Get("Event").New("gazette-ready"))
	select {}
}
