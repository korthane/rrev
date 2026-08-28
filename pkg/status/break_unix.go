//go:build !windows

package status

import (
	"os"
	"syscall"
)

// BreakSignal reports the signal that ends only the current review loop, and
// the key that sends it. SIGQUIT is the one terminal key left that is neither
// the abort (SIGINT) nor a job-control signal, and rrev handles it instead of
// dumping core.
func BreakSignal() (os.Signal, string, bool) { return syscall.SIGQUIT, `Ctrl+\`, true }
