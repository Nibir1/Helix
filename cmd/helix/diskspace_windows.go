//go:build windows

package main

import "golang.org/x/sys/windows"

// freeBytes reports the space available to this user on the volume holding
// path, or false if it cannot be determined.
func freeBytes(path string) (uint64, bool) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, false
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return 0, false
	}
	// The first value is the space available to the CALLING user, which is what
	// a quota-limited account can actually use.
	return free, true
}
