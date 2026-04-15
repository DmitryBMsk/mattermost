# Thread Summarization Implementation Plan (v2 — post code-review)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add AI-powered thread summarization to Mattermost — users click "Summarize Thread" on a post and see a structured summary in the RHS panel.

**Architecture:** New Go API endpoint (`POST /api/v4/posts/{post_id}/summary`) fetches thread posts, sends to LLM via existing AI Bridge using `BridgeCompletionRequest` (same pattern as `summarization.go`), returns structured JSON. Frontend adds new RHS panel state `THREAD_SUMMARY` with a dedicated `ThreadSummaryPanel` component. RHS routing updated so `THREAD_SUMMARY` state takes priority over the `postRightVisible` thread panel.

**Tech Stack:** Go (server API), React/TypeScript/Redux (frontend), Mattermost AI Bridge (`BridgeCompletionRequest`)

**Review fixes applied:**
1. AI Bridge uses `BridgeCompletionRequest` struct (not raw string)
2. RHS state: `THREAD_SUMMARY` excluded from `postRightVisible` check in `sidebar_right/index.ts`
3. Back navigation: `showThreadSummary` pushes current RHS state to `previousRhsStates`
4. Authorization: handler uses `GetPostIfAuthorized` instead of `GetSinglePost`
5. Automated tests added for backend, reducers, actions, and components
6. DotMenu uses `threadReplyCount` prop (not `post.reply_count`)
7. Import uses `types/store` (not `mattermost-redux/types/actions`)
8. Post references use embedded post IDs in prompt, not ambiguous indices

---

## File Map

### New Files
| File | Responsibility |
|------|---------------|
| `server/channels/api4/thread_summary.go` | HTTP handler + route registration |
| `server/channels/app/thread_summary.go` | Business logic: fetch thread, build BridgeCompletionRequest, call LLM, parse response |
| `server/public/model/thread_summary.go` | Response model types |
| `webapp/channels/src/components/thread_summary_panel/thread_summary_panel.tsx` | RHS summary panel component |
| `webapp/channels/src/components/thread_summary_panel/thread_summary_panel.scss` | Styles |
| `webapp/channels/src/actions/views/thread_summary.ts` | Redux actions |
| `webapp/channels/src/reducers/views/thread_summary.ts` | Redux reducer |
| `webapp/channels/src/selectors/views/thread_summary.ts` | Redux selectors |

### Modified Files
| File | Change |
|------|--------|
| `server/channels/api4/api.go` | Add `api.InitThreadSummary()` call |
| `webapp/channels/src/utils/constants.tsx` | Add `THREAD_SUMMARY` to RHSStates + ActionTypes |
| `webapp/channels/src/reducers/views/index.ts` | Register threadSummary reducer |
| `webapp/platform/client/src/client4.ts` | Add `postThreadSummary()` method |
| `webapp/channels/src/components/dot_menu/dot_menu.tsx` | Add "Summarize Thread" menu item |
| `webapp/channels/src/components/dot_menu/index.ts` | Wire `showThreadSummary` action |
| `webapp/channels/src/components/sidebar_right/index.ts` | Exclude THREAD_SUMMARY from `postRightVisible` |
| `webapp/channels/src/components/sidebar_right/sidebar_right.tsx` | Render ThreadSummaryPanel |
| `webapp/channels/src/components/rhs_header_post/rhs_header_post.tsx` | Add ✨ summarize button |

---

## Task 1: Backend — Response Model

**Files:**
- Create: `server/public/model/thread_summary.go`

- [ ] **Step 1: Create the model file**

