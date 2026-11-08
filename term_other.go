//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
)

// readSecret reads a line without echo (stty), falling back to a plain read when stdin is not a terminal.
func readSecret() (string, error) {
	off := exec.Command("stty", "-echo")
	off.Stdin = os.Stdin
	if off.Run() == nil {
		defer func() {
			on := exec.Command("stty", "echo")
			on.Stdin = os.Stdin
			on.Run()
			fmt.Fprintln(os.Stderr)
		}()
	}
	return readLine()
}
