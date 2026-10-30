# Relay

Relay is a terminal-native AI workstation for working with local and remote language models from a single interface. It combines a TUI client (`relay`) with a background daemon (`relayd`) that handles routing, model selection, sessions, tool execution, and an OpenAI-compatible API gateway.

The project is designed for developers who want to:

<<<<<<< HEAD
- use local models and cloud APIs in one place
- switch between model tiers quickly in the terminal
- give an AI coding agent access to files, shell commands, and MCP tools
- expose the same backend to other clients through an OpenAI-like API

The name reflects the goal: a relay between your terminal, your models, and your tooling.

> “Faster. Closer. Together.”

```text
┌──────────────┐   HTTP + SSE    ┌─────────────────────────────────────────────┐
│ relay        │ ◄─────────────► │ relayd                                     │
│ (TUI)        │                 │ router · agent · runners · MCP · /v1 gateway │
└──────────────┘                 └──────────────┬──────────────────────────────┘
   terminal UI                           │
                                         │
                                 ┌───────┴────────┬─────────────┬──────────────┐
                                 │                │             │              │
                            Ollama / llama.cpp  Anthropic  OpenAI-compatible
                            LM Studio / vLLM      (keys with rotation)   OpenAI, OpenRouter, Gemini
```

## Why Relay?
=======
```
┌────────────┐   HTTP + SSE    ┌──────────────────────────────────────────────┐
│  relay     │ ◄─────────────► │  relay daemon (тот же файл, фоновый процесс) │
│  (TUI)     │   localhost     │  роутер · агент · процессы · MCP · шлюз /v1  │
└────────────┘                 └───────┬───────────┬───────────┬──────────────┘
  любой другой интерфейс               │           │           │
  (web, GUI, скрипты)              Ollama /     Anthropic    OpenAI-совместимые
                                  llama.cpp /   (ключи с     (OpenAI, OpenRouter,
                                  LM Studio     ротацией)    Gemini, vLLM ...)
```

Relay это один исполняемый файл `relay`. Интерфейс и бэкенд работают как два процесса: интерфейс сам
запускает бэкенд в фоне (`relay daemon`). Интерфейс ничего не знает о провайдерах, всё делает бэкенд,
поэтому можно написать любой другой клиент (см. [docs/PROTOCOL.md](docs/PROTOCOL.md)).
>>>>>>> cae6ddf (0.0.1)

Relay is built for the common workflow where developers want to move quickly between:

<<<<<<< HEAD
- local inference for privacy and speed
- remote models for better reasoning or larger context
- AI-assisted coding in a constrained workspace
- external tools and scripts that can talk to the same backend
=======
- **Провайдеры:** Ollama, llama.cpp, LM Studio, vLLM, OpenAI, OpenRouter, Gemini (все через OpenAI-совместимый
  адаптер) и Anthropic. Ответы идут потоком.
- **Режимы small / medium / hard / maximum:** каждый задаёт цепочку моделей, цвет интерфейса и маскота
  (maximum: радужная палитра). Переключение `ctrl+t`.
- **Роутинг:** цепочка моделей режима с фолбэком при лимитах и ошибках; несколько ключей на провайдера с
  автоматической ротацией; учёт токенов и стоимости; режим `auto` для шлюза.
- **Лаунчер:** запуск и остановка локальных серверов (`/start ollama`), проверка готовности.
- **Агент для кода:** цикл «модель → инструмент → результат»: чтение, поиск, запись и правка файлов,
  shell, веб, а также инструменты MCP-серверов. Файлы ограничены рабочей папкой, опасные действия
  требуют подтверждения (`y` разово, `a` на сессию, `n` отказ).
- **Сессии:** история сохраняется на диск, смена модели и режима посреди диалога, системные промпты-пресеты.
- **Сравнение:** `/compare модель1 модель2 текст`: один промпт на несколько моделей одновременно.
- **Шлюз:** Бэкенд отдаёт OpenAI-совместимый endpoint, и другие программы работают через Relay.
- **Маскот:** реагирует на события (подробнее ниже).
>>>>>>> cae6ddf (0.0.1)

The backend and UI are intentionally split. The TUI does not talk directly to providers; it sends requests to the daemon, which is responsible for routing, session management, and tool execution.

## Features

- Model routing across local and remote providers
- Modes: `small`, `medium`, `hard`, and `maximum`
- OpenAI-compatible gateway at `/v1/chat/completions`
- Agentic coding loop with file read/write/edit, grep, shell, web fetch, and MCP support
- Session persistence on disk
- Automatic API key rotation and fallback behavior
- Local process launcher for services such as Ollama
- Terminal mascot / themed TUI experience

