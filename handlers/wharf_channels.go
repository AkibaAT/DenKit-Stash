package handlers

import (
	"denkit-stash/auth"
	"denkit-stash/models"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// GET /wharf/channels - List all channels for a target
func (h *WharfHandlers) ListChannels(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("target")

	if target == "" {
		http.Error(w, `{"errors":["missing build target (need game_id or target)"]}`, http.StatusBadRequest)
		return
	}

	// Parse target format: "username/gamename"
	parts := strings.Split(target, "/")
	if len(parts) != 2 {
		http.Error(w, `{"errors":["invalid target format, expected username/gamename"]}`, http.StatusBadRequest)
		return
	}

	username := parts[0]
	gamename := parts[1]

	// Get user from context (set by auth middleware)
	user := auth.MustGetUser(r.Context())

	// Validate namespace access
	err := h.validateNamespaceAccess(user, username)
	if err != nil {
		fmt.Printf("Namespace access denied: %v\n", err)
		http.Error(w, `{"errors":["access denied"]}`, http.StatusForbidden)
		return
	}

	// Note: User and namespace validation already done above

	// Find the game
	game, err := h.db.GetGameByUserAndTitle(user.ID, gamename)
	if err != nil {
		http.Error(w, `{"errors":["game not found"]}`, http.StatusNotFound)
		return
	}

	// Get all uploads for this game
	uploads, err := h.db.GetUploadsByGameID(game.ID)
	if err != nil {
		http.Error(w, `{"errors":["failed to get uploads"]}`, http.StatusInternalServerError)
		return
	}

	// Build channels response
	channels := make(map[string]interface{})

	for _, upload := range uploads {
		// Get actual channels for this upload from the channels table
		uploadChannels, err := h.db.GetChannelsByUploadID(upload.ID)
		if err != nil {
			continue // Skip this upload if we can't get channels
		}

		for _, channel := range uploadChannels {
			var currentBuild *models.Build
			if channel.CurrentBuildID != nil {
				// Get the current build from the channel
				currentBuild, err = h.db.GetBuildByID(*channel.CurrentBuildID)
				if err != nil {
					continue // Skip this channel if we can't get the build
				}
			}

			channelData := map[string]interface{}{
				"name": channel.Name,
				"upload": map[string]interface{}{
					"id": upload.ID,
				},
			}

			if currentBuild != nil {
				files, err := h.db.GetBuildFilesByBuildID(currentBuild.ID)
				if err != nil {
					files = nil
				}
				channelData["head"] = serializeBuild(currentBuild, files)
			}

			channels[channel.Name] = channelData
		}
	}

	response := map[string]interface{}{
		"channels": channels,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GET /wharf/channels/{channel} - Get channel information
func (h *WharfHandlers) GetChannel(w http.ResponseWriter, r *http.Request) {
	channelName := mux.Vars(r)["channel"]
	target := r.URL.Query().Get("target")

	if target == "" {
		http.Error(w, `{"errors":["missing build target (need game_id or target)"]}`, http.StatusBadRequest)
		return
	}

	// Parse target format: "username/gamename"
	parts := strings.Split(target, "/")
	if len(parts) != 2 {
		http.Error(w, `{"errors":["invalid target format, expected username/gamename"]}`, http.StatusBadRequest)
		return
	}

	username := parts[0]
	gamename := parts[1]

	// Get user from context (set by auth middleware)
	user, ok := auth.GetUser(r.Context())
	if !ok || user == nil {
		http.Error(w, `{"errors":["user not found in context"]}`, http.StatusInternalServerError)
		return
	}

	// Validate namespace access
	err := h.validateNamespaceAccess(user, username)
	if err != nil {
		fmt.Printf("Namespace access denied: %v\n", err)
		http.Error(w, `{"errors":["access denied"]}`, http.StatusForbidden)
		return
	}

	// Find the target user (for namespace access)
	var targetUserID int64
	if user.Username == username {
		// User accessing their own namespace
		targetUserID = user.ID
	} else {
		// Admin user accessing another user's namespace - look up the target user
		targetUser, err := h.db.GetUserByUsername(username)
		if err != nil {
			http.Error(w, `{"errors":["target user not found"]}`, http.StatusNotFound)
			return
		}
		targetUserID = targetUser.ID
	}

	// Find the game owned by the target user
	game, err := h.db.GetGameByUserAndTitle(targetUserID, gamename)
	if err != nil {
		http.Error(w, `{"errors":["game not found"]}`, http.StatusNotFound)
		return
	}

	// Get all uploads for this game
	uploads, err := h.db.GetUploadsByGameID(game.ID)
	if err != nil {
		http.Error(w, `{"errors":["failed to get uploads"]}`, http.StatusInternalServerError)
		return
	}

	// Find the channel across all uploads
	var foundChannel *models.Channel
	var foundUpload *models.Upload

	for _, upload := range uploads {
		channels, err := h.db.GetChannelsByUploadID(upload.ID)
		if err != nil {
			continue
		}

		for _, channel := range channels {
			if channel.Name == channelName {
				foundChannel = channel
				foundUpload = upload
				break
			}
		}

		if foundChannel != nil {
			break
		}
	}

	if foundChannel == nil {
		http.Error(w, `{"errors":["channel not found"]}`, http.StatusNotFound)
		return
	}

	channelData := map[string]interface{}{
		"name": foundChannel.Name,
		"upload": map[string]interface{}{
			"id": foundUpload.ID,
		},
	}

	// Get the current build if it exists
	if foundChannel.CurrentBuildID != nil {
		currentBuild, err := h.db.GetBuildByID(*foundChannel.CurrentBuildID)
		if err == nil {
			files, err := h.db.GetBuildFilesByBuildID(currentBuild.ID)
			if err != nil {
				files = nil
			}
			channelData["head"] = serializeBuild(currentBuild, files)
		}
	}

	response := map[string]interface{}{
		"channel": channelData,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
