// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
	"github.com/mattermost/mattermost/server/public/shared/request"
)

// summarizePostsJSONSchema is the structured output schema for the LLM summarization response.
// Defined at package level to avoid re-allocating on every call.
var summarizePostsJSONSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"highlights": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Key discussion points, decisions, or important information",
		},
		"action_items": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Tasks, todos, or action items mentioned",
		},
	},
	"required":             []any{"highlights", "action_items"},
	"additionalProperties": false,
}

// SummarizePosts generates an AI summary of posts with highlights and action items
func (a *App) SummarizePosts(rctx request.CTX, userID string, posts []*model.Post, channelName, teamName string, agentID string) (*model.AIRecapSummaryResponse, *model.AppError) {
	if len(posts) == 0 {
		return &model.AIRecapSummaryResponse{Highlights: []string{}, ActionItems: []string{}}, nil
	}

	// Get site URL for permalink generation
	siteURL := a.GetSiteURL()

	// Build conversation context from posts and collect post IDs
	conversationText, postIDs := buildConversationTextWithIDs(posts)

	systemPrompt := "Ты — эксперт по анализу командных переписок и извлечению ключевой информации. Твоя задача — кратко изложить переписку из канала Mattermost, выделив самое важное и задачи к выполнению. Верни ТОЛЬКО валидный JSON с ключами 'highlights' и 'action_items', каждый — массив строк. Если ничего из этого нет — верни пустые массивы. Не придумывай информацию: включай только то, что явно есть в переписке. Весь текст в highlights и action_items — на РУССКОМ ЯЗЫКЕ. Не переводи и не изменяй @username — оставляй как есть."

	userPrompt := fmt.Sprintf(`Проанализируй переписку из канала «%s» и сформируй саммари на русском языке.

Site URL: %s
Team Name: %s

Переписка:
%s

Доступные Post IDs: %s

Верни JSON-объект с:
- "highlights": массив ключевых тем, решений или важной информации из обсуждения
- "action_items": массив задач, todo и действий, упомянутых в обсуждении

Все строки — на русском языке.

ВАЖНЫЕ ПРАВИЛА:
1. Если в твоём пункте упоминается пользователь, ставь перед username символ @ (оставляй username как есть, без перевода). Пример: если в чате '<username>' = 'john.smith' и он что-то сообщил — пиши «@john.smith отправил апдейт по проекту xyz».

2. К КАЖДОМУ highlight и action item добавляй permalink на исходный пост. Ссылка — в конец строки, в формате: [PERMALINK:%s/%s/pl/<POST_ID>], где <POST_ID> — один из доступных Post IDs выше. Выбирай тот post, который максимально релевантен этому пункту.

Пример: «Команда решила перейти на микросервисную архитектуру [PERMALINK:%s/%s/pl/abc123xyz]»

Ответ — строго компактный валидный JSON, без дополнительного текста, без форматирования и без блоков кода.`, channelName, siteURL, teamName, conversationText, strings.Join(postIDs, ", "), siteURL, teamName, siteURL, teamName)

	// Create bridge client
	sessionUserID := ""
	if session := rctx.Session(); session != nil {
		sessionUserID = session.UserId
	}
	requestUserID := userID
	if sessionUserID != "" {
		requestUserID = sessionUserID
	}
	completionRequest := BridgeCompletionRequest{
		Operation:       BridgeOperationRecapSummary,
		ClientOperation: "recaps",
		Messages: []BridgeMessage{
			{Role: "system", Message: systemPrompt},
			{Role: "user", Message: userPrompt},
		},
		JSONOutputFormat: summarizePostsJSONSchema,
		OperationSubType: "summarize_channel",
		UserID:           requestUserID,
		ChannelID:        posts[0].ChannelId,
	}

	rctx.Logger().Debug("Calling AI agent for post summarization",
		mlog.String("channel_name", channelName),
		mlog.String("user_id", userID),
		mlog.String("agent_id", agentID),
		mlog.Int("post_count", len(posts)),
	)

	completion, err := a.ch.agentsBridge.AgentCompletion(sessionUserID, agentID, completionRequest)
	if err != nil {
		return nil, model.NewAppError("SummarizePosts", "app.ai.summarize.agent_call_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	var summary model.AIRecapSummaryResponse
	if err := json.Unmarshal([]byte(completion), &summary); err != nil {
		return nil, model.NewAppError("SummarizePosts", "app.ai.summarize.parse_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	// Ensure arrays are never nil
	if summary.Highlights == nil {
		summary.Highlights = []string{}
	}
	if summary.ActionItems == nil {
		summary.ActionItems = []string{}
	}

	rctx.Logger().Debug("AI summarization successful",
		mlog.String("channel_name", channelName),
		mlog.Int("highlights_count", len(summary.Highlights)),
		mlog.Int("action_items_count", len(summary.ActionItems)),
	)

	return &summary, nil
}

func buildConversationTextWithIDs(posts []*model.Post) (string, []string) {
	var sb strings.Builder
	postIDs := make([]string, 0, len(posts))

	for _, post := range posts {
		// Collect post ID
		postIDs = append(postIDs, post.Id)

		// Posts should have Username populated by the caller
		// For posts without username, use UserId as fallback
		username := ""
		if usernameProp := post.GetProp("username"); usernameProp != nil {
			if usernameStr, ok := usernameProp.(string); ok {
				username = usernameStr
			}
		}
		if username == "" {
			username = post.UserId
		}
		sb.WriteString(fmt.Sprintf("[%s] %s (Post ID: %s): %s\n",
			time.UnixMilli(post.CreateAt).Format("15:04"),
			username,
			post.Id,
			post.Message))
	}
	return sb.String(), postIDs
}
