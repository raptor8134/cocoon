//go:build !js

package internal

// WebGPUAvailable reports WebGPU availability for native/desktop builds
// (always true; the actual adapter/device probing happens elsewhere).
func WebGPUAvailable() bool { return true }

// WebGPUReady reports WebGPU readiness for native/desktop builds by
// immediately invoking the callback with ok=true.
func WebGPUReady(cb func(ok bool)) { cb(true) }