## Supported providers

Relay is designed to work with multiple backends, including:

- Ollama
- llama.cpp
- LM Studio
- vLLM
- OpenAI
- OpenRouter
- Gemini
- Anthropic

## Quick start

### Requirements

- Go 1.22+
- Optional: Ollama or another local model server

### Build

```bash
<<<<<<< HEAD
git clone https://github.com/ifuckingjoke/relay.git
cd relay
make build
```

This builds both binaries:

- `bin/relayd`
- `bin/relay`

### Run

```bash
make run
```

This launches the TUI. The daemon starts automatically if needed.

### Optional: run daemon only

```bash
make daemon
```

### Local model example

If you use Ollama, install a model and then start it from the app or via shell:

```bash
ollama pull qwen2.5-coder:7b
```

Then in the Relay UI, use a command such as:

```text
/start ollama
```

## Configuration

On first run, Relay creates a config file in the platform-specific app config directory.

- Linux: `~/.config/relay/`
- Windows: `%AppData%\relay\`

API keys are expected as environment variables. Example:

```bash
export ANTHROPIC_API_KEY=...
export ANTHROPIC_API_KEY_2=...   # fallback / rotation key
```

The daemon also keeps a local token for local API access.
=======
make install        # сборка и добавление в PATH (Linux и Windows)
relay               # запуск из любого каталога: агент работает в текущей папке
```

Без make: `go build -o relay ./cmd/relay && ./relay install`.
`relay install` копирует файл в `~/.local/bin` (Linux) или `%LOCALAPPDATA%\Programs\relay` (Windows) и
добавляет каталог в PATH (`~/.bashrc`, `~/.zshrc`, fish или пользовательский PATH Windows). После этого
открой новый терминал.

### Ключи API

Команда `relay keys` (или `/keys` в интерфейсе) открывает в браузере страницу на localhost: там для
каждого провайдера можно вставить один или несколько ключей. Ключи сохраняются в `keys.json`
(права только владельцу), работают между сессиями и применяются сразу. Переменные окружения не нужны
(но поддерживаются как необязательный вариант: `api_key_env` в конфиге).

### Локальные модели

Установи Ollama, скачай модель из конфига (`ollama pull qwen2.5-coder:7b`) и в интерфейсе выполни
`/start ollama`. Локальным серверам ключ не нужен.

## Команды CLI

| Команда | Что делает |
|---|---|
| `relay` | интерфейс в терминале (бэкенд стартует сам) |
| `relay keys` | страница для ввода и хранения ключей API |
| `relay status` | провайдеры, ключи, процессы, расход |
| `relay stop` | остановить фоновый бэкенд |
| `relay daemon` | бэкенд в текущем терминале (для отладки) |
| `relay install` / `uninstall` | добавить в PATH или убрать |
| `relay config [--example]` | путь к конфигурации или пример |

