//go:build windows

// Package platform holds OS-specific helpers.
package platform

import (
	"runtime"
	"syscall"
)

const (
	esContinuous      = 0x80000000
	esSystemRequired  = 0x00000001
	esAwaymodeRequire = 0x00000040
)

var (
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procSetThreadExecutionState = kernel32.NewProc("SetThreadExecutionState")
	awakeReq                    = make(chan uint32)
)

// SetThreadExecutionState applies to the calling thread, so the requests
// run on one goroutine locked to its OS thread for the life of the app.
func init() {
	go func() {
		runtime.LockOSThread()
		for flags := range awakeReq {
			procSetThreadExecutionState.Call(uintptr(flags))
		}
	}()
}

// PreventSleep keeps the machine awake while downloading.
func PreventSleep() bool {
	awakeReq <- esContinuous | esSystemRequired | esAwaymodeRequire
	return true
}

// AllowSleep releases the keep-awake request.
func AllowSleep() bool {
	awakeReq <- esContinuous
	return true
}
