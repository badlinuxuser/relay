# Протокол relayd

Транспорт: HTTP/1.1 + JSON, потоковые ответы через Server-Sent Events (`data: <json>\n\n`).
Адрес и токен: `<home>/daemon.json` (`{"addr":"127.0.0.1:8787","token":"...","pid":123}`).
Каждый запрос (кроме `/api/health`) требует заголовок `Authorization: Bearer <token>`.
`<home>`: `$RELAY_HOME`, иначе `<UserConfigDir>/relay`.

Ошибки: статус 4xx/5xx и тело `{"error":{"message":"..."}}`.

## Эндпоинты

| Метод и путь | Описание |
|---|---|
| `GET /api/health` | без авторизации: `{"ok":true,"version":"0.1.0"}` |
| `GET /api/status` | режимы, модели, провайдеры (число ключей и доступных), процессы, расход, пресеты, MCP |
| `GET /api/sessions` | список сессий (без сообщений) |
| `POST /api/sessions` | создать: `{"mode":"small","workspace":"/path"}` |
| `GET /api/sessions/{id}` | сессия с сообщениями |
| `POST /api/sessions/{id}/settings` | `{"mode","model","preset","agent"}`, пропущенные поля не меняются |
| `POST /api/sessions/{id}/messages` | `{"text":"..."}` → поток событий SSE |
| `POST /api/approvals/{id}` | `{"decision":"allow\|always\|deny"}` (id из события `approval`) |
| `POST /api/compare` | `{"text","models":["a","b"],"preset"}` → поток событий с полем `slot` |
| `POST /api/processes/{name}/{start\|stop}` | управление локальными процессами |
| `POST /api/shutdown` | остановить relayd |
| `GET /v1/models`, `POST /v1/chat/completions` | OpenAI-совместимый шлюз |

Одновременно в сессии выполняется один запрос (иначе 409).
Отмена: закрыть HTTP-соединение потока, выполнение остановится.

## События потока

Поле `type`; остальные поля зависят от типа (пустые не передаются).

| type | Поля | Смысл |
|---|---|---|
| `model` | `model` | модель начала отвечать |
| `text` | `text` | фрагмент ответа |
| `notice` | `text`, `kind` | служебное: `fallback`, `rate_limit`, `key_switch`, `info` |
| `tool_call` | `tool`, `call_id`, `args`, `desc` | агент вызывает инструмент |
| `approval` | `tool`, `call_id`, `args`, `desc` | ждёт решения; `call_id` это id для `/api/approvals/{id}` |
| `tool_result` | `tool`, `result`, `is_error` | результат инструмента |
| `usage` | `model`, `in`, `out`, `cost` | расход (USD по ценам из конфигурации) |
| `error` | `text`, `kind` | `rate_limit`, `provider`, `internal` |
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
