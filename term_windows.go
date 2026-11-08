package main

import (
	"fmt"
	"os"
	"syscall"
)

var setConsoleMode = syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode")

// readSecret reads a line with console echo turned off (ENABLE_ECHO_INPUT = 0x4).
func readSecret() (string, error) {
	h := syscall.Handle(os.Stdin.Fd())
	var mode uint32
	if syscall.GetConsoleMode(h, &mode) == nil {
		setConsoleMode.Call(uintptr(h), uintptr(mode&^0x4))
		defer func() {
			setConsoleMode.Call(uintptr(h), uintptr(mode))
			fmt.Fprintln(os.Stderr)
		}()
	}
	return readLine()
}
