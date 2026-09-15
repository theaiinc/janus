//go:build windows

package supervisor

import (
	"os"
	"os/exec"
	"strconv"
)

func configureProcessGroup(*exec.Cmd) {}

// Windows has no SIGTERM for console processes, so stopping was already a hard kill; taskkill /T
// extends it to the whole tree so a `cmd /C` wrapper's children don't outlive it.
func terminateProcessTree(process *os.Process) error {
	return killProcessTree(process)
}

func killProcessTree(process *os.Process) error {
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(process.Pid)).Run(); err != nil {
		return process.Kill()
	}
	return nil
}

func reapProcessGroup(int) {}
