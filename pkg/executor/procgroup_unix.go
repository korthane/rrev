//go:build !windows

package executor

import (
	"os/exec"
	"syscall"
	"time"
)

// gracePeriod is how long a terminated tool gets to exit on its own before it
// is killed outright.
const gracePeriod = 100 * time.Millisecond

// setProcessGroup puts the tool in its own session, so every sub-agent it
// spawns shares one process group that can be terminated as a unit. A new
// session also detaches the tool from rrev's controlling terminal, so a
// descendant reading the terminal cannot stop the group with SIGTTIN.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// killGroup terminates the tool and everything it spawned, asking first and
// insisting after the grace period.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil || cmd.Process.Pid <= 0 {
		return
	}
	group := -cmd.Process.Pid
	if err := syscall.Kill(group, syscall.SIGTERM); err == syscall.ESRCH {
		return
	}
	time.Sleep(gracePeriod)
	_ = syscall.Kill(group, syscall.SIGKILL)
}
