package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"relay/internal/browser"
	"relay/internal/client"
	"relay/internal/proto"
)

const helpText = `Команды:
  /mode small|medium|hard|maximum   режим (цвет интерфейса и цепочка моделей); короче: /small /medium /hard /max
  /keys                             страница для ввода и хранения ключей API (откроется в браузере)
  /model <id>|auto                  выбрать модель или вернуть выбор по режиму
  /agent on|off                     режим агента (инструменты: файлы, shell, веб, MCP)
  /preset <имя>|none                системный промпт
  /compare <id> <id> ... <текст>    один промпт на несколько моделей
  /new                              новая сессия
  /sessions   /load <id>            список и загрузка сессий
  /start <имя>   /stop <имя>        локальные процессы (лаунчер)
  /clear   /quit
Клавиши: ctrl+t режим · ctrl+a агент · ctrl+n новая · esc стоп · pgup/pgdn прокрутка`

func statusCmd(cl *client.Client) tea.Cmd {
	return func() tea.Msg {
		st, err := cl.Status()
		if err != nil {
			return statusMsg{}
		}
		return statusMsg{st: st}
	}
}

func settingsCmd(cl *client.Client, id string, req proto.SettingsReq) tea.Cmd {
	return func() tea.Msg {
		si, err := cl.Settings(id, req)
		if err != nil {
			return errMsg{err}
		}
		return sessionMsg{sess: si}
	}
}

func newSessionCmd(cl *client.Client, mode, cwd string) tea.Cmd {
	return func() tea.Msg {
		si, err := cl.CreateSession(mode, cwd)
		if err != nil {
			return errMsg{err}
		}
		return sessionMsg{sess: si}
	}
}

