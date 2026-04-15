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
	userPrompt := fmt.Sprintf("Summarize this thread. Provide:\n1. A short summary (2-3 sentences) capturing the main topic and outcome.\n2. Key points as bullet items, each mentioning the participant (@username) and their contribution.\n\nFor each key point, include the post_ids of the messages you reference.\n\nThread:\n%s", sb.String())

	jsonSchema := map[string]any{
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

	req := BridgeCompletionRequest{
		Operation:        BridgeOperationRecapSummary,
		ClientOperation:  "recaps",
		OperationSubType: "summarize_thread",
		Messages: []BridgeMessage{
			{Role: "system", Message: systemPrompt},
			{Role: "user", Message: userPrompt},
		},
		JSONOutputFormat: jsonSchema,
		UserID:           userID,
		ChannelID:        channel.Id,
	}

	llmResponse, llmErr := a.ch.agentsBridge.ServiceCompletion(userID, "", req)
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
