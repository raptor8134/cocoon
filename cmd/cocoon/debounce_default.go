//go:build !js

package main

import (
	"time"

	"cogentcore.org/core/system"
)

func scheduleDebouncedRecompute(state *AppState, f func()) {
	if state.debounceTimer != nil {
		state.debounceTimer.Stop()
	}
	state.debounceTimer = time.AfterFunc(100*time.Millisecond, func() {
		system.TheApp.RunOnMain(func() {
			f()
			state.debounceTimer = nil
		})
	})
}
