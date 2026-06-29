//go:build windows

package main

import "syscall"

// detachSysProcAttr is a no-op on Windows; the child still starts, just without
// the unix session-detach. Traffic Control targets macOS and Linux.
func detachSysProcAttr() *syscall.SysProcAttr {
	return nil
}
