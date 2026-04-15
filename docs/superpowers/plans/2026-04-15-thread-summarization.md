# Thread Summarization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add AI-powered thread summarization to Mattermost — users click "Summarize Thread" on a post and see a structured summary in the RHS panel.

**Architecture:** New Go API endpoint (`POST /api/v4/posts/{post_id}/summary`) fetches thread posts, sends to LLM via existing AI Bridge, returns structured JSON. Frontend adds new RHS panel state `THREAD_SUMMARY` with a dedicated `ThreadSummaryPanel` component, triggered from DotMenu and thread header.

**Tech Stack:** Go (server API), React/TypeScript/Redux (frontend), Mattermost AI Bridge (LLM integration)

---

## File Map

### New Files
| File | Responsibility |
|------|---------------|
| `server/channels/api4/thread_summary.go` | HTTP handler + route registration |
| `server/channels/app/thread_summary.go` | Business logic: fetch thread, format, call LLM, parse response |
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
| `webapp/channels/src/utils/constants.tsx` | Add `THREAD_SUMMARY` to RHSStates |
| `webapp/channels/src/reducers/views/index.ts` | Register threadSummary reducer |
| `webapp/platform/client/src/client4.ts` | Add `postThreadSummary()` method |
| `webapp/channels/src/components/dot_menu/dot_menu.tsx` | Add "Summarize Thread" menu item |
| `webapp/channels/src/components/rhs_thread/rhs_thread.tsx` | Add ✨ button in thread header |

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
	Summary         string          `json:"summary"`
	KeyPoints       []ThreadKeyPoint `json:"key_points"`
	Participants    []string        `json:"participants"`
	ThreadPostCount int             `json:"thread_post_count"`
	Model           string          `json:"model"`
}

