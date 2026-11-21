//go:build windows

package extraction

import (
	"os/exec"
	"syscall"
)

// hideWindow stops the extractor from opening a console window.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
}
