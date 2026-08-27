//go:build windows

package executor

import "os/exec"

// setProcessGroup is a no-op: Windows has no session or process group to put
// the tool in.
func setProcessGroup(_ *exec.Cmd) {}

// killGroup terminates the tool itself. Descendants survive, since reaching
// them would need a job object rrev does not create.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
