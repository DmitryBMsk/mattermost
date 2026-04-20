# Thread Summarization Feature — Design Spec

**Date:** 2026-04-15
**Branch:** `feature/thread-summarization`
**Status:** Approved

## Overview

AI-powered thread summarization for Mattermost, inspired by Slack AI. Users can summarize long threads with one click, seeing a structured summary in the right-hand side (RHS) panel with a short overview and expandable details.

## User Flow

1. User sees a thread with many replies in a channel
2. User clicks "Summarize Thread" via **DotMenu ("...")** on the root post, or **✨ button** in the thread RHS header
3. RHS panel switches to Thread Summary view (with loading state)
4. Backend fetches all thread posts, sends to LLM via AI Bridge, returns structured summary
5. Summary panel shows: short summary (always visible) + expandable detailed bullet points with @mentions and post references
6. "Back" button returns to thread view; "Close" closes RHS entirely
7. Footer: disclaimer + thumbs up/down feedback (visual only for MVP)

## Architecture

### Data Flow

```
User trigger (DotMenu or RHS header button)
  → dispatch(showThreadSummary(rootPostId))
  → RHS state → THREAD_SUMMARY, postId saved
  → ThreadSummaryPanel mounts in RHS
  → useEffect → dispatch(fetchThreadSummary(rootPostId))
  → Client4.postThreadSummary(rootPostId)
  → Go API: POST /api/v4/posts/{post_id}/summary
  → App.GetPostThread() → format posts → AI Bridge ServiceCompletion()
  → JSON response → Redux thread_summary slice → panel re-renders
```

## Frontend

### New Files

| File | Purpose |
|------|---------|
| `webapp/channels/src/components/thread_summary_panel/thread_summary_panel.tsx` | Main RHS panel component |
| `webapp/channels/src/components/thread_summary_panel/thread_summary_panel.scss` | Styles |
| `webapp/channels/src/components/thread_summary_panel/index.ts` | Export |
| `webapp/channels/src/actions/views/thread_summary.ts` | Actions: fetch, show, clear |
| `webapp/channels/src/reducers/views/thread_summary.ts` | State: loading, data, error, postId |
| `webapp/channels/src/selectors/views/thread_summary.ts` | Selectors |

### Modified Files

| File | Change |
|------|--------|
| `webapp/channels/src/components/dot_menu/dot_menu.tsx` | Add "Summarize Thread" menu item (visible only on root posts with ≥1 reply) |
| `webapp/channels/src/components/rhs_thread/rhs_thread.tsx` | Add ✨ summarize button in thread header |
| `webapp/channels/src/reducers/views/rhs.ts` | Add `RHSStates.THREAD_SUMMARY` |
| `webapp/channels/src/utils/constants.tsx` | Add `THREAD_SUMMARY` to RHSStates enum |
| `webapp/platform/client/src/client4.ts` | Add `postThreadSummary(postId)` method |

### ThreadSummaryPanel Component

```
┌─────────────────────────────────┐
│ ← Back    AI Summary         ✕  │  ← Header with back/close
├─────────────────────────────────┤
│ Summary of thread in #channel   │  ← Title + date range
│ Apr 15                          │
│                                 │
│ @adam.torres started a thread   │  ← Short summary (2-3 sentences)
│ about X. The team discussed Y   │     Always visible
│ and decided on Z.               │
│                                 │
│ ▸ More detail                   │  ← Expandable section
│ ┌─────────────────────────────┐ │
│ │ • @adam.torres proposed X   │ │  ← Bullet points with @mentions
│ │ • @marie.carpenter asked    │ │
│ │   about Y [1]               │ │  ← Post reference links
│ │ • @user-1 confirmed Z [2]  │ │
│ └─────────────────────────────┘ │
│                                 │
│ ─────────────────────────────── │
│ ⚠ AI-generated • may be        │  ← Disclaimer
│   inaccurate                    │
│ 👍 👎                           │  ← Feedback (visual only MVP)
└─────────────────────────────────┘
```

### Redux State Shape

