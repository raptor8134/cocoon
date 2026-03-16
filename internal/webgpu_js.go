//go:build js

package internal

import "syscall/js"

// WebGPUAvailable reports WebGPU availability for js/wasm builds by
// checking for navigator.gpu in the browser.
// This is a quick preflight check; actual adapter/device creation may still fail.
func WebGPUAvailable() bool {
	nav := js.Global().Get("navigator")
	if !nav.Truthy() {
		return false
	}
	return nav.Get("gpu").Truthy()
}

// WebGPUReady attempts to request a WebGPU adapter and calls cb with the result.
// If WebGPU is exposed but unavailable (e.g. disabled/blocked), cb(false) is called.
func WebGPUReady(cb func(ok bool)) {
	nav := js.Global().Get("navigator")
	if !nav.Truthy() {
		cb(false)
		return
	}
	gpu := nav.Get("gpu")
	if !gpu.Truthy() {
		cb(false)
		return
	}

	// Note: requestAdapter can succeed even when requestDevice later fails.
	// We probe both so we can give a precise failure point.
	p := gpu.Call("requestAdapter", map[string]any{
		"powerPreference": "high-performance",
	})
	if !p.Truthy() {
		cb(false)
		return
	}

	var thenFn js.Func
	var catchFn js.Func
	var devThenFn js.Func
	var devCatchFn js.Func

	thenFn = js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) == 0 || !args[0].Truthy() {
			println("WebGPUReady: requestAdapter resolved to null/undefined")
			cb(false)
			thenFn.Release()
			catchFn.Release()
			return nil
		}
		adapter := args[0]
		println("WebGPUReady: adapter acquired; requesting device")

		dp := adapter.Call("requestDevice")
		if !dp.Truthy() {
			println("WebGPUReady: requestDevice returned falsy")
			cb(false)
			thenFn.Release()
			catchFn.Release()
			return nil
		}

		devThenFn = js.FuncOf(func(this js.Value, dargs []js.Value) any {
			if len(dargs) == 0 || !dargs[0].Truthy() {
				println("WebGPUReady: requestDevice resolved to null/undefined")
				cb(false)
			} else {
				println("WebGPUReady: device acquired (ok)")
				cb(true)
			}
			// release all funcs
			devThenFn.Release()
			devCatchFn.Release()
			thenFn.Release()
			catchFn.Release()
			return nil
		})
		devCatchFn = js.FuncOf(func(this js.Value, cargs []js.Value) any {
			cb(false)
			devThenFn.Release()
			devCatchFn.Release()
			thenFn.Release()
			catchFn.Release()
			return nil
		})

		dp.Call("then", devThenFn).Call("catch", devCatchFn)
		return nil
	})
	catchFn = js.FuncOf(func(this js.Value, args []js.Value) any {
		cb(false)
		thenFn.Release()
		catchFn.Release()
		return nil
	})

	p.Call("then", thenFn).Call("catch", catchFn)
}

