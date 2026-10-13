//go:build js && wasm

// WASM entry point for the browser surfaces (PWA, extension).
//
// Exposes one global:
//
//	taprootCore.process(requestJSON) → responseJSON
//
// On error the returned JSON is {"error": "..."}. Build with:
//
//	make core-wasm
//
// The target leaves build paths and VCS data out of the committed WASM,
// vendors the matching wasm_exec.js and rewrites vendor/core/checksums.txt.
package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/storymode7/taproot/core"
)

func main() {
	process := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) != 1 {
			return errJSON("taprootCore.process takes exactly one JSON string argument")
		}
		out, err := core.ProcessJSON(args[0].String())
		if err != nil {
			return errJSON(err.Error())
		}
		return out
	})
	js.Global().Set("taprootCore", map[string]any{})
	js.Global().Get("taprootCore").Set("process", process)

	// Keep the Go runtime alive; the page owns the module's lifetime.
	select {}
}

func errJSON(msg string) string {
	out, _ := json.Marshal(map[string]string{"error": msg})
	return string(out)
}