```typescript
interface ThreadSummaryState {
  loading: boolean;
  postId: string | null;
  data: ThreadSummaryData | null;
  error: string | null;
}

interface ThreadSummaryData {
  summary: string;
  key_points: Array<{
    text: string;
    post_ids: string[];
  }>;
  participants: string[];
  thread_post_count: number;
  model: string;
}
```

### RHS Integration

Add `THREAD_SUMMARY` to `RHSStates` constants. In the RHS controller component, when `rhsState === RHSStates.THREAD_SUMMARY`, render `ThreadSummaryPanel` instead of `RhsThread`. The `selectedPostId` in RHS state identifies which thread is being summarized. Back button uses `previousRhsStates` stack to return to thread.

## Backend

### New Files

| File | Purpose |
|------|---------|
| `server/channels/api4/thread_summary.go` | HTTP handler |
| `server/channels/app/thread_summary.go` | Business logic |

### API Endpoint

**`POST /api/v4/posts/{post_id}/summary`**

- Auth: `APISessionRequired` — user must have read access to the post's channel
- Rate limit: Use existing per-user rate limiting
- Request body: none (post_id from URL)

### Handler Flow

```go
func postThreadSummary(c *Context, w http.ResponseWriter, r *http.Request) {
    // 1. Validate post_id parameter
    // 2. Get post, verify it exists and user has channel access
    // 3. Verify AI Bridge is available (agentsBridge != nil)
    // 4. Fetch thread via GetPostThread()
    // 5. Format posts into LLM prompt
    // 6. Call ServiceCompletion() via AI Bridge
    // 7. Parse LLM response into ThreadSummaryResponse
    // 8. Return JSON
}
```

### Post Formatting for LLM

```
Thread in #channel-name (7 messages, 5 participants)

[2026-04-15 08:55] @adam.torres:
veritatis illum aliquid dolor commodi itaque quisquam unde.

[2026-04-15 08:59] @marie.carpenter:
• nemo
• non
• molestias
• consectetur

[2026-04-15 09:01] @guest:
eveniet ipsam tenetur aut sequi est quia...
```

### LLM Prompt

```
Summarize this thread discussion. Provide:
1. A short summary (2-3 sentences) capturing the main topic and outcome
2. Key points as bullet items, each mentioning the participant (@username) and their contribution. Reference the message number in brackets like [1], [2].

Format your response as JSON:
{
  "summary": "...",
  "key_points": [
    {"text": "@username did X [1]", "post_ids_indices": [0]}
  ]
}

Thread:
{formatted_posts}
```

### Response Model

```go
type ThreadSummaryResponse struct {
    Summary        string              `json:"summary"`
    KeyPoints      []ThreadKeyPoint    `json:"key_points"`
    Participants   []string            `json:"participants"`
    ThreadPostCount int               `json:"thread_post_count"`
    Model          string              `json:"model"`
}

type ThreadKeyPoint struct {
    Text    string   `json:"text"`
    PostIDs []string `json:"post_ids"`
}
```

### Error Handling

| Scenario | HTTP Status | Error ID |
|----------|-------------|----------|
| Invalid post ID | 400 | `api.post.summary.invalid_id` |
| Post not found | 404 | `api.post.summary.not_found` |
| No channel access | 403 | `api.post.summary.forbidden` |
| AI Bridge unavailable | 503 | `api.post.summary.service_unavailable` |
| Post has no replies | 400 | `api.post.summary.no_replies` |
| LLM error | 500 | `api.post.summary.generation_failed` |

### Route Registration

In `channels/api4/api.go` `Init()`, add call to `InitThreadSummary()`.
In `thread_summary.go`:
```go
func (api *API) InitThreadSummary() {
    api.BaseRoutes.Post.Handle("/summary",
        api.APISessionRequired(postThreadSummary)).Methods(http.MethodPost)
}
```

## Out of Scope (MVP)

- Caching summaries (each request generates fresh)
- Streaming LLM response
- User-selectable model
- DM/group message summarization
- Translation of summaries
- Persistent storage of summaries
- Admin controls beyond AI Bridge config
