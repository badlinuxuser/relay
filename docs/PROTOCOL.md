# Протокол бэкенда Relay

Транспорт: HTTP/1.1 + JSON, потоковые ответы через Server-Sent Events (`data: <json>\n\n`).
Адрес и токен: `<home>/daemon.json` (`{"addr":"127.0.0.1:8787","token":"...","pid":123}`).
Каждый запрос к `/api/*` и `/v1/*` (кроме `/api/health`) требует заголовок `Authorization: Bearer <token>`.
`<home>`: `$RELAY_HOME`, иначе `<UserConfigDir>/relay`.

Ошибки: статус 4xx/5xx и тело `{"error":{"message":"..."}}`.

## Эндпоинты

| Метод и путь | Описание |
|---|---|
| `GET /api/health` | без авторизации: `{"ok":true,"version":"0.5.0"}`; клиент перезапускает бэкенд другой версии |
| `GET /api/status` | режимы (`effective`: цепочка, которая реально используется), модели, провайдеры (число ключей и доступных), процессы, расход, пресеты, MCP |
| `GET /api/models` | все модели для выбора: найденные у провайдеров (`ollama/…`, `ollama-cloud/…`, `gemini/…`), официальные провайдеры первыми; провайдеры без доступа ничего не дают (модели из конфига показываются только при `discover = "none"`); `providers` с числом найденных, `local`, `skipped` («no key») и `error`. `?refresh=1` опрашивает провайдеров заново; добавление и удаление ключа сбрасывает кэш (60 с) само |
| `GET /api/sessions` | список сессий (без сообщений) |
| `POST /api/sessions` | создать: `{"mode":"small","workspace":"/path"}` |
| `GET /api/sessions/{id}` | сессия с сообщениями |
| `POST /api/sessions/{id}/settings` | `{"mode","model","preset","agent"}`, пропущенные поля не меняются |
| `POST /api/sessions/{id}/messages` | `{"text":"..."}` → поток событий SSE |
| `POST /api/approvals/{id}` | `{"decision":"allow\|always\|deny"}` (id из события `approval`) |
| `POST /api/compare` | `{"text","models":["a","b"],"preset"}` → поток событий с полем `slot` |
| `POST /api/processes/{name}/{start\|stop}` | управление локальными процессами; в теле start можно передать `{"args":[...]}` (добавятся к args из конфига), ответ `{"processes":[...],"already":bool}` |
| `GET /api/keys` | провайдеры и ключи: `stored` (id, label, hint = последние 4 символа), `env`, `total`, `available`, `needs_key` |
| `POST /api/keys` | добавить ключ: `{"provider","key","label"}`; полный ключ назад не возвращается |
| `DELETE /api/keys/{provider}/{id}` | удалить сохранённый ключ |
| `GET /ui/help` | страница справки (HTML на английском, без авторизации); открывается командой `/help` |
| `GET /ui/keys` | страница управления ключами (HTML, без авторизации; токен берёт из `#token=...` в URL) |
| `POST /api/shutdown` | остановить бэкенд |
| `GET /v1/models`, `POST /v1/chat/completions` | OpenAI-совместимый шлюз |

Одновременно в сессии выполняется один запрос (иначе 409).
Отмена: закрыть HTTP-соединение потока, выполнение остановится.

## События потока

Поле `type`; остальные поля зависят от типа (пустые не передаются).

| type | Поля | Смысл |
|---|---|---|
| `model` | `model` | модель начала отвечать |
| `text` | `text` | фрагмент ответа |
| `notice` | `text`, `kind` | служебное: `fallback`, `rate_limit`, `key_switch`, `no_key`, `info` |
| `tool_call` | `tool`, `call_id`, `args`, `desc` | агент вызывает инструмент |
| `approval` | `tool`, `call_id`, `args`, `desc` | ждёт решения; `call_id` это id для `/api/approvals/{id}` |
| `tool_result` | `tool`, `result`, `is_error` | результат инструмента |
| `usage` | `model`, `in`, `out`, `cost` | расход (USD по ценам из конфигурации) |
| `error` | `text`, `kind` | `rate_limit`, `no_key` (нет ключа провайдера), `provider`, `internal` |
| `done` | | выполнение завершено |

В `/api/compare` события дополнительно содержат `slot` (1, 2, ...), номер модели в запросе.

## Пример

```bash
TOKEN=$(cat ~/.config/relay/token)
SID=$(curl -s -X POST localhost:8787/api/sessions -H "Authorization: Bearer $TOKEN" \
        -d '{"mode":"medium"}' | jq -r .id)
curl -N -X POST localhost:8787/api/sessions/$SID/messages \
     -H "Authorization: Bearer $TOKEN" -d '{"text":"Привет"}'
```

## Шлюз OpenAI

Совместим по формату с `/v1/chat/completions` и `/v1/models`: потоковый и обычный режимы,
`usage` в ответе. `model`: `relay-small|medium|hard|maximum`, `auto` или id модели.
Не поддерживается: `tools` и `function_calling` (запросы с ними обрабатываются как обычный чат).

## Защита

- При прослушивании loopback сервер отвечает только на Host `127.0.0.1`, `localhost`, `::1` (DNS rebinding).
- Страница `/ui/keys` статическая и не содержит секретов; CSP запрещает внешние ресурсы.
- Ключи хранятся в `<home>/keys.json` (0600); API отдаёт только последние 4 символа.
