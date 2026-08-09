//go:build windows

// internal/confinement/confine_windows.go
// Purpose: Windows has no supported kernel write-confinement backend in this
// program; strict mode remains advisory with a visible warning.
package confinement

func detectBackend() Backend { return BackendNone }

func wrapCommand(argv []string, p Profile) ([]string, bool) { return nil, false }
