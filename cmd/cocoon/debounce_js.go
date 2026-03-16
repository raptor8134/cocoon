//go:build js

package main

import "syscall/js"

// scheduleDebouncedRecompute runs f once, 100ms after the last call.
// On web we use setTimeout so the callback runs on the browser main thread
// (avoiding nil dereference in Cogent Core's text styling when updating from a goroutine).
var debounceTimeoutID js.Value

func scheduleDebouncedRecompute(state *AppState, f func()) {
	if debounceTimeoutID.Type() != js.TypeUndefined && debounceTimeoutID.Type() != js.TypeNull {
		js.Global().Call("clearTimeout", debounceTimeoutID)
	}
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		f()
		debounceTimeoutID = js.Undefined()
		return nil
	})
	debounceTimeoutID = js.Global().Call("setTimeout", cb, 100)
	_ = state
}
