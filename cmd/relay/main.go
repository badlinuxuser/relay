// relay: launcher and router for AI models. Один исполняемый файл:
// интерфейс в терминале, фоновый бэкенд, управление ключами, установка в PATH.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"relay/internal/daemon"
	"relay/internal/proto"
	"relay/internal/tui"
)

const usage = `relay: launcher and router for AI models

Usage:
  relay                     start the interface (the backend starts by itself)
  relay keys                open the API keys page in your browser
  relay status              backend status: providers, keys, processes, usage
  relay stop                stop the background backend
  relay daemon              run the backend in this terminal
  relay install [--dir D]   install to PATH (Linux: ~/.local/bin, Windows: %LOCALAPPDATA%\Programs\relay)
  relay uninstall           remove from PATH
  relay config [--example]  config path, or an example config
  relay version

Interface flags:
  --no-spawn   do not start the backend automatically
  --keep       do not stop the backend on exit
`

func main() {
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		os.Exit(dispatch(args[0], args[1:]))
	}
	os.Exit(runTUI(args))
}

func dispatch(cmd string, args []string) int {
	switch cmd {
	case "daemon":
		return daemon.Run(args)
	case "keys":
		return cmdKeys()
	case "status":
		return cmdStatus()
	case "stop":
		return cmdStop()
	case "install":
		return cmdInstall(args)
	case "uninstall":
		return cmdUninstall(args)
	case "config":
		return cmdConfig(args)
	case "version":
		fmt.Println("relay", proto.Version)
		return 0
	case "help":
		fmt.Print(usage)
		return 0
	}
	fmt.Fprintf(os.Stderr, "relay: unknown command %q\n\n%s", cmd, usage)
	return 2
}

func runTUI(args []string) int {
	fs := flag.NewFlagSet("relay", flag.ContinueOnError)
	fs.Usage = func() { fmt.Print(usage) }
	noSpawn := fs.Bool("no-spawn", false, "do not start the backend automatically")
	keep := fs.Bool("keep", false, "keep the backend running after exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	p := tea.NewProgram(tui.New(!*noSpawn, *keep), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "relay:", err)
		return 1
	}
	return 0
}
