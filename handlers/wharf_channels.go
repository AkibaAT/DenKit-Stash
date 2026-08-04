package handlers

import (
	"denkit-stash/auth"
	"denkit-stash/models"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

func (h *WharfHandlers) resolveTargetGame(w http.ResponseWriter, r *http.Request, target string) (*models.Game, bool) {
	if target == "" {
		writeError(w, http.StatusBadRequest, "missing build target")
		return nil, false
	}
	parts := strings.Split(target, "/")
	if len(parts) != 2 {
		writeError(w, http.StatusBadRequest, "invalid target format, expected username/gamename")
		return nil, false
	}

	username, gameName := parts[0], parts[1]
	user := auth.MustGetUser(r.Context())
	if err := h.validateNamespaceAccess(user, username); err != nil {
		writeError(w, http.StatusForbidden, "access denied")
		return nil, false
	}

	targetUserID := user.ID
	if user.Username != username {
		targetUser, err := h.db.GetUserByUsername(username)
		if err != nil {
			writeError(w, http.StatusNotFound, "target user not found")
			return nil, false
		}
		targetUserID = targetUser.ID
	}

	game, err := h.db.GetGameByUserAndTitle(targetUserID, gameName)
	if err != nil {
		writeError(w, http.StatusNotFound, "game not found")
		return nil, false
	}
	return game, true
}

func (h *WharfHandlers) ListChannels(w http.ResponseWriter, r *http.Request) {
	game, ok := h.resolveTargetGame(w, r, r.URL.Query().Get("target"))
	if !ok {
		return
	}
	uploads, err := h.db.GetUploadsByGameID(game.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get uploads")
		return
	}

	channels := make(map[string]WharfChannelResponse)
	for _, upload := range uploads {
		uploadChannels, err := h.db.GetChannelsByUploadID(upload.ID)
		if err != nil {
			continue
		}
		for _, channel := range uploadChannels {
			var currentBuild *models.Build
			var files []*models.BuildFile
			if channel.CurrentBuildID != nil {
				currentBuild, err = h.db.GetBuildByID(*channel.CurrentBuildID)
				if err != nil {
					continue
				}
				files, _ = h.db.GetBuildFilesByBuildID(currentBuild.ID)
			}
			channels[channel.Name] = newWharfChannelResponse(channel, upload, currentBuild, files)
		}
	}
	writeJSON(w, http.StatusOK, ChannelMapResponse{Channels: channels})
}

func (h *WharfHandlers) GetChannel(w http.ResponseWriter, r *http.Request) {
	game, ok := h.resolveTargetGame(w, r, r.URL.Query().Get("target"))
	if !ok {
		return
	}
	channelName := mux.Vars(r)["channel"]
	uploads, err := h.db.GetUploadsByGameID(game.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get uploads")
		return
	}

	for _, upload := range uploads {
		channel, err := h.db.GetChannelByName(channelName, upload.ID)
		if err != nil {
			continue
		}
		var currentBuild *models.Build
		var files []*models.BuildFile
		if channel.CurrentBuildID != nil {
			currentBuild, err = h.db.GetBuildByID(*channel.CurrentBuildID)
			if err == nil {
				files, _ = h.db.GetBuildFilesByBuildID(currentBuild.ID)
			}
		}
		writeJSON(w, http.StatusOK, ChannelEnvelopeResponse{
			Channel: newWharfChannelResponse(channel, upload, currentBuild, files),
		})
		return
	}
	writeError(w, http.StatusNotFound, "channel not found")
}

func (h *WharfHandlers) ListBuilds(w http.ResponseWriter, r *http.Request) {
	channelName := r.URL.Query().Get("channel")
	if channelName == "" {
		writeError(w, http.StatusBadRequest, "missing channel")
		return
	}
	game, ok := h.resolveTargetGame(w, r, r.URL.Query().Get("target"))
	if !ok {
		return
	}
	builds, err := h.db.GetBuildsByGameAndChannel(game.ID, channelName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get builds")
		return
	}
	responseBuilds := make([]CoreBuildResponse, 0, len(builds))
	for _, build := range builds {
		responseBuilds = append(responseBuilds, newCoreBuildResponse(build, true))
	}
	writeJSON(w, http.StatusOK, BuildListResponse{Builds: responseBuilds})
}

func (h *WharfHandlers) GetLatestCompletedBuild(w http.ResponseWriter, r *http.Request) {
	channelName := r.URL.Query().Get("channel")
	userVersion := r.URL.Query().Get("user_version")
	if channelName == "" {
		writeError(w, http.StatusBadRequest, "missing channel")
		return
	}
	if userVersion == "" {
		writeError(w, http.StatusBadRequest, "missing user_version")
		return
	}
	game, ok := h.resolveTargetGame(w, r, r.URL.Query().Get("target"))
	if !ok {
		return
	}
	build, err := h.db.GetLatestCompletedBuildByGameChannelVersion(game.ID, channelName, userVersion)
	if err != nil {
		writeError(w, http.StatusNotFound, "build not found")
		return
	}
	writeJSON(w, http.StatusOK, BuildEnvelopeResponse{Build: newCoreBuildResponse(build, true)})
}
