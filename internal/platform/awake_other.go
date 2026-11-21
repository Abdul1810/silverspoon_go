//go:build !windows

// Package platform holds OS-specific helpers.
package platform

// PreventSleep is a no-op off Windows.
func PreventSleep() bool { return false }

// AllowSleep is a no-op off Windows.
func AllowSleep() bool { return false }
