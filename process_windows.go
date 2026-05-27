//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

const windowsCreateNoWindow = 0x08000000

func hideSubprocessWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windowsCreateNoWindow
}
