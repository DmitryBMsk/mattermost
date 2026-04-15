package api4

import (
	"encoding/json"
	"net/http"

	"github.com/mattermost/mattermost/server/public/model"
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
	// Signature: (post *model.Post, appErr *model.AppError, isMember bool)
	post, appErr, _ := c.App.GetPostIfAuthorized(c.AppContext, c.Params.PostId, c.AppContext.Session(), false)
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