Файлы данных: Linux `~/.config/relay/`, Windows `%AppData%\relay\` (`config.toml`, `keys.json`, `sessions/`,
`usage.json`, `token`).
>>>>>>> cae6ddf (0.0.1)

## OpenAI-compatible gateway

<<<<<<< HEAD
Relay exposes an OpenAI-style endpoint for compatibility with tools and scripts.
=======
## Управление

| Клавиша | Действие |
|---|---|
| `enter` | отправить сообщение |
| `ctrl+t` | следующий режим (small → medium → hard → maximum) |
| `ctrl+a` | включить или выключить режим агента |
| `ctrl+n` | новая сессия |
| `esc` | остановить выполнение |
| `pgup` / `pgdn`, колесо мыши | прокрутка |
| `ctrl+c` | выход |

Команды: `/mode` (или `/small` `/medium` `/hard` `/max`), `/keys`, `/model`, `/agent`, `/preset`, `/compare`,
`/new`, `/sessions`, `/load`, `/start`, `/stop`, `/clear`, `/help`, `/quit`.

## Маскот

Пять эмоций из спрайт-листа, четыре цветовые темы (small: зелёная, medium: фиолетовая, hard: красная,
maximum: радужная).

| Событие | Эмоция |
|---|---|
| ожидание | спокойная |
| вызов инструмента, смена ключа | заметила |
| ошибка, нужно подтверждение, внезапное пробуждение | удивление |
| ответ получен, смена режима, старт | радость |
| лимит ключа исчерпан, долго нет активности | устала (спит) |

Смена режима анимируется: цвета интерфейса плавно перетекают, а маскот «перекрашивается» волной
с яркой линией-разделителем.

Спрайты собираются из `assets/sheets/*.png` скриптом `tools/sprites/build.py` (`make sprites`): чёткие
пиксели 44x36, два пикселя на ячейку терминала (символ `▀`), маскот стоит на карточке.
Радужная тема строится из зелёного листа: акцентные цвета перекрашиваются на лету.

## Шлюз OpenAI
>>>>>>> cae6ddf (0.0.1)

```bash
TOKEN=$(cat ~/.config/relay/token)     # Windows: %AppData%\relay\token
curl http://127.0.0.1:8787/v1/chat/completions \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "relay-medium",
    "messages": [{"role": "user", "content": "Hello from Relay"}]
  }'
```

Available model aliases usually include values like:

- `relay-small`
- `relay-medium`
- `relay-hard`
- `relay-maximum`
- `auto`

<<<<<<< HEAD
## Common controls
=======
- API слушает только `127.0.0.1`, требует токен (`<home>/token`, права 0600) и проверяет заголовок Host
  (защита от DNS rebinding). Страница ключей получает токен из фрагмента URL, он не уходит на сервер.
- Ключи в `keys.json` хранятся открытым текстом (права 0600), интерфейс и API показывают только последние 4 символа.
- Агент ограничен рабочей папкой; запись, shell и MCP всегда спрашивают подтверждение.
- Shell не изолирован: подтверждай команды внимательно.
>>>>>>> cae6ddf (0.0.1)

| Key | Action |
| --- | --- |
| `Enter` | send message |
| `Ctrl+T` | switch mode |
| `Ctrl+A` | toggle agent mode |
| `Ctrl+N` | new session |
| `Esc` | stop current action |
| `PgUp` / `PgDn` | scroll |
| `Ctrl+C` | quit |

<<<<<<< HEAD
Useful slash commands include:

- `/mode`
- `/model`
- `/agent`
- `/preset`
- `/compare`
- `/new`
- `/sessions`
- `/load`
- `/start`
- `/stop`
- `/clear`
- `/help`
- `/quit`

## Project layout

```text
cmd/relayd         backend daemon
cmd/relay          terminal UI
internal/config    config loading and TOML setup
internal/provider  provider adapters and key rotation
internal/router    routing logic and model fallback
internal/agent     agent execution loop
internal/tools     file, shell, web, and MCP tools
internal/mcp       MCP client
internal/runner    process launcher
internal/session   session history and persistence
internal/server     HTTP API and SSE server
internal/client     API client for clients/UI
internal/tui       TUI, view, commands, mascot, themes
assets/sheets      sprite sheets
tools/sprites      sprite generation helpers
=======
```
cmd/relay          единственный исполняемый файл: TUI, daemon, keys, status, stop, install, config
internal/daemon    запуск бэкенда (relay daemon)
internal/config    конфигурация (TOML)
internal/keystore  хранилище ключей API (keys.json)
internal/provider  адаптеры openai и anthropic, ротация ключей
internal/router    режимы, цепочки моделей, фолбэк, учёт расхода
internal/agent     цикл агента
internal/tools     инструменты агента (файлы, shell, веб, MCP)
internal/mcp       клиент MCP (stdio)
internal/runner    лаунчер локальных процессов
internal/session   история диалогов
internal/server    HTTP API, SSE, шлюз /v1, страница ключей (ui/keys.html)
internal/client    клиент API, автозапуск бэкенда в фоне
internal/browser   открытие URL в браузере
internal/tui       интерфейс, маскот, темы
tools/sprites      конвертер спрайтов
assets/sheets      исходные спрайт-листы
>>>>>>> cae6ddf (0.0.1)
```

## Security notes

<<<<<<< HEAD
Relay is a developer tool and should be treated as such.

- the daemon binds to local-only interfaces and expects a token
- agent actions are restricted to the workspace
- file writes, shell commands, and external tools may require approval
- shell execution is not sandboxed; review commands carefully before approving

## Status

This project is an experimental terminal AI tool. It is intentionally lightweight and practical rather than polished enterprise software. It is designed to be hackable and easy to extend.

## Contributing

Pull requests and experiments are welcome. If you want to improve routing, provider integrations, the TUI, or the agent workflow, this repo is structured to make that possible with relatively little ceremony.

## Note

This is a vibe-code project, built with Claude as an AI-assisted development workflow. It is a creative, experimental codebase and should be treated as such rather than as a production-grade commercial product.
=======
Версия 0.2. Версия 0.1 собралась и запустилась у автора; изменения 0.2 (единый бинарник, ключи через
страницу, новые спрайты, анимация) написаны без компилятора, подробности и план в [PROJECT.md](PROJECT.md).
>>>>>>> cae6ddf (0.0.1)
