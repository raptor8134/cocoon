//go:build js

package main

import (
	"fmt"
	"syscall/js"
)

// downloadCurrent triggers a browser download of the current editor JSON.
func downloadCurrent(state *AppState) error {
	if state == nil || state.Lines == nil {
		return fmt.Errorf("no document to download")
	}
	name := state.CurrentName
	if name == "" {
		name = "untitled.json"
	}
	data := []byte(state.Lines.String())

	// Create a Uint8Array from the Go byte slice.
	uint8Array := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(uint8Array, data)

	blob := js.Global().Get("Blob").New([]interface{}{uint8Array}, map[string]any{
		"type": "application/json",
	})
	url := js.Global().Get("URL").Call("createObjectURL", blob)

	doc := js.Global().Get("document")
	a := doc.Call("createElement", "a")
	a.Set("href", url)
	a.Set("download", name)
	doc.Get("body").Call("appendChild", a)
	a.Call("click")
	a.Call("remove")
	js.Global().Get("URL").Call("revokeObjectURL", url)
	return nil
}

