# AI Recaps — сетап и использование

AI Recaps — встроенная в MM (v11.x) фича: пользователь выбирает каналы, нажимает «создать Recap», MM асинхронно берёт свежую переписку из этих каналов и через LLM возвращает структурированный отчёт: **highlights** (ключевые события) + **action items** (задачи) + ссылки на исходные посты. Работает поверх того же AI Bridge, что и саммари тредов.

Фича спрятана за feature-флагом `EnableAIRecaps` и в дистрибутиве MM по умолчанию выключена.

## 1. Что уже есть в коде

| Часть | Где |
|---|---|
| API handlers | `/Users/dmitrybakhtin/WebstormProjects/mattermost/server/channels/api4/recap.go` — `POST /api/v4/recaps`, `GET /api/v4/recaps`, `GET /api/v4/recaps/:id`, `POST /api/v4/recaps/:id/read`, `POST /api/v4/recaps/:id/regenerate`, `DELETE /api/v4/recaps/:id` |
| Async worker | `/Users/dmitrybakhtin/WebstormProjects/mattermost/server/channels/jobs/recap/worker.go` — сам запускается при `EnableAIRecaps=true` |
| Business logic | `/Users/dmitrybakhtin/WebstormProjects/mattermost/server/channels/app/recap.go` — `ProcessRecapChannel(...)` дергает `agentsBridge.AgentCompletion` и пишет результат в таблицы `recaps` / `recap_channels` |
| Модель | `/Users/dmitrybakhtin/WebstormProjects/mattermost/server/public/model/recap.go` — `Recap`, `RecapChannel`, `CreateRecapRequest`, `AIRecapSummaryResponse` |
| Feature flag | `/Users/dmitrybakhtin/WebstormProjects/mattermost/server/public/model/feature_flags.go:95` — `EnableAIRecaps bool` (default `false`) |
| Redux actions/reducers | `/Users/dmitrybakhtin/WebstormProjects/mattermost/webapp/channels/src/packages/mattermost-redux/src/actions/recaps.ts`, `.../reducers/entities/recaps.ts`, `.../selectors/entities/recaps.ts` |
| UI компоненты | `/Users/dmitrybakhtin/WebstormProjects/mattermost/webapp/channels/src/components/recaps/` (`recaps.tsx`, `recap_item.tsx`, `recap_channel_card.tsx`, `recap_processing.tsx`, `recap_menu.tsx`), `.../create_recap_modal/`, `.../recaps_link/` |

То есть — всё уже в репозитории, включая webapp. Нужно только поднять флаг.

## 2. Включение флага

### 2.1. Persistent (persist между перезапусками)

В нашем dev-окружении добавлено в `/Users/dmitrybakhtin/WebstormProjects/mattermost/server/config.override.mk`:

```make
ENABLED_DOCKER_SERVICES = inbucket redis minio
RUN_SERVER_IN_BACKGROUND = true

export MM_FEATUREFLAGS_ENABLEAIRECAPS = true
export MM_FEATUREFLAGS_ENABLEAIPLUGINBRIDGE = true
```

При каждом `make run-server` эти env vars подхватятся.

Проверка (под system_admin):

```bash
TOKEN=$(curl -s -D - -X POST http://localhost:8065/api/v4/users/login \
  -H 'Content-Type: application/json' \
  -d '{"login_id":"dbahtin","password":"DevLocal-2026!"}' \
  | grep -i '^token:' | awk '{print $2}' | tr -d '\r')

curl -s http://localhost:8065/api/v4/config -H "Authorization: Bearer $TOKEN" \
  | python3 -c "import json,sys; ff=json.load(sys.stdin)['FeatureFlags']; \
print('EnableAIRecaps=', ff['EnableAIRecaps']); print('EnableAIPluginBridge=', ff['EnableAIPluginBridge'])"
# ожидание: True / True
```

### 2.2. Ad-hoc для одного запуска

```bash
cd /Users/dmitrybakhtin/WebstormProjects/mattermost/server
MM_FEATUREFLAGS_ENABLEAIRECAPS=true \
MM_FEATUREFLAGS_ENABLEAIPLUGINBRIDGE=true \
make run-server
```

### 2.3. Почему не через /system_console UI

Feature flags в MM **read-only через `/api/v4/config` PUT** — это по дизайну (они контролируются операционно через env, чтобы роллауты были управляемы). Так что UI/API способ "на лету" не работает, нужен рестарт с env.

## 3. Зависимости (должны быть выполнены до включения флага)

1. **AI-плагин работает** — см. `/Users/dmitrybakhtin/WebstormProjects/mattermost/docs/plans/ai-plugin-setup.md`. Recaps используют тот же `agentsBridge.AgentCompletion`, что и Thread Summary; если LLM-бот не инициализирован, Recap job упадёт с тем же `nil bot.LLM()` panic.
2. **Default bot задан** — в `System Console → Plugins → Agents` отмечен один бот как default. Worker берёт его, когда `agent_id` в запросе пуст.
3. **User limit** — если в БД больше active users чем `maxUsersLimit`, см. [ai-plugin-setup.md §5](/Users/dmitrybakhtin/WebstormProjects/mattermost/docs/plans/ai-plugin-setup.md). Recaps будут молча фейлить на activate-бота этапе.