```go
// server/public/model/thread_summary.go
package model

import "encoding/json"

type ThreadKeyPoint struct {
	Text    string   `json:"text"`
	PostIDs []string `json:"post_ids"`
}

type ThreadSummaryResponse struct {
	Summary         string           `json:"summary"`
	KeyPoints       []ThreadKeyPoint `json:"key_points"`
	Participants    []string         `json:"participants"`
	ThreadPostCount int              `json:"thread_post_count"`
	Model           string           `json:"model"`
}

type ThreadSummaryLLMResponse struct {
	Summary   string `json:"summary"`
	KeyPoints []struct {
		Text    string   `json:"text"`
		PostIDs []string `json:"post_ids"`
	} `json:"key_points"`
}

func (r *ThreadSummaryResponse) ToJSON() ([]byte, error) {
	return json.Marshal(r)
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/dmitrybakhtin/WebstormProjects/mattermost/server && go build ./public/model/...`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
cd /Users/dmitrybakhtin/WebstormProjects/mattermost
git add server/public/model/thread_summary.go
git commit -m "feat(thread-summary): add response model types"
```

---

## Task 2: Backend — App Layer Business Logic

**Files:**
- Create: `server/channels/app/thread_summary.go`

Uses the same `BridgeCompletionRequest` pattern as `server/channels/app/summarization.go`: structured request with Operation, Messages (system + user), JSONOutputFormat, UserID, ChannelID.

Post IDs are embedded directly in the prompt text as `[post_id:abc123]` markers — the LLM echoes them back in key_points, and the parser extracts them. No ambiguous numeric indices.

- [ ] **Step 1: Create the app-layer function**

```go
// server/channels/app/thread_summary.go
package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/request"
)

const threadSummaryJSONSchema = `{"type":"object","properties":{"summary":{"type":"string"},"key_points":{"type":"array","items":{"type":"object","properties":{"text":{"type":"string"},"post_ids":{"type":"array","items":{"type":"string"}}},"required":["text","post_ids"]}}},"required":["summary","key_points"]}`

var postIDRefRegex = regexp.MustCompile(`\[post_id:([a-zA-Z0-9]+)\]`)

