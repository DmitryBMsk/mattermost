# Сетап плагина mattermost-ai с detmir-gpt

Инструкция для dev-окружения Mattermost (fork из `feature/thread-summarization`). Цель — поднять AI-бота `matty`, подключённого к внутреннему LLM `detmir-gpt` (`http://gpt-api.detmir.team:8000/v1`, openai-compatible), чтобы заработали фичи: AI-саммари тредов, DM с ботом, ask-AI, ассистент.

## 0. Предусловия

- MM backend запущен: `cd /Users/dmitrybakhtin/WebstormProjects/mattermost/server && make run-server` (ждём `Server is listening on [::]:8065`).
- Webapp dev-server работает: `cd /Users/dmitrybakhtin/WebstormProjects/mattermost/webapp && make dev` → http://localhost:9005.
- Есть аккаунт с ролью `system_admin`. На внешней `mattermost_test` это, например, `dbahtin` — повысить роль: `./bin/mmctl --local roles system_admin <username>`.
- База содержит < `maxUsersLimit` активных юзеров (для Team Edition без лицензии — 200). Если в БД больше — см. раздел 5.

Всё ниже выполняется в директории `/Users/dmitrybakhtin/WebstormProjects/mattermost/server`.

## 1. Установка плагина

Плагин `mattermost-ai` приходит в prepackaged-дистрибутиве MM v11.7.0+, и MM сам его распакует при старте. Проверка:

```bash
./bin/mmctl --local plugin list | grep -i ai
# ожидание:  mattermost-ai: Agents, Version: 1.14.0
```

Если отсутствует — положить `mattermost-ai.tar.gz` в `./prepackaged_plugins/` и перезапустить сервер (`make stop-server && make run-server`). После старта в логе должно появиться `Installing extracted plugin ... plugin_id=mattermost-ai`.

## 2. Настройка LLM-сервиса (detmir-gpt)

Способ А — через UI /system_console:

1. В webapp: `System Console → Plugins → Agents` (или `Integrations → Agents`).
2. Блок **Services** → `Add Service`. Заполнить:
   - **Name:** `Detmir GPT`
   - **Service Type:** `OpenAI Compatible`
   - **API URL:** `http://gpt-api.detmir.team:8000/v1`
   - **API Key:** `ak_rulhdUWm8UxDffwCNgUpEh9RFyN4mHqQDpbaBStOLws`
   - **Default Model:** `detmir-gpt`
   - Остальные поля (orgId, region, token limits) оставить пустыми/дефолтными.
3. `Save`.

Способ Б — через `PUT /api/v4/config` (атомарно с ботом, см. ниже раздел 3).

Быстрая проверка endpoint'а напрямую (без MM):

```bash
curl -s -H "Authorization: Bearer ak_rulhdUWm8UxDffwCNgUpEh9RFyN4mHqQDpbaBStOLws" \
  -H "Content-Type: application/json" \
  -d '{"model":"detmir-gpt","messages":[{"role":"user","content":"ping"}],"max_tokens":10}' \
  http://gpt-api.detmir.team:8000/v1/chat/completions
# ожидание: HTTP 200, content с coherent text
```

## 3. Настройка бота

В `System Console → Plugins → Agents` в блоке **Bots** → `Add Bot`. Поля:

- **Username:** `matty`
- **Display Name:** `Matty`
- **Service:** `Detmir GPT` (из предыдущего шага)
- **Model:** `detmir-gpt` (важно — не пустая строка; иначе `bot.llm` останется `nil` и фичи падают).
- **Reasoning Enabled:** `false` (выключить — vLLM-endpoint detmir-gpt не возвращает reasoning-поля, а плагин может предполагать их наличие).
- **Enable Vision:** по желанию.
- **Channel / User Access Level:** по желанию (для dev удобно оставить по умолчанию).
- **Custom Instructions:** по желанию.
- Default bot: отметить `matty` как default, чтобы backend-фичи (`AgentCompletion` без явного ID) ходили в него.

`Save`.

## 4. MCP servers — обязательно массив, не объект

