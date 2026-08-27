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

// processGroup is the tool and every process it spawns, held as one unit so a
// cancelled review leaves no sub-agent behind.
type processGroup struct{ cmd *exec.Cmd }

// newProcessGroup puts the tool in its own session, so every sub-agent it
// spawns shares one process group that can be terminated as a unit. A new
// session also detaches the tool from rrev's controlling terminal, so a
// descendant reading the terminal cannot stop the group with SIGTTIN. Nothing
// here can fail; the error exists for the Windows implementation.
func newProcessGroup(cmd *exec.Cmd) (*processGroup, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return &processGroup{cmd: cmd}, nil
}

// started has nothing to do: the session is established by the fork itself.
func (g *processGroup) started() error { return nil }

// kill terminates the tool and everything it spawned, asking first and
// insisting after the grace period.
func (g *processGroup) kill() {
	if g.cmd.Process == nil || g.cmd.Process.Pid <= 0 {
		return
	}
	group := -g.cmd.Process.Pid
	if err := syscall.Kill(group, syscall.SIGTERM); err == syscall.ESRCH {
		return
	}
	time.Sleep(gracePeriod)
	_ = syscall.Kill(group, syscall.SIGKILL)
}

// close holds no resource to release.
func (g *processGroup) close() {}