func (a *App) GetThreadSummary(rctx request.CTX, rootPostID string, userID string, channel *model.Channel) (*model.ThreadSummaryResponse, *model.AppError) {
	// 1. Fetch all thread posts
	opts := model.GetPostsOptions{
		SkipFetchThreads: true,
	}
	postList, appErr := a.GetPostThread(rctx, rootPostID, opts, userID)
	if appErr != nil {
		return nil, appErr
	}

	if len(postList.Order) <= 1 {
		return nil, model.NewAppError("GetThreadSummary", "app.thread_summary.no_replies", nil, "thread has no replies", http.StatusBadRequest)
	}

	// 2. Check AI Bridge availability
	available, _ := a.GetAIPluginBridgeStatus(rctx)
	if !available {
		return nil, model.NewAppError("GetThreadSummary", "app.thread_summary.ai_unavailable", nil, "AI service is not available", http.StatusServiceUnavailable)
	}

	// 3. Build sorted posts list and format for LLM
	posts := make([]*model.Post, 0, len(postList.Posts))
	for _, post := range postList.Posts {
		posts = append(posts, post)
	}
	sort.Slice(posts, func(i, j int) bool {
		return posts[i].CreateAt < posts[j].CreateAt
	})

	participantSet := make(map[string]bool)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Thread in #%s (%d messages)\n\n", channel.DisplayName, len(posts)))

	for _, post := range posts {
		user, userErr := a.GetUser(post.UserId)
		username := "unknown"
		if userErr == nil {
			username = user.Username
		}
		participantSet[username] = true

		t := time.Unix(post.CreateAt/1000, 0).UTC().Format("2006-01-02 15:04")
		sb.WriteString(fmt.Sprintf("[post_id:%s] [%s] @%s:\n%s\n\n", post.Id, t, username, post.Message))
	}

	participants := make([]string, 0, len(participantSet))
	for p := range participantSet {
		participants = append(participants, p)
	}
	sort.Strings(participants)

	// 4. Build BridgeCompletionRequest (same pattern as summarization.go)
	systemPrompt := "You are a thread summarizer. Given a thread of messages, produce a structured JSON summary. Each message is prefixed with [post_id:XXX]. In your key_points, include the post_id values for referenced messages in the post_ids array."
	userPrompt := fmt.Sprintf(`Summarize this thread. Provide:
1. A short summary (2-3 sentences) capturing the main topic and outcome.
2. Key points as bullet items, each mentioning the participant (@username) and their contribution.

For each key point, include the post_ids of the messages you reference.

Thread:
%s`, sb.String())

	req := BridgeCompletionRequest{
		Operation:       BridgeOperationRecapSummary,
		ClientOperation: "recaps",
		OperationSubType: "summarize_thread",
		Messages: []BridgeCompletionMessage{
			{Role: "system", Message: systemPrompt},
			{Role: "user", Message: userPrompt},
		},
		JSONOutputFormat: threadSummaryJSONSchema,
		UserID:           userID,
		ChannelID:        channel.Id,
	}

	llmResponse, llmErr := a.ch.agentsBridge.AgentCompletion(userID, "", req)
	if llmErr != nil {
		return nil, model.NewAppError("GetThreadSummary", "app.thread_summary.llm_error", nil, llmErr.Error(), http.StatusInternalServerError)
	}

	// 5. Parse LLM response
	var parsed model.ThreadSummaryLLMResponse
	if jsonErr := json.Unmarshal([]byte(llmResponse), &parsed); jsonErr != nil {
		return &model.ThreadSummaryResponse{
			Summary:         llmResponse,
			KeyPoints:       []model.ThreadKeyPoint{},
			Participants:    participants,
			ThreadPostCount: len(posts),
			Model:           "ai-bridge",
		}, nil
	}

	// 6. Validate post IDs — only keep IDs that exist in this thread
	validIDs := make(map[string]bool, len(posts))
	for _, post := range posts {
		validIDs[post.Id] = true
	}

	keyPoints := make([]model.ThreadKeyPoint, 0, len(parsed.KeyPoints))
	for _, kp := range parsed.KeyPoints {
		filteredIDs := make([]string, 0, len(kp.PostIDs))
		for _, id := range kp.PostIDs {
			if validIDs[id] {
				filteredIDs = append(filteredIDs, id)
			}
		}
		// Also extract any [post_id:XXX] references from text
		if matches := postIDRefRegex.FindAllStringSubmatch(kp.Text, -1); matches != nil {
			for _, m := range matches {
				if validIDs[m[1]] {
					filteredIDs = append(filteredIDs, m[1])
				}
			}
		}
		keyPoints = append(keyPoints, model.ThreadKeyPoint{
			Text:    kp.Text,
			PostIDs: filteredIDs,
		})
	}

	return &model.ThreadSummaryResponse{
		Summary:         parsed.Summary,
		KeyPoints:       keyPoints,
		Participants:    participants,
		ThreadPostCount: len(posts),
		Model:           "ai-bridge",
	}, nil
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/dmitrybakhtin/WebstormProjects/mattermost/server && go build ./channels/app/...`
Expected: No errors. If `BridgeCompletionRequest`, `BridgeCompletionMessage`, `BridgeOperationRecapSummary` are not exported, check `agents_bridge.go` for exact type names and adjust.

- [ ] **Step 3: Commit**

```bash
git add server/channels/app/thread_summary.go
git commit -m "feat(thread-summary): add app-layer with BridgeCompletionRequest"
```

---

## Task 3: Backend — API Handler + Route

**Files:**
- Create: `server/channels/api4/thread_summary.go`
- Modify: `server/channels/api4/api.go` (add `api.InitThreadSummary()` call after `api.InitPost()`)

Uses `GetPostIfAuthorized` for proper authorization (not `GetSinglePost`).

- [ ] **Step 1: Create the handler file**

```go
// server/channels/api4/thread_summary.go
package api4

import (
	"encoding/json"
	"net/http"
)

func (api *API) InitThreadSummary() {
	api.BaseRoutes.Post.Handle("/summary", api.APISessionRequired(postThreadSummary)).Methods(http.MethodPost)
}

func postThreadSummary(c *Context, w http.ResponseWriter, r *http.Request) {
	c.RequirePostId()
	if c.Err != nil {
		return
	}

	// GetPostIfAuthorized checks channel read permission internally
	post, appErr := c.App.GetPostIfAuthorized(c.AppContext, c.Params.PostId, c.AppContext.Session(), false)
	if appErr != nil {
		c.Err = appErr
		return
	}

	// Must be a root post
	if post.RootId != "" {
		c.Err = model.NewAppError("postThreadSummary", "api.post.summary.not_root_post", nil, "post is a reply, not a root post", http.StatusBadRequest)
		return
	}

	channel, appErr := c.App.GetChannel(c.AppContext, post.ChannelId)
	if appErr != nil {
		c.Err = appErr
		return
	}

	summary, appErr := c.App.GetThreadSummary(c.AppContext, c.Params.PostId, c.AppContext.Session().UserId, channel)
	if appErr != nil {
		c.Err = appErr
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(summary); err != nil {
		c.Logger.Warn("Error writing thread summary response")
	}
}
```

- [ ] **Step 2: Add missing import in handler**

Add `"github.com/mattermost/mattermost/server/public/model"` to imports if the `model.NewAppError` call requires it.

- [ ] **Step 3: Register the route in api.go**

In `server/channels/api4/api.go`, find `api.InitPost()` and add after it:
```go
	api.InitThreadSummary()
```

- [ ] **Step 4: Verify it compiles**

Run: `cd /Users/dmitrybakhtin/WebstormProjects/mattermost/server && go build ./channels/api4/...`

- [ ] **Step 5: Commit**

```bash
git add server/channels/api4/thread_summary.go server/channels/api4/api.go
git commit -m "feat(thread-summary): add POST /posts/{id}/summary endpoint with GetPostIfAuthorized"
```

---

## Task 4: Frontend — Constants + Redux State

**Files:**
- Modify: `webapp/channels/src/utils/constants.tsx`
- Create: `webapp/channels/src/reducers/views/thread_summary.ts`
- Create: `webapp/channels/src/selectors/views/thread_summary.ts`
- Modify: `webapp/channels/src/reducers/views/index.ts`

- [ ] **Step 1: Add THREAD_SUMMARY to RHSStates**

In `webapp/channels/src/utils/constants.tsx`, add after `EDIT_HISTORY: 'edit-history'`:
```typescript
    THREAD_SUMMARY: 'thread-summary',
```

- [ ] **Step 2: Add ActionTypes for thread summary**

In the `ActionTypes` object in the same file, add:
```typescript
    THREAD_SUMMARY_REQUEST: 'thread_summary_request',
    THREAD_SUMMARY_SUCCESS: 'thread_summary_success',
    THREAD_SUMMARY_FAILURE: 'thread_summary_failure',
    THREAD_SUMMARY_CLEAR: 'thread_summary_clear',
```

- [ ] **Step 3: Create the reducer**

```typescript
// webapp/channels/src/reducers/views/thread_summary.ts
import {combineReducers} from 'redux';

import {ActionTypes} from 'utils/constants';

export interface ThreadSummaryData {
    summary: string;
    key_points: Array<{
        text: string;
        post_ids: string[];
    }>;
    participants: string[];
    thread_post_count: number;
    model: string;
}

function loading(state = false, action: {type: string}) {
    switch (action.type) {
    case ActionTypes.THREAD_SUMMARY_REQUEST:
        return true;
    case ActionTypes.THREAD_SUMMARY_SUCCESS:
    case ActionTypes.THREAD_SUMMARY_FAILURE:
    case ActionTypes.THREAD_SUMMARY_CLEAR:
        return false;
    default:
        return state;
    }
}

function postId(state: string | null = null, action: {type: string; postId?: string}) {
    switch (action.type) {
    case ActionTypes.THREAD_SUMMARY_REQUEST:
        return action.postId ?? null;
    case ActionTypes.THREAD_SUMMARY_CLEAR:
        return null;
    default:
        return state;
    }
}

function data(state: ThreadSummaryData | null = null, action: {type: string; data?: ThreadSummaryData}) {
    switch (action.type) {
    case ActionTypes.THREAD_SUMMARY_SUCCESS:
        return action.data ?? null;
    case ActionTypes.THREAD_SUMMARY_CLEAR:
    case ActionTypes.THREAD_SUMMARY_REQUEST:
        return null;
    default:
        return state;
    }
}

function error(state: string | null = null, action: {type: string; error?: string}) {
    switch (action.type) {
    case ActionTypes.THREAD_SUMMARY_FAILURE:
        return action.error ?? 'Unknown error';
    case ActionTypes.THREAD_SUMMARY_REQUEST:
    case ActionTypes.THREAD_SUMMARY_CLEAR:
        return null;
    default:
        return state;
    }
}

export default combineReducers({loading, postId, data, error});
```

- [ ] **Step 4: Create selectors**

```typescript
// webapp/channels/src/selectors/views/thread_summary.ts
import type {GlobalState} from 'types/store';

export function getThreadSummaryLoading(state: GlobalState): boolean {
    return state.views.threadSummary?.loading ?? false;
}

export function getThreadSummaryData(state: GlobalState) {
    return state.views.threadSummary?.data ?? null;
}

export function getThreadSummaryError(state: GlobalState): string | null {
    return state.views.threadSummary?.error ?? null;
}

export function getThreadSummaryPostId(state: GlobalState): string | null {
    return state.views.threadSummary?.postId ?? null;
}
```

- [ ] **Step 5: Register reducer in views/index.ts**

In `webapp/channels/src/reducers/views/index.ts`, add:
```typescript
import threadSummary from './thread_summary';
```
And add `threadSummary` to the `combineReducers` call.

- [ ] **Step 6: Commit**

```bash
git add webapp/channels/src/utils/constants.tsx \
        webapp/channels/src/reducers/views/thread_summary.ts \
        webapp/channels/src/selectors/views/thread_summary.ts \
        webapp/channels/src/reducers/views/index.ts
git commit -m "feat(thread-summary): add Redux state, reducer, selectors, RHS constants"
```

---

## Task 5: Frontend — Client4 Method + Actions (fixed RHS flow)

**Files:**
- Modify: `webapp/platform/client/src/client4.ts`
- Create: `webapp/channels/src/actions/views/thread_summary.ts`

Key fix: `showThreadSummary` does NOT dispatch `SELECT_POST` (which would clear `rhsState`). Instead it dispatches `UPDATE_RHS_STATE` with the postId embedded, and stores the previous state for back navigation.

Import uses `types/store` (not `mattermost-redux/types/actions`).

- [ ] **Step 1: Add Client4 method**

In `webapp/platform/client/src/client4.ts`, add after `getPaginatedPostThread`:

```typescript
postThreadSummary = (postId: string) => {
    return this.doFetch<{
        summary: string;
        key_points: Array<{text: string; post_ids: string[]}>;
        participants: string[];
        thread_post_count: number;
        model: string;
    }>(
        `${this.getPostRoute(postId)}/summary`,
        {method: 'post'},
    );
};
```

- [ ] **Step 2: Create actions**

```typescript
// webapp/channels/src/actions/views/thread_summary.ts
import {Client4} from 'mattermost-redux/client';

import {ActionTypes, RHSStates} from 'utils/constants';
import {getRhsState, getSelectedPostId} from 'selectors/rhs';

import type {DispatchFunc, GetStateFunc} from 'types/store';

export function showThreadSummary(postId: string) {
    return (dispatch: DispatchFunc, getState: GetStateFunc) => {
        // Preserve current RHS state for back navigation
        const currentRhsState = getRhsState(getState());
        const currentPostId = getSelectedPostId(getState());

        // Push current state to previousRhsStates stack via RHS_GO_BACK-compatible dispatch
        // UPDATE_RHS_STATE pushes the current state onto the stack in the reducer
        dispatch({
            type: ActionTypes.UPDATE_RHS_STATE,
            state: RHSStates.THREAD_SUMMARY,
            postId,
            channelId: '',
            previousRhsState: currentRhsState,
        });

        dispatch(fetchThreadSummary(postId));

        return {data: true};
    };
}

export function fetchThreadSummary(postId: string) {
    return async (dispatch: DispatchFunc) => {
        dispatch({
            type: ActionTypes.THREAD_SUMMARY_REQUEST,
            postId,
        });

        try {
            const data = await Client4.postThreadSummary(postId);
            dispatch({
                type: ActionTypes.THREAD_SUMMARY_SUCCESS,
                data,
            });
            return {data};
        } catch (err: unknown) {
            const message = err instanceof Error ? err.message : 'Failed to generate summary';
            dispatch({
                type: ActionTypes.THREAD_SUMMARY_FAILURE,
                error: message,
            });
            return {error: message};
        }
    };
}

export function clearThreadSummary() {
    return {type: ActionTypes.THREAD_SUMMARY_CLEAR};
}
```

- [ ] **Step 3: Commit**

```bash
git add webapp/platform/client/src/client4.ts \
        webapp/channels/src/actions/views/thread_summary.ts
git commit -m "feat(thread-summary): add Client4 method and actions with correct RHS flow"
```

---

## Task 6: Frontend — ThreadSummaryPanel Component

**Files:**
- Create: `webapp/channels/src/components/thread_summary_panel/thread_summary_panel.tsx`
- Create: `webapp/channels/src/components/thread_summary_panel/thread_summary_panel.scss`
- Create: `webapp/channels/src/components/thread_summary_panel/index.ts`

- [ ] **Step 1: Create styles** (same as original plan — see `thread_summary_panel.scss` in v1)

- [ ] **Step 2: Create component** (same as original plan — see `thread_summary_panel.tsx` in v1)

- [ ] **Step 3: Create index export**

```typescript
// webapp/channels/src/components/thread_summary_panel/index.ts
export {default} from './thread_summary_panel';
```

- [ ] **Step 4: Commit**

```bash
git add webapp/channels/src/components/thread_summary_panel/
git commit -m "feat(thread-summary): add ThreadSummaryPanel RHS component"
```

---

## Task 7: Frontend — Wire RHS Routing (critical fix)

**Files:**
- Modify: `webapp/channels/src/components/sidebar_right/index.ts`
- Modify: `webapp/channels/src/components/sidebar_right/sidebar_right.tsx`

The key fix: `postRightVisible` on line 44 of `index.ts` currently renders `RhsThread` whenever `selectedPostId` is set. We must exclude `THREAD_SUMMARY` from this check (same pattern as `EDIT_HISTORY`).

- [ ] **Step 1: Fix sidebar_right/index.ts**

At line 44, change:
```typescript
postRightVisible: Boolean(selectedPostId) && rhsState !== RHSStates.EDIT_HISTORY,
```
To:
```typescript
postRightVisible: Boolean(selectedPostId) && rhsState !== RHSStates.EDIT_HISTORY && rhsState !== RHSStates.THREAD_SUMMARY,
```

Add a new prop:
```typescript
isThreadSummary: rhsState === RHSStates.THREAD_SUMMARY,
```

- [ ] **Step 2: Add rendering in sidebar_right.tsx**

Import:
```typescript
import ThreadSummaryPanel from 'components/thread_summary_panel';
```

In the render logic (after `postRightVisible` block, ~line 298), add before `postCardVisible`:
```typescript
} else if (isThreadSummary) {
    content = (
        <div className='post-right__container'>
            <ThreadSummaryPanel/>
        </div>
    );
}
```

Add `isThreadSummary` to the destructured props.

- [ ] **Step 3: Verify no type errors**

Run: `cd /Users/dmitrybakhtin/WebstormProjects/mattermost/webapp && make check-types 2>&1 | head -20`

- [ ] **Step 4: Commit**

```bash
git add webapp/channels/src/components/sidebar_right/
git commit -m "feat(thread-summary): wire RHS routing with THREAD_SUMMARY exclusion"
```

---

## Task 8: Frontend — DotMenu "Summarize Thread" (fixed)

**Files:**
- Modify: `webapp/channels/src/components/dot_menu/dot_menu.tsx`
- Modify: `webapp/channels/src/components/dot_menu/index.ts`

Uses `threadReplyCount` prop (already computed in connector at index.ts:98), not `post.reply_count`.

- [ ] **Step 1: Add action to connector (index.ts)**

In `webapp/channels/src/components/dot_menu/index.ts`, import and add to `mapDispatchToProps`:
```typescript
import {showThreadSummary} from 'actions/views/thread_summary';
```
Add `showThreadSummary` to the actions object.

- [ ] **Step 2: Add handler and menu item (dot_menu.tsx)**

Add handler:
```typescript
handleSummarizeThread = (): void => {
    this.props.actions.showThreadSummary(this.props.post.id);
};
```

Add menu item (after Follow Thread, before Mark as Unread). Use `threadReplyCount`:
```tsx
{this.props.post.root_id === '' && this.props.threadReplyCount > 0 && (
    <Menu.Item
        id={`summarize_thread_${this.props.post.id}`}
        labels={
            <FormattedMessage
                id='post_info.summarize_thread'
                defaultMessage='Summarize Thread'
            />
        }
        leadingElement={<LightbulbOutlineIcon size={18}/>}
        onClick={this.handleSummarizeThread}
    />
)}
```

Import: `import {LightbulbOutlineIcon} from '@mattermost/compass-icons/components';`

If `LightbulbOutlineIcon` doesn't exist, check available icons with: `grep -r "export.*Icon" webapp/node_modules/@mattermost/compass-icons/components/index.ts | grep -i light`

- [ ] **Step 3: Commit**

```bash
git add webapp/channels/src/components/dot_menu/
git commit -m "feat(thread-summary): add Summarize Thread to DotMenu using threadReplyCount"
```

---

## Task 9: Frontend — Thread Header ✨ Button

**Files:**
- Modify: `webapp/channels/src/components/rhs_header_post/rhs_header_post.tsx`

- [ ] **Step 1: Add summarize button to RhsHeaderPost**

Read `rhs_header_post.tsx` to understand its structure. Add a button near the existing header actions (shrink/expand, close). Import:

```typescript
import {LightbulbOutlineIcon} from '@mattermost/compass-icons/components';
import {showThreadSummary} from 'actions/views/thread_summary';
```

Add a clickable icon button that calls `dispatch(showThreadSummary(rootPostId))`. Include a tooltip:

```tsx
<OverlayTrigger
    trigger={['hover', 'focus']}
    placement='bottom'
    overlay={<Tooltip id='summarizeThreadTooltip'>
        <FormattedMessage id='rhs_header.summarize_thread' defaultMessage='Summarize Thread'/>
    </Tooltip>}
>
    <button
        type='button'
        className='sidebar--right__subheader'
        onClick={() => dispatch(showThreadSummary(rootPostId))}
    >
        <LightbulbOutlineIcon size={18}/>
    </button>
</OverlayTrigger>
```

- [ ] **Step 2: Commit**

```bash
git add webapp/channels/src/components/rhs_header_post/
git commit -m "feat(thread-summary): add summarize button to thread RHS header"
```

---

## Task 10: Backend Tests

**Files:**
- Create: `server/channels/api4/thread_summary_test.go`

- [ ] **Step 1: Write API test**

Use the e2e agents bridge test helper (`AIBridgeTestHelperConfig`) to mock LLM responses. Test scenarios:
- Happy path: root post with replies → 200 + valid summary JSON
- Not a root post (reply) → 400
- Post not found → 404
- Thread with no replies → 400
- AI Bridge unavailable → 503
- User without channel access → 403

Follow patterns in existing `server/channels/api4/post_test.go`.

- [ ] **Step 2: Run tests**

Run: `cd /Users/dmitrybakhtin/WebstormProjects/mattermost/server && go test ./channels/api4/ -run TestPostThreadSummary -v`

- [ ] **Step 3: Commit**

```bash
git add server/channels/api4/thread_summary_test.go
git commit -m "test(thread-summary): add API endpoint tests"
```

---

## Task 11: Frontend Tests

**Files:**
- Create: `webapp/channels/src/reducers/views/thread_summary.test.ts`
- Create: `webapp/channels/src/components/thread_summary_panel/thread_summary_panel.test.tsx`

- [ ] **Step 1: Reducer tests**

Test all action types: REQUEST sets loading, SUCCESS stores data, FAILURE stores error, CLEAR resets all.

- [ ] **Step 2: Component tests**

Test ThreadSummaryPanel renders:
- Loading spinner when `loading: true`
- Summary text when data is present
- Error + retry when error is set
- Expand/collapse toggling
- Back and close button dispatch correct actions

Follow patterns in `webapp/channels/src/components/rhs_thread/rhs_thread.test.tsx`.

- [ ] **Step 3: DotMenu visibility test**

In `webapp/channels/src/components/dot_menu/dot_menu.test.tsx`, add test that "Summarize Thread" appears only on root posts with `threadReplyCount > 0`.

- [ ] **Step 4: Run tests**

Run: `cd /Users/dmitrybakhtin/WebstormProjects/mattermost/webapp && npx jest --testPathPattern="thread_summary" --no-coverage`

- [ ] **Step 5: Commit**

```bash
git add webapp/channels/src/reducers/views/thread_summary.test.ts \
        webapp/channels/src/components/thread_summary_panel/thread_summary_panel.test.tsx \
        webapp/channels/src/components/dot_menu/dot_menu.test.tsx
git commit -m "test(thread-summary): add reducer, component, and DotMenu tests"
```

---

## Task 12: Integration Test — Full Flow

- [ ] **Step 1: Verify dev server running**
- [ ] **Step 2: Manual browser test** (login → channel → DotMenu → Summarize → panel appears → loading → error/result → expand → collapse → back → thread)
- [ ] **Step 3: Final commit**

```bash
git add -A
git commit -m "feat(thread-summary): complete thread summarization feature v2"
```