Плагин v1.14.0 ожидает `PluginSettings.Plugins.mattermost-ai.config.mcp.servers` типа `[]`. В некоторых дампах конфига это поле приходит как `{}` (пустой объект), и на старте плагин пишет в stderr:

```
LoadPluginConfiguration API failed to unmarshal: json: cannot unmarshal
object into Go struct field MCPConfig.config.mcp.servers of type
[]config.MCPServerConfig
```

Из-за этой ошибки вся конфигурация плагина не грузится — боты/сервисы не попадают в память. Починить через full-config PUT:

```bash
TOKEN=$(curl -s -D - -X POST http://localhost:8065/api/v4/users/login \
  -H 'Content-Type: application/json' \
  -d '{"login_id":"<admin>","password":"<password>"}' \
  | grep -i '^token:' | awk '{print $2}' | tr -d '\r')

curl -s http://localhost:8065/api/v4/config -H "Authorization: Bearer $TOKEN" \
  | python3 -c "
import json,sys
c=json.load(sys.stdin)
p=c['PluginSettings']['Plugins']['mattermost-ai']['config']
p['mcp']['servers']=[]
p['bots'][0]['model']='detmir-gpt'
p['bots'][0]['reasoningEnabled']=False
# маскированные поля — вернуть реальные значения
c['SqlSettings']['DataSource']='<реальный DSN>'
c['SqlSettings']['AtRestEncryptKey']='<ключ из config.json>'
c['FileSettings']['PublicLinkSalt']='<соль из config.json>'
print(json.dumps(c))
" > /tmp/mm-cfg.json

curl -s -X PUT http://localhost:8065/api/v4/config \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  --data-binary @/tmp/mm-cfg.json | head -c 200
# ожидание: HTTP 200 и возврат нового конфига
```

⚠️ MM маскирует sensitive-поля (`DataSource`, `AtRestEncryptKey`, `PublicLinkSalt`, `SMTPPassword`, `RedisPassword`, `ElasticsearchSettings.Password`, `SplitKey`) звёздочками при GET. При PUT нужно подставить реальные значения — иначе MM сохранит `"********************************"` как пароль и всё сломается. Значения лежат в `/Users/dmitrybakhtin/WebstormProjects/mattermost/server/config/config.json`.

## 5. User limit (!! важно для shared-БД)

Если БД содержит > `maxUsersLimit` активных юзеров, `pluginAPI.Bot.UpdateActive(matty, true)` при старте плагина возвращает `app.user.update_active.user_limit.exceeded`. Плагин делает `continue` в цикле инициализации (`vendor/mattermost-plugin-ai@v1.14.0/bots/bots.go:299-322`), **пропуская `getLLM()`** для этого бота. `bot.llm` остаётся `nil`. При первом же вызове AI-фичи — panic `nil pointer dereference` в `api/api_llm_bridge.go:258`.

Симптом в логе:

```
Failed to update bot active  bot_name=matty  error="UpdateActive: Can't activate user. Server exceeds safe user limit. ERROR_SAFETY_LIMITS_EXCEEDED."
```

и далее 500 `app.thread_summary.llm_error` с пустым body.

Варианты фикса:

| Путь | Когда использовать |
|---|---|
| **A. Лицензия MM** — положить Enterprise/Team trial-лицензию в `/Users/dmitrybakhtin/WebstormProjects/mattermost/server/config/mattermost.mattermost-license`. Лимит поднимется до `license.Features.Users`. | prod, общие стенды, любые shared-БД |
| **B. Hardcoded patch (dev only)** — в `/Users/dmitrybakhtin/WebstormProjects/mattermost/server/channels/app/limits.go:12-15` увеличить `maxUsersLimit` и `maxUsersHardLimit`. Пересобрать (`make run-server`). | локальный dev, нельзя в prod |
| **C. Деактивировать «лишних» юзеров** до лимита. | если БД действительно ваша и часть юзеров архивные |

В нашем форке уже применён вариант B (см. `docs/plans/2026-04-17-external-db-switch-and-russian-thread-summary.md`).

## 6. Активация плагина + рестарт