## 4. Использование — через UI

1. Открыть http://localhost:9005, зайти под обычным юзером.
2. В левом сайдбаре над списком каналов появится линк **«Recaps»** (компонент `recaps_link`). Если его не видно — это значит feature flag не долетел до клиента: сделайте hard-refresh (Ctrl/Cmd+Shift+R), webapp читает его из `/api/v4/config/client?format=old`.
3. По клику — открывается страница Recaps (`components/recaps/recaps.tsx`). В пустом состоянии — кнопка **«Create Recap»**.
4. Модалка `CreateRecapModal`: дать заголовок, выбрать 1-10 каналов, (опц.) агента. `Submit` — модалка закроется, карточка Recap появится в списке со статусом `processing`.
5. Worker асинхронно дергает LLM по каждому каналу (1-5 минут на канал в зависимости от активности). По WebSocket приходит событие `recap_updated` — UI обновляет карточку.
6. Результат: карточка Recap разворачивается, показывая по каналам блоки `Highlights` и `Action items` с кликабельными ссылками на исходные сообщения.

## 5. Использование — через REST API (без UI)

```bash
TOKEN=...  # см. раздел 2

# Создать recap: все каналы моей команды за последние 24ч, default bot
curl -s -X POST http://localhost:8065/api/v4/recaps \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "title": "Утро понедельника",
    "channel_ids": ["<channel_id_1>","<channel_id_2>"],
    "agent_id": ""
  }'
# ожидание: HTTP 200, JSON с Recap{id, status:"pending", ...}

RECAP_ID="<из ответа>"

# Опросить статус
curl -s http://localhost:8065/api/v4/recaps/$RECAP_ID \
  -H "Authorization: Bearer $TOKEN"
# через 1-5 минут:  status:"completed", channels:[{highlights:[...], action_items:[...]}]

# Перегенерить (например, после правки промпта)
curl -s -X POST http://localhost:8065/api/v4/recaps/$RECAP_ID/regenerate \
  -H "Authorization: Bearer $TOKEN"

# Удалить
curl -s -X DELETE http://localhost:8065/api/v4/recaps/$RECAP_ID \
  -H "Authorization: Bearer $TOKEN"
```

Channel IDs можно взять через `./bin/mmctl --local channel search <term>` или `GET /api/v4/teams/{team_id}/channels`.

## 6. Таблицы в БД

При первом создании Recap сервер создаёт две таблицы (через миграцию morph):

- `recaps` — top-level запись (`id`, `user_id`, `title`, `status`, `bot_id`, timestamps, `total_message_count`, `read_at`).
- `recap_channels` — per-channel результат (`recap_id`, `channel_id`, `highlights` jsonb, `action_items` jsonb, `source_post_ids` jsonb, `create_at`).

Хранение в `public.recaps` / `public.recap_channels` (schema — та же где MM основной).

## 7. Диагностика

| Симптом | Причина |
|---|---|
| В UI нет линка «Recaps» в сайдбаре | Hard-refresh не применён; либо `EnableAIRecaps=false` в `/api/v4/config/client`; либо фича отключена для конкретного юзера через `DisabledUsers`/acl |
| `POST /api/v4/recaps` → 501 `api.recap.disabled.app_error` | Флаг не долетел до backend'а. Перезапустите MM с env var. |
| Recap долго висит в `processing` | Worker не запущен. В логе должно быть `Starting Worker  worker=Recap`. Если нет — `EnableAIRecaps` не активировался или jobserver отключён (`JobSettings.RunJobs=false`). |
| Recap → `status=failed` | Worker поймал error от `ProcessRecapChannel`. Смотрите `tail -f /Users/dmitrybakhtin/WebstormProjects/mattermost/server/logs/mattermost.log | grep -i recap`. Чаще всего — `nil bot.LLM()` (см. ai-plugin-setup §5) или 500 от LLM. |
| Recap `completed`, но highlights пустые | LLM не смог распарсить формат или ответил пустым JSON. Включите `LogSettings.FileLevel = DEBUG`, плюс `PluginSettings.Plugins.mattermost-ai.config.enableLLMTrace = true`, и повторите — в логе будет полный request/response. |

## 8. Откат

```make
# /Users/dmitrybakhtin/WebstormProjects/mattermost/server/config.override.mk — убрать две export-строки
ENABLED_DOCKER_SERVICES = inbucket redis minio
RUN_SERVER_IN_BACKGROUND = true
```

`make run-server` перезапустит MM без флагов. Существующие recaps в БД останутся, просто эндпоинты станут отвечать 501.

## 9. Технический долг / follow-ups

- Для recaps по русским каналам промпт внутри `server/channels/app/recap.go` — свой, отдельно от thread_summary. Если фича уйдёт в продакшен с русским контентом — его тоже стоит перевести (сейчас промпт английский по аналогии с upstream).
- В worker.go нет учёта `maxRecapMessagesPerChannel` / per-bot token budget — для больших каналов может быть дорого по токенам. Когда фича пойдёт в прод, добавить лимит (подобно `maxThreadPostsForSummary = 200` в thread_summary).
- `requireRecapsEnabled` отдаёт 501, что некрасиво для UI. На клиенте линк уже не показываем, но REST-клиенты получают неочевидное. Можно заменить на 403/`api.recap.disabled`.
