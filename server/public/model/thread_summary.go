// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model

// ThreadKeyPoint represents a single key point extracted from a thread summary.
type ThreadKeyPoint struct {
	Text    string   `json:"text"`
	PostIDs []string `json:"post_ids"`
}

// ThreadSummaryResponse is the API response for a thread summary request.
type ThreadSummaryResponse struct {
	Summary         string           `json:"summary"`
	KeyPoints       []ThreadKeyPoint `json:"key_points"`
	Participants    []string         `json:"participants"`
	ThreadPostCount int              `json:"thread_post_count"`
	Model           string           `json:"model"`
}

// ThreadSummaryLLMResponse is the expected JSON structure from the LLM.
type ThreadSummaryLLMResponse struct {
	Summary   string           `json:"summary"`
	KeyPoints []ThreadKeyPoint `json:"key_points"`
}
