# Сессия 2026-04-17: перевод backend на внешнюю PostgreSQL + саммари тредов на русском

Журнал работ по двум смежным задачам:
1. Переключить dev-стенд Mattermost с локального Docker postgres на внешнюю БД `10.210.39.149/mattermost_test`.
2. Сделать так, чтобы фича `thread summarization` возвращала саммари на русском языке.

Финальное состояние: обе задачи решены. MM работает на `10.210.39.149/mattermost_test` (schema `public`, 946 юзеров / 12 команд / 10.5M постов), саммари возвращает валидный JSON на русском через `detmir-gpt`.

---

## 1. Изменения в коде и конфигурации

| Файл | Изменение | Зачем |
|---|---|---|
| `/Users/dmitrybakhtin/WebstormProjects/mattermost/server/config/config.json:164` | `DataSource` → `postgres://mmuser:mmuser@10.210.39.149:5432/mattermost_test?sslmode=disable&connect_timeout=10&binary_parameters=yes` | Подключение к внешней БД. |
| `/Users/dmitrybakhtin/WebstormProjects/mattermost/server/config.override.mk:1` | Убран `postgres` из `ENABLED_DOCKER_SERVICES` (осталось `inbucket redis minio`). | Локальный docker postgres больше не нужен. |
| `/Users/dmitrybakhtin/WebstormProjects/mattermost/server/channels/app/thread_summary.go:117-118` | Системный и user-промпт переведены на русский; явная инструкция «строго на РУССКОМ ЯЗЫКЕ», «не переводи @username». | Саммари и ключевые моменты приходят по-русски. |
| `/Users/dmitrybakhtin/WebstormProjects/mattermost/server/channels/app/limits.go:13-14` | `maxUsersLimit: 200 → 1_000_000`, `maxUsersHardLimit: 250 → 1_000_000`. | В рабочей БД 946 active users, штатный Team-Edition-лимит (200) блокировал активацию бота плагина `mattermost-ai`. Патч только для локального dev — в prod/enterprise нужно лицензией. |

## 2. Изменения в БД и runtime-конфиге плагина

| Операция | Где применена | Зачем |
|---|---|---|
| `ALTER USER mmuser IN DATABASE mattermost_test SET search_path = 'public'` | `10.210.39.149/mattermost_test` | Закрепляет схему по умолчанию для всех коннектов `mmuser`. |
| `PUT /api/v4/config`: `PluginSettings.Plugins.mattermost-ai.config.bots[0].model = "detmir-gpt"`, `reasoningEnabled = false`, `mcp.servers = []` | MM config store | Bot `matty` в новой БД был с пустой моделью и `reasoningEnabled=true`; `mcp.servers = {}` ломал unmarshal плагина. |
| `LogSettings.FileLevel = DEBUG` | MM config store | Нужно было увидеть stack trace panic'а из плагина. |
| `mmctl user change-password dbahtin --password 'DevLocal-2026!'` | БД `public.users` | Сброс пароля для моего собственного аккаунта, чтобы зайти в систему. |

## 3. Хронология проблем (и как каждая диагностирована)

### 3.1. Схема `mattermost` принята за рабочую
**Симптом:** `mmctl team list` возвращал 0 команд, `users=1`, хотя пользователь настаивал, что в БД есть рабочие данные.
**Диагностика:** `pg_stat_user_tables` показывал `posts.n_live_tup=10.5M` в `schema=public`, а не `mattermost` — я прочитал невнимательно и подумал, что live-данные в `mattermost`. При `SELECT count(*) FROM mattermost.posts` получил 0 (пустая схема), решил что данные удалены. Настоящий count — `SELECT count(*) FROM public.posts` = 10_582_141 — показал, что рабочая схема это `public`.
**Урок:** `n_live_tup` в `pg_stat_user_tables` может быть устаревшим, а колонка schema в широких запросах — легко спутать. Всегда уточнять `SELECT count(*) FROM <schema>.<table>`.

### 3.2. search_path не прокидывается через DSN
**Симптом:** DSN-параметры `?search_path=mattermost` и `?options=-c search_path=mattermost` (URL-encoded) MM-коннекты игнорировали, все запросы по-прежнему шли в `public`.
**Решение:** установить `search_path` на уровне роли БД: `ALTER ROLE mmuser IN DATABASE mattermost_test SET search_path TO public;`. Применяется ко всем новым коннектам без правок DSN.
**Урок:** lib/pq в Mattermost не всегда уважает `options` в URL-форме; persistent role-setting — самый надёжный путь.

