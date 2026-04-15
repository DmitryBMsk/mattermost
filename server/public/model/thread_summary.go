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
