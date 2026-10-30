// relay: лаунчер и роутер нейросетей. Один исполняемый файл:
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

const usage = `relay: лаунчер и роутер нейросетей

Использование:
  relay                     запустить интерфейс (бэкенд стартует сам)
  relay keys                открыть в браузере страницу с ключами API
  relay status              состояние бэкенда: провайдеры, ключи, процессы, расход
  relay stop                остановить фоновый бэкенд
  relay daemon              запустить бэкенд в этом терминале
  relay install [--dir D]   установить в PATH (Linux: ~/.local/bin, Windows: %LOCALAPPDATA%\Programs\relay)
  relay uninstall           удалить из PATH
  relay config [--example]  путь к конфигурации или пример конфигурации
  relay version

Флаги интерфейса:
  --no-spawn   не запускать бэкенд автоматически
  --keep       не останавливать бэкенд при выходе
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
	fmt.Fprintf(os.Stderr, "relay: неизвестная команда %q\n\n%s", cmd, usage)
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
