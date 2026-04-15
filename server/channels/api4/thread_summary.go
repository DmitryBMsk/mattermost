// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api4

import (
	"encoding/json"
	"net/http"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
)

func (api *API) InitThreadSummary() {
	// Note: relies on the global rate limiter for per-user throttling.
	// Consider adding per-endpoint rate limiting if AI cost becomes a concern.
	api.BaseRoutes.Post.Handle("/summary", api.APISessionRequired(postThreadSummary)).Methods(http.MethodPost)
}

func postThreadSummary(c *Context, w http.ResponseWriter, r *http.Request) {
	c.RequirePostId()
	if c.Err != nil {
		return
	}

	// GetPostIfAuthorized checks channel read permission internally
	post, appErr, _ := c.App.GetPostIfAuthorized(c.AppContext, c.Params.PostId, c.AppContext.Session(), false)
	if appErr != nil {
		c.Err = appErr
		return
	}

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
		c.Logger.Warn("Error writing thread summary response", mlog.Err(err))
	}
}