После любого изменения config.json/БД-конфига плагина — перезапустить плагин, чтобы он пересоздал боты с новым `llm`:

```bash
./bin/mmctl --local plugin disable mattermost-ai
./bin/mmctl --local plugin enable  mattermost-ai
```

В логах после enable должно быть:

- `Plugin activated  plugin_id=mattermost-ai  version=1.14.0`
- **нет** `Failed to update bot active ... ERROR_SAFETY_LIMITS_EXCEEDED`
- **нет** `LoadPluginConfiguration API failed to unmarshal`
- `Embedded MCP server handlers initialized successfully`

## 7. Smoke-тест

### 7.1. Через REST API

```bash
TOKEN=...  # см. раздел 4

# выбрать любой post_id с несколькими ответами
POST_ID="<root_post_id>"

curl -s -X POST "http://localhost:8065/api/v4/posts/$POST_ID/summary" \
  -H "Authorization: Bearer $TOKEN" -w '\nHTTP %{http_code}\n'
```

Ожидание: `HTTP 200`, JSON с полями `summary`, `key_points` (массив объектов `{text, post_ids}`), `participants`, `thread_post_count`, `model=ai-bridge`. Текст — на русском (промпт переведён в `server/channels/app/thread_summary.go:117-118`).

### 7.2. Через UI

1. Открыть http://localhost:9005, залогиниться как system_admin.
2. Открыть любой канал с тредами, развернуть тред (RHS справа).
3. В заголовке RHS нажать иконку ✨ `Summarize` (добавлена нашей веткой рядом с Follow / Unfollow).
4. Появится панель `AI Summary` — через 2-5 сек придёт саммари + key points с кликабельными @mention.

### 7.3. Через DM

1. В sidebar выбрать DM с `@matty` (плагин создаёт этот канал автоматически при активации).
2. Отправить любое сообщение. В ответ Matty должен использовать `detmir-gpt` и вернуть текст.

## 8. Диагностика частых ошибок

| Симптом | Где смотреть | Вероятная причина |
|---|---|---|
| `app.thread_summary.ai_unavailable` | лог MM: `AI plugin bridge not available` | плагин не активирован / старше v1.5.0 |
| `app.thread_summary.llm_error` + пустой body в "request failed with status 500:" | `plugin_stderr` — нужен `LogSettings.FileLevel = DEBUG` | panic `bot.LLM()==nil` — см. раздел 5 (user limit) или раздел 3 (пустой model) |
| `request failed with status 500: failed to complete LLM request: ...` | тот же endpoint | сам LLM отклонил запрос. Проверить apiURL, apiKey, что модель доступна в `GET /v1/models` |
| `LoadPluginConfiguration API failed to unmarshal` | `plugin_stderr` | `mcp.servers` приходит как `{}` вместо `[]`. См. раздел 4. |
| `Failed to update bot active ... ERROR_SAFETY_LIMITS_EXCEEDED` | лог MM | `ActiveUserCount > maxUsersLimit`. См. раздел 5. |
| Саммари приходит на английском | — | плагин кэшировал старый системный prompt. `plugin disable && enable` либо `make stop-server && make run-server`. |

Подробный лог плагина включается так:

```bash
./bin/mmctl --local config set LogSettings.FileLevel DEBUG
# откатить:
./bin/mmctl --local config set LogSettings.FileLevel INFO
```

Дальше смотреть: `tail -f /Users/dmitrybakhtin/WebstormProjects/mattermost/server/logs/mattermost.log`.

## 9. Откат

- Отключить плагин: `./bin/mmctl --local plugin disable mattermost-ai`.
- Убрать bot и service из конфига плагина (через UI → Delete) — или оставить, плагин при disabled ничего не делает.
- Откатить патч user limit: `git checkout /Users/dmitrybakhtin/WebstormProjects/mattermost/server/channels/app/limits.go` + `make run-server`.
- Вернуть английские промпты: `git checkout /Users/dmitrybakhtin/WebstormProjects/mattermost/server/channels/app/thread_summary.go` + `make run-server`.
