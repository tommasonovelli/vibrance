//go:build linux

package media

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// confine makes cmd, before it starts, a tool that leaves nothing behind
// (DESIGN.md T2):
//
//   - it is the leader of a process group of its own, so that the tool and
//     anything it started are killed together;
//   - the kernel kills it (SIGKILL) if the server dies, however it dies:
//     a killed server leaves no tool running;
//   - when the context of cmd ends, the whole group is killed, not only
//     the tool.
//
// Vibrance runs on Linux only (§3.5): there is no other implementation.
func confine(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	cmd.Cancel = func() error {
		// The group id is the pid of the tool, its leader.
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			// Nothing is left of the group: the tool ended by itself.
			return os.ErrProcessDone
		}
		return err
	}
}
