//go:build js && wasm

// WASM entry point for the browser surfaces (PWA, extension).
//
// Exposes one global:
//
//	astraCore.process(requestJSON) → responseJSON
//
// On error the returned JSON is {"error": "..."}. Build with:
//
//	GOOS=js GOARCH=wasm go build -o vendor/core/astra-core.wasm ./core/wasmmain
//
// (see the core-wasm Makefile target, which also vendors the matching
// wasm_exec.js from the Go toolchain).
package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/storymode7/astra/core"
)

func main() {
	process := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) != 1 {
			return errJSON("astraCore.process takes exactly one JSON string argument")
		}
		out, err := core.ProcessJSON(args[0].String())
		if err != nil {
			return errJSON(err.Error())
		}
		return out
	})
	js.Global().Set("astraCore", map[string]any{})
	js.Global().Get("astraCore").Set("process", process)

	// Keep the Go runtime alive; the page owns the module's lifetime.
	select {}
}

func errJSON(msg string) string {
	out, _ := json.Marshal(map[string]string{"error": msg})
	return string(out)
}