func approveCmd(cl *client.Client, id, decision string) tea.Cmd {
	return func() tea.Msg {
		if err := cl.Approve(id, decision); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

func sessionsCmd(cl *client.Client) tea.Cmd {
	return func() tea.Msg {
		list, err := cl.Sessions()
		if err != nil {
			return errMsg{err}
		}
		return sessionsMsg{list: list}
	}
}

func loadCmd(cl *client.Client, query string) tea.Cmd {
	return func() tea.Msg {
		list, err := cl.Sessions()
		if err != nil {
			return errMsg{err}
		}
		for _, s := range list {
			if strings.Contains(s.ID, query) {
				si, err := cl.GetSession(s.ID)
				if err != nil {
					return errMsg{err}
				}
				return sessionMsg{sess: si, reset: true}
			}
		}
		return errMsg{fmt.Errorf("сессия %q не найдена", query)}
	}
}

func procCmd(cl *client.Client, name, action string) tea.Cmd {
	return func() tea.Msg {
		if err := cl.Process(name, action); err != nil {
			return errMsg{err}
		}
		st, err := cl.Status()
		if err != nil {
			return infoMsg{fmt.Sprintf("%s: %s", name, action)}
		}
		return statusMsg{st: st}
	}
}

func (m model) isModelID(id string) bool {
	if m.status == nil {
		return false
	}
	for _, md := range m.status.Models {
		if md.ID == id {
			return true
		}
	}
	return false
}

// command разбирает слэш-команду.
func (m model) command(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	cmd, args := fields[0], fields[1:]
	needConn := func() bool {
		if m.cl == nil || m.sess == nil {
			m.addNotice("Ещё не подключился к бэкенду")
			return false
		}
		return true
	}

	switch cmd {
	case "/help", "/?":
		for _, l := range strings.Split(helpText, "\n") {
			m.addNotice(l)
		}
		return m, nil

	case "/quit", "/exit", "/q":
		return m.quit()

	case "/clear":
		m.entries = nil
		m.scroll = 0
		return m, nil

	case "/small", "/medium", "/hard", "/maximum", "/max":
		mode := strings.TrimPrefix(cmd, "/")
		if mode == "max" {
			mode = "maximum"
		}
		for _, n := range m.modeNames() {
			if n == mode {
				cm := m.setMode(n)
				return m, cm
			}
		}
		m.addError("Режим " + mode + " не настроен в конфигурации")
		return m, nil

	case "/keys":
		if !needConn() {
			return m, nil
		}
		url := m.cl.Base + "/ui/keys#token=" + m.cl.Token
		m.mc.react("noticed", 2*time.Second, "Ключи в браузере")
		if err := browser.Open(url); err != nil {
			m.addNotice("Не удалось открыть браузер. Открой вручную: " + url)
		} else {
			m.addNotice("Открыл страницу ключей в браузере. Добавь ключи и возвращайся: они применяются сразу.")
		}
		return m, nil

	case "/mode":
		if len(args) != 1 {
			m.addNotice("Режим сейчас: " + m.mode + ". Использование: /mode " + strings.Join(m.modeNames(), "|"))
			return m, nil
		}
		for _, n := range m.modeNames() {
			if n == args[0] {
				c := m.setMode(n)
				return m, c
			}
		}
		m.addError("Неизвестный режим " + args[0])
		return m, nil

	case "/model":
		if !needConn() {
			return m, nil
		}
		if len(args) != 1 {
			var ids []string
			if m.status != nil {
				for _, md := range m.status.Models {
					ids = append(ids, md.ID)
				}
			}
			m.addNotice("Использование: /model <id>|auto. Доступны: " + strings.Join(ids, ", "))
			return m, nil
		}
		v := args[0]
		if v == "auto" {
			v = ""
		}
		m.addNotice("Модель: " + args[0])
		return m, settingsCmd(m.cl, m.sess.ID, proto.SettingsReq{Model: &v})

	case "/agent":
		if !needConn() {
			return m, nil
		}
		on := !m.sess.Agent
		if len(args) == 1 {
			on = args[0] == "on" || args[0] == "вкл"
		}
		m.sess.Agent = on
		return m, settingsCmd(m.cl, m.sess.ID, proto.SettingsReq{Agent: &on})

	case "/preset":
		if !needConn() {
			return m, nil
		}
		if len(args) != 1 {
			if m.status != nil {
				m.addNotice("Пресеты: " + strings.Join(m.status.Presets, ", "))
			}
			return m, nil
		}
		v := args[0]
		if v == "none" {
			v = ""
		}
		return m, settingsCmd(m.cl, m.sess.ID, proto.SettingsReq{Preset: &v})

	case "/new":
		c := m.newSession()
		return m, c

	case "/sessions":
		if !needConn() {
			return m, nil
		}
		return m, sessionsCmd(m.cl)

	case "/load":
		if !needConn() || len(args) != 1 {
			return m, nil
		}
		return m, loadCmd(m.cl, args[0])

	case "/start", "/stop":
		if !needConn() || len(args) != 1 {
			m.addNotice("Использование: " + cmd + " <имя процесса>")
			return m, nil
		}
		return m, procCmd(m.cl, args[0], strings.TrimPrefix(cmd, "/"))

	case "/compare":
		if !needConn() {
			return m, nil
		}
		if m.streaming {
			m.addNotice("Подожди, идёт другой запрос")
			return m, nil
		}
		var models []string
		i := 0
		for i < len(args) && m.isModelID(args[i]) {
			models = append(models, args[i])
			i++
		}
		prompt := strings.Join(args[i:], " ")
		if len(models) == 0 || prompt == "" {
			m.addNotice("Использование: /compare <модель> <модель> ... <текст>")
			return m, nil
		}
		m.entries = append(m.entries, entry{kind: eUser, text: prompt})
		cl := m.cl
		preset := m.sess.Preset
		runCmd := m.beginStream(func(ctx context.Context, emit func(proto.Event)) error {
			return cl.Compare(ctx, proto.CompareReq{Text: prompt, Models: models, Preset: preset}, emit)
		})
		m.mc.react("noticed", 2*time.Second, "Сравниваю модели")
		return m, runCmd
	}

	m.addError("Неизвестная команда " + cmd + " (/help)")
	return m, nil
}
