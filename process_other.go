//go:build !windows

package main

import "os/exec"

func hideSubprocessWindow(cmd *exec.Cmd) {}
