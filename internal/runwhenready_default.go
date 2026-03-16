//go:build !js

package internal

// RunWhenReady schedules f to run once the UI is ready.
// On desktop/native builds the main window/composer is already available,
// so we just invoke f immediately.
func RunWhenReady(f func()) {
	f()
}
