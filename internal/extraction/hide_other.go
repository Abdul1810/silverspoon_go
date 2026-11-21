//go:build !windows

package extraction

import "os/exec"

func hideWindow(*exec.Cmd) {}
