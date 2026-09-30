//go:build windows

package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

var (
	k32                = syscall.NewLazyDLL("kernel32.dll")
	procGetStdHandle   = k32.NewProc("GetStdHandle")
	procGetConsoleMode = k32.NewProc("GetConsoleMode")
	procSetConsoleMode = k32.NewProc("SetConsoleMode")
)

const enableEchoInput = 0x0004

// readPassword 关闭控制台回显读取一行密码。
// 若 stdin 不是控制台（例如被重定向），退回普通读取。
func readPassword() (string, error) {
	// STD_INPUT_HANDLE = -10
	h, _, _ := procGetStdHandle.Call(^uintptr(9))

	var mode uint32
	r, _, _ := procGetConsoleMode.Call(h, uintptr(unsafe.Pointer(&mode)))
	if r == 0 {
		rd := bufio.NewReader(os.Stdin)
		s, err := rd.ReadString('\n')
		return strings.TrimRight(s, "\r\n"), err
	}

	_, _, _ = procSetConsoleMode.Call(h, uintptr(mode&^enableEchoInput))
	defer func() {
		_, _, _ = procSetConsoleMode.Call(h, uintptr(mode))
		fmt.Println()
	}()

	rd := bufio.NewReader(os.Stdin)
	s, err := rd.ReadString('\n')
	return strings.TrimRight(s, "\r\n"), err
}