type ThreadSummaryLLMResponse struct {
	Summary   string `json:"summary"`
	KeyPoints []struct {
		Text            string `json:"text"`
		PostIdsIndices  []int  `json:"post_ids_indices"`
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

- [ ] **Step 1: Create the app-layer function**

```go
// server/channels/app/thread_summary.go
package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/request"
)

func (a *App) GetThreadSummary(rctx request.CTX, rootPostID string, userID string) (*model.ThreadSummaryResponse, *model.AppError) {
	// 1. Fetch the root post
	rootPost, err := a.GetSinglePost(rctx, rootPostID, false)
	if err != nil {
		return nil, err
	}

	// 2. Verify it's a root post with replies
	if rootPost.RootId != "" {
		return nil, model.NewAppError("GetThreadSummary", "app.thread_summary.not_root_post", nil, "post is a reply, not a root post", http.StatusBadRequest)
	}

	// 3. Fetch all thread posts
	opts := model.GetPostsOptions{
		SkipFetchThreads: true,
	}
	postList, err := a.GetPostThread(rctx, rootPostID, opts, userID)
	if err != nil {
		return nil, err
	}

	if len(postList.Order) <= 1 {
		return nil, model.NewAppError("GetThreadSummary", "app.thread_summary.no_replies", nil, "thread has no replies", http.StatusBadRequest)
	}

	// 4. Get channel for context
	channel, err := a.GetChannel(rctx, rootPost.ChannelId)
	if err != nil {
		return nil, err
	}

	// 5. Build sorted posts list and format for LLM
	posts := make([]*model.Post, 0, len(postList.Posts))
	for _, post := range postList.Posts {
		posts = append(posts, post)
	}
	sort.Slice(posts, func(i, j int) bool {
		return posts[i].CreateAt < posts[j].CreateAt
	})

	// Collect participants and build formatted text
	participantSet := make(map[string]bool)
	postIDs := make([]string, 0, len(posts))
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Thread in #%s (%d messages)\n\n", channel.DisplayName, len(posts)))

	for i, post := range posts {
		user, userErr := a.GetUser(post.UserId)
		username := "unknown"
		if userErr == nil {
			username = user.Username
		}
		participantSet[username] = true
		postIDs = append(postIDs, post.Id)

		t := time.Unix(post.CreateAt/1000, 0).UTC().Format("2006-01-02 15:04")
		sb.WriteString(fmt.Sprintf("[%d] [%s] @%s:\n%s\n\n", i+1, t, username, post.Message))
	}

	participants := make([]string, 0, len(participantSet))
	for p := range participantSet {
		participants = append(participants, p)
	}
	sort.Strings(participants)

	// 6. Check AI Bridge availability
	available, _ := a.GetAIPluginBridgeStatus(rctx)
	if !available {
		return nil, model.NewAppError("GetThreadSummary", "app.thread_summary.ai_unavailable", nil, "AI service is not available", http.StatusServiceUnavailable)
	}

	// 7. Build prompt and call LLM
	prompt := fmt.Sprintf(`Summarize this thread discussion. Provide:
1. A short summary (2-3 sentences) capturing the main topic and outcome.
2. Key points as bullet items, each mentioning the participant (@username) and their contribution. Reference the message number in brackets like [1], [2].

Respond ONLY with valid JSON, no markdown:
{"summary": "...", "key_points": [{"text": "@user did X [1]", "post_ids_indices": [0]}]}

Thread:
%s`, sb.String())

	llmResponse, llmErr := a.ch.agentsBridge.ServiceCompletion(userID, "", prompt)
	if llmErr != nil {
		return nil, model.NewAppError("GetThreadSummary", "app.thread_summary.llm_error", nil, llmErr.Error(), http.StatusInternalServerError)
	}

	// 8. Parse LLM response
	var parsed model.ThreadSummaryLLMResponse
	if jsonErr := json.Unmarshal([]byte(llmResponse), &parsed); jsonErr != nil {
		// Fallback: use raw text as summary
		return &model.ThreadSummaryResponse{
			Summary:         llmResponse,
			KeyPoints:       []model.ThreadKeyPoint{},
			Participants:    participants,
			ThreadPostCount: len(posts),
			Model:           "ai-bridge",
		}, nil
	}

	// 9. Map post indices to actual post IDs
	keyPoints := make([]model.ThreadKeyPoint, 0, len(parsed.KeyPoints))
	for _, kp := range parsed.KeyPoints {
		ids := make([]string, 0, len(kp.PostIdsIndices))
		for _, idx := range kp.PostIdsIndices {
			if idx >= 0 && idx < len(postIDs) {
				ids = append(ids, postIDs[idx])
			}
		}
		keyPoints = append(keyPoints, model.ThreadKeyPoint{
			Text:    kp.Text,
			PostIDs: ids,
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
Expected: No errors (or compile errors to fix — `agentsBridge` access may need `a.ch.agentsBridge` pattern check)

- [ ] **Step 3: Commit**

```bash
git add server/channels/app/thread_summary.go
git commit -m "feat(thread-summary): add app-layer business logic for thread summarization"
```

---

## Task 3: Backend — API Handler + Route

**Files:**
- Create: `server/channels/api4/thread_summary.go`
- Modify: `server/channels/api4/api.go` (add `api.InitThreadSummary()` call after `api.InitPost()` at ~line 343)

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

	// Check user has access to the post's channel
	post, appErr := c.App.GetSinglePost(c.AppContext, c.Params.PostId, false)
	if appErr != nil {
		c.Err = appErr
		return
	}

	channel, appErr := c.App.GetChannel(c.AppContext, post.ChannelId)
	if appErr != nil {
		c.Err = appErr
		return
	}

	hasPermission, _ := c.App.SessionHasPermissionToReadChannel(c.AppContext, *c.AppContext.Session(), channel)
	if !hasPermission {
		c.SetPermissionError()
		return
	}

	summary, appErr := c.App.GetThreadSummary(c.AppContext, c.Params.PostId, c.AppContext.Session().UserId)
	if appErr != nil {
		c.Err = appErr
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(summary); err != nil {
		c.Logger.Warn("Error writing thread summary response", err)
	}
}
```

- [ ] **Step 2: Register the route in api.go**

In `server/channels/api4/api.go`, after the line `api.InitPost()` (~line 343), add:
```go
api.InitThreadSummary()
```

- [ ] **Step 3: Verify it compiles**

Run: `cd /Users/dmitrybakhtin/WebstormProjects/mattermost/server && go build ./channels/api4/...`
Expected: No errors

- [ ] **Step 4: Commit**

```bash
git add server/channels/api4/thread_summary.go server/channels/api4/api.go
git commit -m "feat(thread-summary): add POST /api/v4/posts/{post_id}/summary endpoint"
```

---

## Task 4: Frontend — Constants + Redux State

**Files:**
- Modify: `webapp/channels/src/utils/constants.tsx` (~line 991, add to RHSStates)
- Create: `webapp/channels/src/reducers/views/thread_summary.ts`
- Create: `webapp/channels/src/selectors/views/thread_summary.ts`
- Create: `webapp/channels/src/actions/views/thread_summary.ts`
- Modify: `webapp/channels/src/reducers/views/index.ts` (register reducer)

- [ ] **Step 1: Add THREAD_SUMMARY to RHSStates**

In `webapp/channels/src/utils/constants.tsx`, add after `EDIT_HISTORY: 'edit-history'` (line 991):
```typescript
THREAD_SUMMARY: 'thread-summary',
```

- [ ] **Step 2: Add ActionTypes for thread summary**

In the same file `constants.tsx`, find the `ActionTypes` object and add inside it:
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

export interface ThreadSummaryState {
    loading: boolean;
    postId: string | null;
    data: ThreadSummaryData | null;
    error: string | null;
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

export default combineReducers({
    loading,
    postId,
    data,
    error,
});
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

In `webapp/channels/src/reducers/views/index.ts`, add import:
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
git commit -m "feat(thread-summary): add Redux state, reducer, selectors, and RHS constants"
```

---

## Task 5: Frontend — Client4 Method + Actions

**Files:**
- Modify: `webapp/platform/client/src/client4.ts`
- Create: `webapp/channels/src/actions/views/thread_summary.ts`

- [ ] **Step 1: Add Client4 method**

In `webapp/platform/client/src/client4.ts`, add near the other post methods (after `getPostThread`):

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

import type {DispatchFunc} from 'mattermost-redux/types/actions';

export function showThreadSummary(postId: string) {
    return (dispatch: DispatchFunc) => {
        dispatch({
            type: ActionTypes.UPDATE_RHS_STATE,
            state: RHSStates.THREAD_SUMMARY,
        });

        dispatch({
            type: ActionTypes.SELECT_POST,
            postId,
            channelId: '',
            timestamp: Date.now(),
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
    return {
        type: ActionTypes.THREAD_SUMMARY_CLEAR,
    };
}
```

- [ ] **Step 3: Commit**

```bash
git add webapp/platform/client/src/client4.ts \
        webapp/channels/src/actions/views/thread_summary.ts
git commit -m "feat(thread-summary): add Client4 API method and Redux actions"
```

---

## Task 6: Frontend — ThreadSummaryPanel Component

**Files:**
- Create: `webapp/channels/src/components/thread_summary_panel/thread_summary_panel.tsx`
- Create: `webapp/channels/src/components/thread_summary_panel/thread_summary_panel.scss`
- Create: `webapp/channels/src/components/thread_summary_panel/index.ts`

- [ ] **Step 1: Create styles**

```scss
// webapp/channels/src/components/thread_summary_panel/thread_summary_panel.scss
.ThreadSummaryPanel {
    display: flex;
    flex-direction: column;
    height: 100%;

    &__header {
        display: flex;
        align-items: center;
        padding: 12px 16px;
        border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
        font-weight: 600;
        font-size: 16px;
        gap: 8px;

        .back-button {
            cursor: pointer;
            display: flex;
            align-items: center;
            color: rgba(var(--center-channel-color-rgb), 0.56);

            &:hover {
                color: rgba(var(--center-channel-color-rgb), 0.72);
            }
        }

        .title {
            flex: 1;
        }

        .close-button {
            cursor: pointer;
            color: rgba(var(--center-channel-color-rgb), 0.56);

            &:hover {
                color: rgba(var(--center-channel-color-rgb), 0.72);
            }
        }
    }

    &__content {
        flex: 1;
        overflow-y: auto;
        padding: 16px;
    }

    &__channel-info {
        font-size: 13px;
        color: rgba(var(--center-channel-color-rgb), 0.56);
        margin-bottom: 16px;
    }

    &__summary {
        font-size: 14px;
        line-height: 1.6;
        margin-bottom: 16px;
        color: var(--center-channel-color);
    }

    &__details-toggle {
        display: flex;
        align-items: center;
        gap: 4px;
        cursor: pointer;
        font-size: 13px;
        font-weight: 600;
        color: var(--button-bg);
        margin-bottom: 12px;
        user-select: none;
    }

    &__key-points {
        list-style: none;
        padding: 0;
        margin: 0 0 16px;

        li {
            position: relative;
            padding: 6px 0 6px 16px;
            font-size: 14px;
            line-height: 1.5;

            &::before {
                content: '•';
                position: absolute;
                left: 0;
                color: var(--button-bg);
                font-weight: bold;
            }
        }
    }

    &__footer {
        padding: 12px 16px;
        border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
        font-size: 12px;
        color: rgba(var(--center-channel-color-rgb), 0.56);

        .feedback-buttons {
            display: flex;
            gap: 8px;
            margin-top: 8px;

            button {
                background: none;
                border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
                border-radius: 4px;
                padding: 4px 8px;
                cursor: pointer;
                font-size: 14px;

                &:hover {
                    background: rgba(var(--center-channel-color-rgb), 0.08);
                }
            }
        }
    }

    &__loading {
        display: flex;
        flex-direction: column;
        align-items: center;
        justify-content: center;
        padding: 48px 16px;
        gap: 12px;
        color: rgba(var(--center-channel-color-rgb), 0.56);
        font-size: 14px;
    }

    &__error {
        padding: 16px;
        text-align: center;
        color: var(--error-text);
        font-size: 14px;

        .retry-button {
            margin-top: 12px;
            color: var(--button-bg);
            cursor: pointer;
            font-weight: 600;

            &:hover {
                text-decoration: underline;
            }
        }
    }
}
```

- [ ] **Step 2: Create component**

```tsx
// webapp/channels/src/components/thread_summary_panel/thread_summary_panel.tsx
import React, {memo, useCallback, useState} from 'react';
import {useDispatch, useSelector} from 'react-redux';
import {FormattedMessage} from 'react-intl';

import {
    ArrowLeftIcon,
    CloseIcon,
    ChevronRightIcon,
    ChevronDownIcon,
    LoadingOutlineIcon,
} from '@mattermost/compass-icons/components';

import {closeRightHandSide, goBack} from 'actions/views/rhs';
import {fetchThreadSummary} from 'actions/views/thread_summary';
import {
    getThreadSummaryLoading,
    getThreadSummaryData,
    getThreadSummaryError,
    getThreadSummaryPostId,
} from 'selectors/views/thread_summary';

import './thread_summary_panel.scss';

const ThreadSummaryPanel: React.FC = () => {
    const dispatch = useDispatch();
    const loading = useSelector(getThreadSummaryLoading);
    const data = useSelector(getThreadSummaryData);
    const error = useSelector(getThreadSummaryError);
    const postId = useSelector(getThreadSummaryPostId);
    const [detailsExpanded, setDetailsExpanded] = useState(false);

    const handleBack = useCallback(() => {
        dispatch(goBack());
    }, [dispatch]);

    const handleClose = useCallback(() => {
        dispatch(closeRightHandSide());
    }, [dispatch]);

    const handleRetry = useCallback(() => {
        if (postId) {
            dispatch(fetchThreadSummary(postId));
        }
    }, [dispatch, postId]);

    const toggleDetails = useCallback(() => {
        setDetailsExpanded((prev) => !prev);
    }, []);

    return (
        <div className='ThreadSummaryPanel'>
            <div className='ThreadSummaryPanel__header'>
                <span
                    className='back-button'
                    onClick={handleBack}
                    role='button'
                    tabIndex={0}
                >
                    <ArrowLeftIcon size={20}/>
                </span>
                <span className='title'>
                    <FormattedMessage
                        id='thread_summary.title'
                        defaultMessage='AI Summary'
                    />
                </span>
                <span
                    className='close-button'
                    onClick={handleClose}
                    role='button'
                    tabIndex={0}
                >
                    <CloseIcon size={20}/>
                </span>
            </div>

            <div className='ThreadSummaryPanel__content'>
                {loading && (
                    <div className='ThreadSummaryPanel__loading'>
                        <LoadingOutlineIcon size={24}/>
                        <FormattedMessage
                            id='thread_summary.loading'
                            defaultMessage='Generating summary...'
                        />
                    </div>
                )}

                {error && !loading && (
                    <div className='ThreadSummaryPanel__error'>
                        <p>{error}</p>
                        <span
                            className='retry-button'
                            onClick={handleRetry}
                            role='button'
                            tabIndex={0}
                        >
                            <FormattedMessage
                                id='thread_summary.retry'
                                defaultMessage='Try again'
                            />
                        </span>
                    </div>
                )}

                {data && !loading && (
                    <>
                        <div className='ThreadSummaryPanel__channel-info'>
                            <FormattedMessage
                                id='thread_summary.post_count'
                                defaultMessage='{count} messages • {participants} participants'
                                values={{
                                    count: data.thread_post_count,
                                    participants: data.participants.length,
                                }}
                            />
                        </div>

                        <div className='ThreadSummaryPanel__summary'>
                            {data.summary}
                        </div>

                        {data.key_points.length > 0 && (
                            <>
                                <div
                                    className='ThreadSummaryPanel__details-toggle'
                                    onClick={toggleDetails}
                                    role='button'
                                    tabIndex={0}
                                >
                                    {detailsExpanded ? (
                                        <ChevronDownIcon size={16}/>
                                    ) : (
                                        <ChevronRightIcon size={16}/>
                                    )}
                                    <FormattedMessage
                                        id={detailsExpanded ? 'thread_summary.less_detail' : 'thread_summary.more_detail'}
                                        defaultMessage={detailsExpanded ? 'Less detail' : 'More detail'}
                                    />
                                </div>

                                {detailsExpanded && (
                                    <ul className='ThreadSummaryPanel__key-points'>
                                        {data.key_points.map((point, idx) => (
                                            <li key={idx}>{point.text}</li>
                                        ))}
                                    </ul>
                                )}
                            </>
                        )}
                    </>
                )}
            </div>

            <div className='ThreadSummaryPanel__footer'>
                <FormattedMessage
                    id='thread_summary.disclaimer'
                    defaultMessage='AI-generated summary. May be inaccurate.'
                />
                <div className='feedback-buttons'>
                    <button type='button'>{'👍'}</button>
                    <button type='button'>{'👎'}</button>
                </div>
            </div>
        </div>
    );
};

export default memo(ThreadSummaryPanel);
```

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

## Task 7: Frontend — Wire RHS to Show Summary Panel

**Files:**
- Modify: RHS controller to render ThreadSummaryPanel when `rhsState === THREAD_SUMMARY`

- [ ] **Step 1: Find the RHS controller**

The RHS content renderer needs to be located — it conditionally renders based on `rhsState`. Search for where `RHSStates.PIN`, `RHSStates.CHANNEL_INFO` etc. are checked to render different panels. This is likely in `webapp/channels/src/components/sidebar_right/sidebar_right.tsx` or similar. Find it and add:

```typescript
import ThreadSummaryPanel from 'components/thread_summary_panel';
```

And in the render logic, add a case:
```typescript
case RHSStates.THREAD_SUMMARY:
    content = <ThreadSummaryPanel/>;
    break;
```

- [ ] **Step 2: Verify no type errors**

Run: `cd /Users/dmitrybakhtin/WebstormProjects/mattermost/webapp && make check-types 2>&1 | head -20`
Expected: No new errors

- [ ] **Step 3: Commit**

```bash
git add webapp/channels/src/components/sidebar_right/
git commit -m "feat(thread-summary): wire ThreadSummaryPanel into RHS controller"
```

---

## Task 8: Frontend — DotMenu "Summarize Thread" Item

**Files:**
- Modify: `webapp/channels/src/components/dot_menu/dot_menu.tsx`

- [ ] **Step 1: Add handler and menu item**

Import at top of dot_menu.tsx:
```typescript
import {showThreadSummary} from 'actions/views/thread_summary';
```

Add handler method to the DotMenuClass:
```typescript
handleSummarizeThread = (): void => {
    this.props.actions.showThreadSummary(this.props.post.id);
};
```

Add the action to the `mapDispatchToProps` / `actions` object:
```typescript
showThreadSummary,
```

Add the menu item in the render method, after the "Follow Thread" item and before "Mark as Unread". Only show on root posts (not replies) that have replies:

```tsx
{this.props.post.root_id === '' && this.props.post.reply_count > 0 && (
    <Menu.Item
        id={`summarize_thread_${this.props.post.id}`}
        labels={
            <FormattedMessage
                id='post_info.summarize_thread'
                defaultMessage='Summarize Thread'
            />
        }
        leadingElement={<AutoAwesomeOutlineIcon size={18}/>}
        onClick={this.handleSummarizeThread}
    />
)}
```

Import the icon (or use an existing one from compass-icons):
```typescript
import {AutoAwesomeOutlineIcon} from '@mattermost/compass-icons/components';
```

If `AutoAwesomeOutlineIcon` doesn't exist, use `LightbulbOutlineIcon` or `SparklesIcon` instead.

- [ ] **Step 2: Commit**

```bash
git add webapp/channels/src/components/dot_menu/dot_menu.tsx
git commit -m "feat(thread-summary): add Summarize Thread to post dot menu"
```

---

## Task 9: Frontend — Thread Header ✨ Button

**Files:**
- Modify: `webapp/channels/src/components/rhs_thread/rhs_thread.tsx`

- [ ] **Step 1: Add summarize button to thread RHS header**

In `rhs_thread.tsx`, the component renders `RhsHeaderPost`. We need to either:
a) Add a button inside this component, or
b) Modify `RhsHeaderPost` to include the button.

Find `RhsHeaderPost` component and add a summarize button. Add imports:

```typescript
import {useDispatch} from 'react-redux';
import {showThreadSummary} from 'actions/views/thread_summary';
```

Add a button in the header area (near the close button) that calls `dispatch(showThreadSummary(selected.id))` when clicked. Use `LightbulbOutlineIcon` or similar compass icon with a tooltip "Summarize Thread".

- [ ] **Step 2: Commit**

```bash
git add webapp/channels/src/components/rhs_thread/ webapp/channels/src/components/rhs_header_post/
git commit -m "feat(thread-summary): add summarize button to thread RHS header"
```

---

## Task 10: Integration Test — Full Flow

- [ ] **Step 1: Start the dev server and verify**

Run: `cd /Users/dmitrybakhtin/WebstormProjects/mattermost/server && make run-server` (if not running)
Run: `cd /Users/dmitrybakhtin/WebstormProjects/mattermost/webapp && make dev` (if not running)

- [ ] **Step 2: Manual test via Playwright or browser**

1. Open http://localhost:9005, login as sysadmin
2. Navigate to a channel with threads
3. Click "..." on a root post with replies
4. Verify "Summarize Thread" appears in the menu
5. Click it — RHS should show the ThreadSummaryPanel with loading state
6. If AI Bridge is configured: summary appears; if not: error with "AI service unavailable" and retry button
7. Click "More detail" — key points expand
8. Click "Less detail" — they collapse
9. Click Back arrow — returns to thread view

- [ ] **Step 3: Final commit**

```bash
git add -A
git commit -m "feat(thread-summary): complete thread summarization feature"
```