### 3.3. Ошибка `llm_error` после переключения на внешнюю БД
**Симптом:** `POST /api/v4/posts/{id}/summary` → `500 app.thread_summary.llm_error` с пустым telemetry-body; плагин молчал в логах.
**Шаги диагностики:**
1. **Русские промпты как ложный след.** Пользователь правильно заметил, что переключение БД было до правки промптов. Прямым curl к `http://gpt-api.detmir.team:8000/v1/chat/completions` с русским содержимым и `response_format: json_schema` — 200 OK. Значит endpoint ни при чём.
2. **Пустое поле `model` и `reasoningEnabled=true`.** В новой БД конфиг плагина был не тот же, что в локальной dev БД: `bots[0].model = ""`, `reasoningEnabled=true`. Поправил через API — не помогло.
3. **Unmarshal error плагина.** В `plugin_stderr` нашлась строка `LoadPluginConfiguration API failed to unmarshal: json: cannot unmarshal object into Go struct field MCPConfig.config.mcp.servers of type []config.MCPServerConfig`. Плагин v1.14.0 ждёт `servers: []`, а в БД лежал `servers: {}`. Поправил через PUT-config → unmarshal error исчез, но 500 остался.
4. **Panic `bot.LLM() = nil`.** После включения `FileLevel=DEBUG` в логе появился stack-trace panic'а плагина: `runtime error: invalid memory address or nil pointer dereference` в `api/api_llm_bridge.go:258`. Строка 258 — `bot.LLM().ChatCompletionNoStream(...)`. Значит поле `llm` у Bot-структуры — nil.
5. **Корневая причина — user limit.** В `vendor/mattermost-plugin-ai@v1.14.0/bots/bots.go:299-322` при старте плагин зовёт `pluginAPI.Bot.UpdateActive(prevBot.UserId, true)`. Если возвращается error — `continue`, пропуская `getLLM`. В логе `Failed to update bot active: ERROR_SAFETY_LIMITS_EXCEEDED`. MM возвращает ошибку в `channels/app/user.go:1161-1174` — `isAtUserLimit() == true`, потому что `ActiveUserCount (946) > maxUsersLimit (200)`. В `channels/app/limits.go:13` лимит был захардкожен `maxUsersLimit = 200`. Поднял до 1_000_000 — плагин активировал бота, получил LLM-клиент, саммари поехал.

**Урок:** `bot.LLM()` в mattermost-plugin-ai может быть nil при любом сбое активации бота на старте. В наших фичах, которые его вызывают, имеет смысл добавить defensive check и возвращать более информативный error пользователю.

## 4. Ключевые места кода

- **Инициализация LLM бота** (где nil возникает): `~/go/pkg/mod/github.com/mattermost/mattermost-plugin-ai@v1.14.0/bots/bots.go:287-323` — цикл по `bots`, `UpdateActive → continue on error → getLLM not called`.
- **Panic при nil LLM**: `~/go/pkg/mod/github.com/mattermost/mattermost-plugin-ai@v1.14.0/api/api_llm_bridge.go:258`: `bot.LLM().ChatCompletionNoStream(...)`.
- **User limit enforce**: `/Users/dmitrybakhtin/WebstormProjects/mattermost/server/channels/app/user.go:1161-1174` (`UpdateActive`).
- **Хардкод лимита** (наш патч): `/Users/dmitrybakhtin/WebstormProjects/mattermost/server/channels/app/limits.go:12-15`.
- **Наша фича**: `/Users/dmitrybakhtin/WebstormProjects/mattermost/server/channels/app/thread_summary.go`, `/Users/dmitrybakhtin/WebstormProjects/mattermost/webapp/channels/src/components/thread_summary_panel/thread_summary_panel.tsx`.

## 5. Проверка end-to-end

1. `curl http://localhost:8065/api/v4/system/ping` → `{"status":"OK"}`.
2. `./bin/mmctl --local team list` → 12 команд.
3. Логин `dbahtin` / `DevLocal-2026!` в http://localhost:9005.
4. Открыть тред в канале, нажать ✨ Summarize → в RHS-панели приходит саммари на русском, ключевые моменты с кликабельными @username, корректный `thread_post_count`.
5. `curl -X POST /api/v4/posts/{post_id}/summary` → HTTP 200, JSON с полями `summary`, `key_points`, `participants`, `thread_post_count`, `model=ai-bridge`.

## 6. Откат (если потребуется вернуть всё обратно)

- DSN в `server/config/config.json:164` → `postgres://mmuser:mostest@localhost/mattermost_test?sslmode=disable&connect_timeout=10&binary_parameters=yes`.
- `server/config.override.mk:1` → вернуть `postgres` в `ENABLED_DOCKER_SERVICES`.
- `channels/app/limits.go:13-14` → вернуть `200` / `250`.
- `ALTER ROLE mmuser IN DATABASE mattermost_test RESET search_path` на `10.210.39.149`.
- `thread_summary.go:117-118` — можно откатить промпты на английский: `git checkout channels/app/thread_summary.go`.

## 7. Открытые вопросы / технический долг

- **Лицензия**: вариант A (повышение `maxUsersLimit`) — локальный dev-хак, не для prod. В prod нужна Team/Enterprise license с `Features.Users >= активных пользователей`.
- **Defensive check** в `server/channels/app/thread_summary.go`: перед `a.ch.agentsBridge.AgentCompletion(...)` стоит проверить, что бот реально имеет LLM-клиент, и возвращать `app.thread_summary.bot_not_initialized` (503) вместо опережающего panic в плагине и непонятного `llm_error` (500).
- **Feature flags** `EnableAIPluginBridge`, `EnableAIRecaps` в БД — `false`. На работу не влияет (`liveAgentsBridge` их не проверяет), но стоит включить для единообразия с документацией MM AI.
- **Персональный пароль `dbahtin`**: я установил `DevLocal-2026!` на shared-БД. Если этим аккаунтом пользуется ещё что-то (другие dev/CI) — верните прежний пароль или рассадите dev-инстанс в отдельную schema/БД.
