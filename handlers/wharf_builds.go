package handlers

import (
	"denkit-stash/auth"
	"denkit-stash/models"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

func (h *WharfHandlers) CreateBuild(w http.ResponseWriter, r *http.Request) {
	user := auth.MustGetUser(r.Context())

	var req struct {
		Target      string `json:"target"`
		Channel     string `json:"channel"`
		UserVersion string `json:"user_version"`
	}

	if ok := decodeJSONOrFormRequest(w, r, &req, func() error {
		req.Target = r.Form.Get("target")
		req.Channel = r.Form.Get("channel")
		req.UserVersion = r.Form.Get("user_version")
		return nil
	}); !ok {
		return
	}

	if req.Target == "" {
		http.Error(w, `{"errors":["missing target"]}`, http.StatusBadRequest)
		return
	}

	parts := strings.Split(req.Target, "/")
	if len(parts) != 2 {
		http.Error(w, `{"errors":["invalid target format"]}`, http.StatusBadRequest)
		return
	}

	username, gameName := parts[0], parts[1]

	err := h.validateNamespaceAccess(user, username)
	if err != nil {
		fmt.Printf("Namespace access denied: %v\n", err)
		http.Error(w, `{"errors":["access denied"]}`, http.StatusForbidden)
		return
	}

	namespaceOwner, err := h.db.GetUserByUsername(username)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"errors":["namespace owner not found: %s"]}`, username), http.StatusNotFound)
		return
	}

	var games []*models.Game
	games, err = h.db.GetGamesByUserID(namespaceOwner.ID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"errors":["%s"]}`, err.Error()), http.StatusInternalServerError)
		return
	}

	var game *models.Game
	for _, g := range games {
		if g.Title == gameName {
			game = g
			break
		}
	}

	if game == nil {
		game = &models.Game{
			UserID:         namespaceOwner.ID,
			Title:          gameName,
			Type:           "default",
			Classification: "game",
		}

		err = h.db.CreateGame(game)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"errors":["%s"]}`, err.Error()), http.StatusInternalServerError)
			return
		}
	}

	var uploads []*models.Upload
	uploads, err = h.db.GetUploadsByGameID(game.ID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"errors":["%s"]}`, err.Error()), http.StatusInternalServerError)
		return
	}

	var upload *models.Upload

	for _, existingUpload := range uploads {
		_, channelErr := h.db.GetChannelByName(req.Channel, existingUpload.ID)
		if channelErr == nil {
			upload = existingUpload
			channelPlatforms := platformsForChannelName(req.Channel)
			if upload.Platforms != channelPlatforms {
				upload.Platforms = channelPlatforms
				if err = h.db.UpdateUpload(upload); err != nil {
					http.Error(w, fmt.Sprintf(`{"errors":["%s"]}`, err.Error()), http.StatusInternalServerError)
					return
				}
			}
			break
		}
	}

	if upload == nil {
		upload = &models.Upload{
			GameID:      game.ID,
			Filename:    fmt.Sprintf("%s.zip", gameName),
			DisplayName: gameName,
			Storage:     "hosted",
			Type:        "default",
			Platforms:   platformsForChannelName(req.Channel),
		}

		err = h.db.CreateUpload(upload)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"errors":["%s"]}`, err.Error()), http.StatusInternalServerError)
			return
		}
	}

	var parentBuildID *int64
	var existingChannel *models.Channel

	existingChannel, err = h.db.GetChannelByName(req.Channel, upload.ID)
	if err == nil {
		if existingChannel.CurrentBuildID != nil {
			parentBuildID = existingChannel.CurrentBuildID
		}
	}

	build := &models.Build{
		UploadID:      upload.ID,
		UserVersion:   req.UserVersion,
		ChannelName:   req.Channel,
		ParentBuildID: parentBuildID,
		State:         "started",
	}

	err = h.db.CreateBuild(build)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"errors":["%s"]}`, err.Error()), http.StatusInternalServerError)
		return
	}

	// Create the channel row if needed, but do not advance the channel head
	// until the build has completed all required artifacts.
	if existingChannel == nil {
		channel := &models.Channel{
			Name:     req.Channel,
			UploadID: upload.ID,
		}
		err = h.db.CreateChannel(channel)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"errors":["%s"]}`, err.Error()), http.StatusInternalServerError)
			return
		}
	}

	response := map[string]interface{}{
		"build": serializeBuild(build, nil),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GET /wharf/builds/{id}/files - List files for a build
func (h *WharfHandlers) GetBuildFiles(w http.ResponseWriter, r *http.Request) {
	buildIDStr := mux.Vars(r)["id"]

	buildID, err := strconv.ParseInt(buildIDStr, 10, 64)
	if err != nil {
		http.Error(w, `{"errors":["invalid build id"]}`, http.StatusBadRequest)
		return
	}

	build, err := h.db.GetBuildByID(buildID)
	if err != nil {
		http.Error(w, `{"errors":["build not found"]}`, http.StatusNotFound)
		return
	}
	if err = h.authorizeLoadedBuildAccess(r, build, nil); err != nil {
		writeBuildAccessError(w, r)
		return
	}

	var buildFiles []*models.BuildFile
	buildFiles, err = h.db.GetBuildFilesByBuildID(buildID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"errors":["%s"]}`, err.Error()), http.StatusInternalServerError)
		return
	}

	var filesResponse []map[string]interface{}
	for _, file := range buildFiles {
		fileResponse := serializeBuildFile(file)
		filesResponse = append(filesResponse, fileResponse)
	}

	response := map[string]interface{}{
		"files": filesResponse,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *WharfHandlers) CreateBuildFile(w http.ResponseWriter, r *http.Request) {
	buildIDStr := mux.Vars(r)["id"]
	buildID, err := strconv.ParseInt(buildIDStr, 10, 64)
	if err != nil {
		http.Error(w, `{"errors":["invalid build id"]}`, http.StatusBadRequest)
		return
	}

	build, err := h.db.GetBuildByID(buildID)
	if err != nil {
		http.Error(w, `{"errors":["build not found"]}`, http.StatusNotFound)
		return
	}
	if err = h.authorizeLoadedBuildAccess(r, build, nil); err != nil {
		writeBuildAccessError(w, r)
		return
	}

	var req struct {
		Type       string `json:"type"`
		SubType    string `json:"sub_type"`
		UploadType string `json:"upload_type"`
	}

	if ok := decodeJSONOrFormRequest(w, r, &req, func() error {
		req.Type = r.Form.Get("type")
		req.SubType = r.Form.Get("sub_type")
		req.UploadType = r.Form.Get("upload_type")
		return nil
	}); !ok {
		return
	}

	if req.Type == "" {
		http.Error(w, `{"errors":["missing type"]}`, http.StatusBadRequest)
		return
	}

	if req.SubType == "" {
		req.SubType = "default"
	}

	storagePath := fmt.Sprintf("builds/%d/%s_%s_%s", buildID, req.Type, req.SubType, uuid.New().String())

	buildFile := &models.BuildFile{
		BuildID:     buildID,
		Type:        req.Type,
		SubType:     req.SubType,
		State:       "uploading",
		StoragePath: storagePath,
	}

	err = h.db.CreateBuildFile(buildFile)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"errors":["%s"]}`, err.Error()), http.StatusInternalServerError)
		return
	}

	uploadHeaders := map[string]interface{}{}
	if req.UploadType == "deferred_resumable" || req.UploadType == "deferred-resumable" {
		session := &models.UploadSession{
			ID:          uuid.New().String(),
			BuildFileID: buildFile.ID,
			StoragePath: buildFile.StoragePath,
			State:       "active",
		}
		if err = h.db.CreateUploadSession(session); err != nil {
			http.Error(w, fmt.Sprintf(`{"errors":["%s"]}`, err.Error()), http.StatusInternalServerError)
			return
		}
		buildFile.UploadURL = h.absoluteURL(r, "/wharf/upload-sessions/"+session.ID)
	} else {
		buildFile.UploadURL, err = h.GetPresignedUploadURL(storagePath, time.Hour)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"errors":["failed to generate upload URL: %s"]}`, err.Error()), http.StatusInternalServerError)
			return
		}
		uploadHeaders["Content-Type"] = "application/octet-stream"
	}
	if err = h.db.UpdateBuildFile(buildFile); err != nil {
		http.Error(w, fmt.Sprintf(`{"errors":["%s"]}`, err.Error()), http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"file": map[string]interface{}{
			"id":            buildFile.ID,
			"uploadUrl":     buildFile.UploadURL,
			"uploadParams":  map[string]interface{}{},
			"uploadHeaders": uploadHeaders,
		},
	}

	fmt.Printf("CreateBuildFile response: %+v\n", response)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *WharfHandlers) FinalizeBuildFile(w http.ResponseWriter, r *http.Request) {
	buildIDStr := mux.Vars(r)["buildId"]
	fileIDStr := mux.Vars(r)["fileId"]

	buildID, err := strconv.ParseInt(buildIDStr, 10, 64)
	if err != nil {
		http.Error(w, `{"errors":["invalid build id"]}`, http.StatusBadRequest)
		return
	}

	fileID, err := strconv.ParseInt(fileIDStr, 10, 64)
	if err != nil {
		http.Error(w, `{"errors":["invalid file id"]}`, http.StatusBadRequest)
		return
	}

	build, err := h.db.GetBuildByID(buildID)
	if err != nil {
		http.Error(w, `{"errors":["build not found"]}`, http.StatusNotFound)
		return
	}
	if err = h.authorizeLoadedBuildAccess(r, build, nil); err != nil {
		writeBuildAccessError(w, r)
		return
	}

	var req struct {
		Size int64 `json:"size"`
	}

	if ok := decodeJSONOrFormRequest(w, r, &req, func() error {
		sizeStr := r.Form.Get("size")
		if sizeStr != "" {
			req.Size, err = strconv.ParseInt(sizeStr, 10, 64)
			if err != nil {
				fmt.Printf("Size parsing error: %v\n", err)
				return fmt.Errorf("invalid size: %s", err.Error())
			}
		}
		return nil
	}); !ok {
		return
	}

	var buildFile *models.BuildFile
	buildFile, err = h.db.GetBuildFileByID(fileID)
	if err != nil {
		http.Error(w, `{"errors":["build file not found"]}`, http.StatusNotFound)
		return
	}

	if buildFile.BuildID != buildID {
		http.Error(w, `{"errors":["build file does not belong to build"]}`, http.StatusBadRequest)
		return
	}

	if !h.FileExists(buildFile.StoragePath) {
		http.Error(w, `{"errors":["file not found in storage - upload may have failed"]}`, http.StatusBadRequest)
		return
	}

	actualSize, err := h.GetFileSize(buildFile.StoragePath)
	if err != nil {
		http.Error(w, `{"errors":["could not verify file size in storage"]}`, http.StatusInternalServerError)
		return
	}
	if req.Size > 0 && actualSize != req.Size {
		buildFile.State = "failed"
		_ = h.db.UpdateBuildFile(buildFile)
		if err = h.checkAndUpdateBuildState(buildID); err != nil {
			fmt.Printf("Warning: Failed to mark build failed after size mismatch: %v\n", err)
		}
		http.Error(w, `{"errors":["uploaded size does not match finalized size"]}`, http.StatusBadRequest)
		return
	}

	buildFile.Size = actualSize
	buildFile.State = "uploaded"

	err = h.db.UpdateBuildFile(buildFile)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"errors":["%s"]}`, err.Error()), http.StatusInternalServerError)
		return
	}

	err = h.checkAndUpdateBuildState(buildID)
	if err != nil {
		fmt.Printf("Warning: Failed to update build state: %v\n", err)
	}

	response := map[string]interface{}{
		"file": map[string]interface{}{
			"id":    buildFile.ID,
			"size":  buildFile.Size,
			"state": buildFile.State,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
