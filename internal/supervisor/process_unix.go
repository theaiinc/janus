//go:build !windows

package supervisor

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureProcessGroup makes the supervised shell the leader of its own process group, so it
// and everything it spawns can be signalled together. Commands are wrapper scripts that
// background their real work (e.g. `cloudflared ... &`); signalling only the shell orphaned
// those children to launchd/init on every restart, and they piled up by the thousand.
func configureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func terminateProcessTree(process *os.Process) error {
	return signalProcessGroup(process, syscall.SIGTERM)
}

func killProcessTree(process *os.Process) error {
	return signalProcessGroup(process, syscall.SIGKILL)
}

func signalProcessGroup(process *os.Process, sig syscall.Signal) error {
	err := syscall.Kill(-process.Pid, sig)
	if errors.Is(err, syscall.ESRCH) {
		// No group left: fall back to the leader, which reports os.ErrProcessDone if it exited too.
		return process.Signal(sig)
	}
	return err
}

// reapProcessGroup kills whatever a command left running in its group after the command itself
// exited — a wrapper script that died (set -e, a crash) without stopping its background children.
func reapProcessGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
