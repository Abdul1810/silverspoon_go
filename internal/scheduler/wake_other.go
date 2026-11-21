//go:build !windows

package scheduler

// RegisterWakeTask is Windows-only.
func RegisterWakeTask(Schedule, string, string) (bool, string) {
	return false, "Wake timer is Windows-only; using keep-awake instead."
}

// UnregisterWakeTask is a no-op off Windows.
func UnregisterWakeTask() (bool, string) { return true, "" }
