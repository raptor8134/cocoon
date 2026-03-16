//go:build js && wasm

package main

// Blank import to register the Cogent Core web driver
// when building for WebAssembly. Without this, there is
// no system driver for the GUI on the web, and the app
// will exit immediately when RunMainWindow is called.
import _ "cogentcore.org/core/system/driver/web"

