//go:build !js

package main

// downloadCurrent is only implemented for the web build. On desktop it
// is a no-op that returns an error so callers can hide or disable the
// Download option.
func downloadCurrent(state *AppState) error {
	return nil
}

