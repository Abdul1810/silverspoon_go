//go:build windows

package segmented

import (
	"os"
	"syscall"
)

const fsctlSetSparse = 0x000900C4

// makeSparse marks an open file sparse (best effort; FAT/exFAT can't).
func makeSparse(f *os.File) {
	var returned uint32
	_ = syscall.DeviceIoControl(syscall.Handle(f.Fd()), fsctlSetSparse, nil, 0, nil, 0, &returned, nil)
}
