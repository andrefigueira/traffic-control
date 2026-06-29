//go:build !windows

package main

import "syscall"

// detachSysProcAttr starts the child in its own session so it outlives the
// short-lived hook process that spawned it.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
