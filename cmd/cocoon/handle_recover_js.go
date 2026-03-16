//go:build js

package main

import (
	"log"
	"runtime/debug"

	"cogentcore.org/core/system"
)

func init() {
	println("cocoon: installing web-safe system.HandleRecover override")

	// Override system.HandleRecover so that on WASM we keep the console logging
	// behavior of HandleRecoverBase, but avoid AppDataDir/os.WriteFile crash-log
	// writes (no desktop filesystem in the browser).
	system.HandleRecover = func(r any) {
		if r == nil {
			return
		}

		stack := string(debug.Stack())

		log.Println("panic:", r)
		log.Println("")
		log.Println("----- START OF STACK TRACE: -----")
		log.Println(stack)
		log.Println("----- END OF STACK TRACE -----")

		// Preserve default crash semantics after logging.
		system.HandleRecoverPanic(r)
	}
}

