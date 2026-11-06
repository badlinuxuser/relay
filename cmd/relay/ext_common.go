package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// confirm asks a yes/no question. Without a terminal (a script, a pipe) the answer is no:
// nothing that needs a decision is done behind the user's back.
func confirm(prompt string) bool {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		fmt.Println(prompt + "(no terminal, so: no; add --yes to agree)")
		return false
	}
	fmt.Print(prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "д", "да":
		return true
	}
	return false
}

// multiFlag is a flag that can be given several times (--env A=1 --env B=2).
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// oneLine shortens text to n characters on one line.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "relay:", err)
	return 1
}
