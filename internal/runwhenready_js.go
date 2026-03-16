//go:build js

package internal

import (
	"syscall/js"

	"cogentcore.org/core/core"
)

// RunWhenReady schedules f to run once the Cogent Core window/composer is
// available in a js/wasm build. We poll using requestAnimationFrame until
// core.TheApp.Window(0).Composer() is non-nil to avoid early panics.
func RunWhenReady(f func()) {
	var cb js.Func
	tries := 0
	cb = js.FuncOf(func(this js.Value, args []js.Value) any {
		tries++
		println("RunWhenReady: requestAnimationFrame tick, tries =", tries)
		if coreComposerReady() {
			println("RunWhenReady: coreComposerReady returned true, scheduling deferred callback on next RAF")
			// Defer f to the *next* animation frame after the composer is ready.
			// This approximates "after the first composer render", ensuring that
			// layout and the underlying drawing surface have settled before f
			// runs (e.g. to trigger an initial NeedsRender on webgpu).
			js.Global().Call("requestAnimationFrame", js.FuncOf(func(this js.Value, a []js.Value) any {
				println("RunWhenReady: deferred RAF firing, invoking callback")
				f()
				return nil
			}))
			cb.Release()
			return nil
		}
		js.Global().Call("requestAnimationFrame", cb)
		return nil
	})
	js.Global().Call("requestAnimationFrame", cb)
}

func coreComposerReady() (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	if core.TheApp == nil {
		return false
	}
	w := core.TheApp.Window(0)
	if w == nil {
		return false
	}
	return w.Composer() != nil
}
