//go:build !windows

package segmented

import "os"

// makeSparse is a no-op: other OSes leave holes on their own.
func makeSparse(*os.File) {}
