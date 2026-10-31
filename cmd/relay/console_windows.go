//go:build windows

package main

import "syscall"

// setupConsole switches the Windows console to UTF-8 (code page 65001) for the time
// Relay runs. Relay writes UTF-8 (box lines, ❯ ◆ ● ·, Cyrillic text); a console left
// on its default code page (437, 866, 1251, ...) shows them as garbage. The previous
// code pages are restored by the returned function. When there is no console
// (output is redirected or the process is detached) nothing is changed.
func setupConsole() (restore func()) {
	const utf8CP = 65001
	k := syscall.NewLazyDLL("kernel32.dll")
	getOut, setOut := k.NewProc("GetConsoleOutputCP"), k.NewProc("SetConsoleOutputCP")
	getIn, setIn := k.NewProc("GetConsoleCP"), k.NewProc("SetConsoleCP")

	oldOut, _, _ := getOut.Call()
	oldIn, _, _ := getIn.Call()
	if oldOut == 0 && oldIn == 0 {
		return func() {}
	}
	if oldOut != utf8CP {
		setOut.Call(utf8CP)
	}
	if oldIn != utf8CP {
		setIn.Call(utf8CP)
	}
	return func() {
		if oldOut != 0 {
			setOut.Call(oldOut)
		}
		if oldIn != 0 {
			setIn.Call(oldIn)
		}
	}
}
