//go:build !windows

package main

// setupConsole is needed on Windows only (see console_windows.go).
func setupConsole() (restore func()) { return func() {} }
