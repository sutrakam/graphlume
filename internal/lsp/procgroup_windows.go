//go:build windows

package lsp

import (
	"fmt"
	"os/exec"
)

func configureProcessGroup(cmd *exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// On Windows, killing cmd.Process directly orphans child worker processes
	// (e.g. rust-analyzer worker processes, tsserver child nodes).
	// taskkill /F /T terminates the target process and its entire process tree.
	killCmd := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", cmd.Process.Pid))
	if err := killCmd.Run(); err != nil {
		_ = cmd.Process.Kill()
	}
}
