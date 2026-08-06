// internal/shell/resize_windows.go
// Purpose: Resize notification stub for Windows. The 1Hz width poll in the
// animation loop covers console resizes on this platform.
//go:build windows

package shell

// notifyResize is a no-op on Windows; returns a silent channel and stop func.
func notifyResize() (chan struct{}, func()) {
	return make(chan struct{}), func() {}
}
