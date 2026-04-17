// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/v8/channels/store"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
	"github.com/mattermost/mattermost/server/public/shared/request"
)

const maxThreadPostsForSummary = 200

var threadSummaryJSONSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"summary": map[string]any{"type": "string"},
		"key_points": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text":     map[string]any{"type": "string"},
					"post_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required": []any{"text", "post_ids"},
			},
		},
	},
	"required": []any{"summary", "key_points"},
}

// GetThreadSummary generates an AI-powered summary for a thread.
// The caller must ensure the user has read access to the channel.
func (a *App) GetThreadSummary(rctx request.CTX, rootPostID string, userID string, channel *model.Channel) (*model.ThreadSummaryResponse, *model.AppError) {
	// Check AI Bridge availability before doing any DB work
	available, _ := a.GetAIPluginBridgeStatus(rctx)
	if !available {
		return nil, model.NewAppError("GetThreadSummary", "app.thread_summary.ai_unavailable", nil, "AI service is not available", http.StatusServiceUnavailable)
	}

	// Fetch all thread posts
	opts := model.GetPostsOptions{}
	postList, appErr := a.GetPostThread(rctx, rootPostID, opts, userID)
	if appErr != nil {
		return nil, appErr
	}

	if len(postList.Posts) <= 1 {
		return nil, model.NewAppError("GetThreadSummary", "app.thread_summary.no_replies", nil, "thread has no replies", http.StatusBadRequest)
	}

	// Sort posts chronologically and bound to maxThreadPostsForSummary
	posts := make([]*model.Post, 0, len(postList.Posts))
	for _, post := range postList.Posts {
		posts = append(posts, post)
	}
	sort.Slice(posts, func(i, j int) bool {
		return posts[i].CreateAt < posts[j].CreateAt
	})
	if len(posts) > maxThreadPostsForSummary {
		posts = posts[len(posts)-maxThreadPostsForSummary:]
	}

	// Batch-fetch unique users to avoid N+1 queries
	userIDSet := make(map[string]bool, len(posts))
	for _, post := range posts {
		userIDSet[post.UserId] = true
	}
	userIDs := make([]string, 0, len(userIDSet))
	for uid := range userIDSet {
		userIDs = append(userIDs, uid)
	}
	users, usersErr := a.GetUsersByIds(rctx, userIDs, &store.UserGetByIdsOpts{})
	usernameMap := make(map[string]string, len(userIDs))
	if usersErr == nil {
		for _, u := range users {
			usernameMap[u.Id] = u.Username
		}
	}

	// Single pass: format for LLM, collect participants and valid post IDs
	participantSet := make(map[string]bool)
	validIDs := make(map[string]bool, len(posts))
	var sb strings.Builder
	sb.Grow(len(posts) * 150)
	sb.WriteString(fmt.Sprintf("Thread in #%s (%d messages)\n\n", channel.DisplayName, len(posts)))

	for _, post := range posts {
		username := usernameMap[post.UserId]
		if username == "" {
			username = "unknown"
		}
		participantSet[username] = true
		validIDs[post.Id] = true

		t := time.Unix(post.CreateAt/1000, 0).UTC().Format("2006-01-02 15:04")
		sb.WriteString(fmt.Sprintf("[post_id:%s] [%s] @%s:\n%s\n\n", post.Id, t, username, post.Message))
	}

	participants := make([]string, 0, len(participantSet))
	for p := range participantSet {
		participants = append(participants, p)
	}
	sort.Strings(participants)

	// Build BridgeCompletionRequest
	systemPrompt := "Ты — суммаризатор тредов сообщений. На основе переданного треда сформируй структурированный JSON-саммари строго на РУССКОМ ЯЗЫКЕ (summary и key_points — только по-русски). Каждое сообщение начинается с [post_id:XXX]. В key_points добавляй значения post_id упомянутых сообщений в массив post_ids. Не переводи имена участников (@username) — оставляй как есть."
	userPrompt := fmt.Sprintf("Сделай саммари этого треда на русском языке. Укажи:\n1. Короткое саммари (2-3 предложения) с главной темой и итогом обсуждения.\n2. Ключевые моменты пунктами; в каждом упоминай участника (@username) и его вклад в обсуждение.\n\nДля каждого ключевого момента включай post_ids упомянутых сообщений.\n\nВесь текст — на русском.\n\nТред:\n%s", sb.String())

	req := BridgeCompletionRequest{
		Operation:        BridgeOperationRecapSummary,
		ClientOperation:  "recaps",
		OperationSubType: "summarize_thread",
		Messages: []BridgeMessage{
			{Role: "system", Message: systemPrompt},
			{Role: "user", Message: userPrompt},
		},
		JSONOutputFormat: threadSummaryJSONSchema,
		UserID:           userID,
		ChannelID:        channel.Id,
	}

	// Get the default agent (or first available)
	agents, agentsErr := a.ch.agentsBridge.GetAgents(userID, userID)
	if agentsErr != nil || len(agents) == 0 {
		return nil, model.NewAppError("GetThreadSummary", "app.thread_summary.no_agents", nil, "no AI agents available", http.StatusServiceUnavailable)
	}
	agentID := agents[0].ID
	for _, agent := range agents {
		if agent.IsDefault {
			agentID = agent.ID
			break
		}
	}

	llmResponse, llmErr := a.ch.agentsBridge.AgentCompletion(userID, agentID, req)
	if llmErr != nil {
		rctx.Logger().Error("LLM completion failed for thread summary", mlog.String("post_id", rootPostID), mlog.Err(llmErr))
		return nil, model.NewAppError("GetThreadSummary", "app.thread_summary.llm_error", nil, "AI completion request failed", http.StatusInternalServerError)
	}

	// Parse LLM response
	var parsed model.ThreadSummaryLLMResponse
	if jsonErr := json.Unmarshal([]byte(llmResponse), &parsed); jsonErr != nil {
		rctx.Logger().Warn("Failed to parse LLM summary JSON, returning raw text", mlog.String("post_id", rootPostID), mlog.Err(jsonErr))
		return &model.ThreadSummaryResponse{
			Summary:         llmResponse,
			KeyPoints:       []model.ThreadKeyPoint{},
			Participants:    participants,
			ThreadPostCount: len(posts),
			Model:           "ai-bridge",
		}, nil
	}

	// Validate post IDs — only keep IDs that exist in this thread
	keyPoints := make([]model.ThreadKeyPoint, 0, len(parsed.KeyPoints))
	for _, kp := range parsed.KeyPoints {
		filteredIDs := make([]string, 0, len(kp.PostIDs))
		for _, id := range kp.PostIDs {
			if validIDs[id] {
				filteredIDs = append(filteredIDs, id)
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
