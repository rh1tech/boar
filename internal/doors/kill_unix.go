//go:build unix

package doors

import (
	"os/exec"
	"syscall"
)

// configureKill puts the door in its own process group so that ending it
// also ends anything it started (DOSBox, shell pipelines).
func configureKill(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
