//go:build windows

package scheduler

import (
	"context"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const createNoWindow = 0x08000000

func runPowerShell(command string) (int, string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow, HideWindow: true}
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code, err = ee.ExitCode(), nil
	}
	return code, out.String(), errb.String(), err
}

// RegisterWakeTask registers a task that wakes the PC and launches the app at
// the window start. No admin rights needed for a current-user task.
func RegisterWakeTask(s Schedule, executable, arguments string) (bool, string) {
	code, out, errs, err := runPowerShell(RegisterWakeCommand(s, executable, arguments, time.Now()))
	if err != nil {
		return false, err.Error()
	}
	if code == 0 {
		return true, "Wake timer registered."
	}
	msg := errs
	if msg == "" {
		msg = out
	}
	if msg == "" {
		msg = "Failed to register wake task."
	}
	return false, strings.TrimSpace(msg)
}

// UnregisterWakeTask removes the wake task, if any.
func UnregisterWakeTask() (bool, string) {
	if _, _, _, err := runPowerShell(UnregisterWakeCommand()); err != nil {
		return false, err.Error()
	}
	return true, ""
}
