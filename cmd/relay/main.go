// relay: терминальный интерфейс Relay. Подключается к relayd (запускает его при
// необходимости) и показывает чат, маскота и состояние провайдеров.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"relay/internal/tui"
)

func main() {
	noSpawn := flag.Bool("no-spawn", false, "do not start relayd automatically")
	keep := flag.Bool("keep", false, "keep relayd running after exit")
	flag.Parse()

	p := tea.NewProgram(tui.New(!*noSpawn, *keep), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
