package handlers

import (
	"denkit-stash/auth"
	"denkit-stash/models"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (h *WharfHandlers) CreateBuild(w http.ResponseWriter, r *http.Request) {
	user := auth.MustGetUser(r.Context())

	var req CreateBuildRequest

	if ok := decodeJSONOrFormRequest(w, r, &req, func() error {
		req.Target = r.Form.Get("target")
		req.Channel = r.Form.Get("channel")
		req.UserVersion = r.Form.Get("user_version")
		return nil
	}); !ok {
		return
	}

	if req.Target == "" {
		writeError(w, http.StatusBadRequest, "missing target")
		return
	}

	parts := strings.Split(req.Target, "/")
	if len(parts) != 2 {
		writeError(w, http.StatusBadRequest, "invalid target format")
		return
	}

	username, gameName := parts[0], parts[1]

	err := h.validateNamespaceAccess(user, username)
	if err != nil {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}

	namespaceOwner, err := h.db.GetUserByUsername(username)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("namespace owner not found: %s", username))
		return
	}

	var games []*models.Game
	games, err = h.db.GetGamesByUserID(namespaceOwner.ID)
	if err != nil {
		writeInternalError(w, err)
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
			writeInternalError(w, err)
			return
		}
	}

	var uploads []*models.Upload
	uploads, err = h.db.GetUploadsByGameID(game.ID)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	var upload *models.Upload

	for _, existingUpload := range uploads {
		_, channelErr := h.db.GetChannelByName(req.Channel, existingUpload.ID)
		if channelErr == nil {
			upload = existingUpload
			channelPlatforms := platformsForTokens(channelNameTokens(req.Channel))
			if upload.Platforms != channelPlatforms {
				upload.Platforms = channelPlatforms
				if err = h.db.UpdateUpload(upload); err != nil {
					writeInternalError(w, err)
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
			Platforms:   platformsForTokens(channelNameTokens(req.Channel)),
		}

		err = h.db.CreateUpload(upload)
		if err != nil {
			writeInternalError(w, err)
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
		writeInternalError(w, err)
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
			writeInternalError(w, err)
			return
		}
	}

	writeJSON(w, http.StatusOK, WharfBuildEnvelopeResponse{Build: newWharfBuildResponse(build, nil)})
}

func (h *WharfHandlers) GetBuildFiles(w http.ResponseWriter, r *http.Request) {
	buildID, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid build id")
		return
	}

	build, err := h.db.GetBuildByID(buildID)
	if err != nil {
		writeError(w, http.StatusNotFound, "build not found")
		return
	}
	if err = h.authorizeLoadedBuildAccess(r, build); err != nil {
		writeBuildAccessError(w, r)
		return
	}

	var buildFiles []*models.BuildFile
	buildFiles, err = h.db.GetBuildFilesByBuildID(buildID)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	var filesResponse []WharfBuildFileResponse
	for _, file := range buildFiles {
		filesResponse = append(filesResponse, newWharfBuildFileResponse(file))
	}
	writeJSON(w, http.StatusOK, BuildFilesResponse{Files: filesResponse})
}

func (h *WharfHandlers) CreateBuildFile(w http.ResponseWriter, r *http.Request) {
	buildID, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid build id")
		return
	}

	build, err := h.db.GetBuildByID(buildID)
	if err != nil {
		writeError(w, http.StatusNotFound, "build not found")
		return
	}
	if err = h.authorizeLoadedBuildAccess(r, build); err != nil {
		writeBuildAccessError(w, r)
		return
	}

	var req CreateBuildFileRequest

	if ok := decodeJSONOrFormRequest(w, r, &req, func() error {
		req.Type = r.Form.Get("type")
		req.SubType = r.Form.Get("sub_type")
		req.UploadType = r.Form.Get("upload_type")
		return nil
	}); !ok {
		return
	}

	if req.Type == "" {
		writeError(w, http.StatusBadRequest, "missing type")
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
		writeInternalError(w, err)
		return
	}

	uploadHeaders := map[string]string{}
	if req.UploadType == "deferred_resumable" || req.UploadType == "deferred-resumable" {
		session := &models.UploadSession{
			ID:          uuid.New().String(),
			BuildFileID: buildFile.ID,
			StoragePath: buildFile.StoragePath,
			State:       "active",
		}
		if err = h.db.CreateUploadSession(session); err != nil {
			writeInternalError(w, err)
			return
		}
		buildFile.UploadURL = h.absoluteURL(r, "/wharf/upload-sessions/"+session.ID)
	} else {
		buildFile.UploadURL, err = h.GetPresignedUploadURL(storagePath, time.Hour)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		uploadHeaders["Content-Type"] = "application/octet-stream"
	}
	if err = h.db.UpdateBuildFile(buildFile); err != nil {
		writeInternalError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, BuildFileUploadEnvelopeResponse{File: BuildFileUploadResponse{
		ID: buildFile.ID, UploadURL: buildFile.UploadURL,
		UploadParams: map[string]string{}, UploadHeaders: uploadHeaders,
	}})
}

func (h *WharfHandlers) FinalizeBuildFile(w http.ResponseWriter, r *http.Request) {
	buildID, err := pathInt64(r, "buildId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid build id")
		return
	}

	fileID, err := pathInt64(r, "fileId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid file id")
		return
	}

	build, err := h.db.GetBuildByID(buildID)
	if err != nil {
		writeError(w, http.StatusNotFound, "build not found")
		return
	}
	if err = h.authorizeLoadedBuildAccess(r, build); err != nil {
		writeBuildAccessError(w, r)
		return
	}

	var req FinalizeBuildFileRequest

	if ok := decodeJSONOrFormRequest(w, r, &req, func() error {
		sizeStr := r.Form.Get("size")
		if sizeStr != "" {
			req.Size, err = strconv.ParseInt(sizeStr, 10, 64)
			if err != nil {
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
		writeError(w, http.StatusNotFound, "build file not found")
		return
	}

	if buildFile.BuildID != buildID {
		writeError(w, http.StatusBadRequest, "build file does not belong to build")
		return
	}

	if !h.FileExists(buildFile.StoragePath) {
		writeError(w, http.StatusBadRequest, "file not found in storage - upload may have failed")
		return
	}

	actualSize, err := h.GetFileSize(buildFile.StoragePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not verify file size in storage")
		return
	}
	if req.Size > 0 && actualSize != req.Size {
		buildFile.State = "failed"
		_ = h.db.UpdateBuildFile(buildFile)
		if err = h.checkAndUpdateBuildState(buildID); err != nil {
			log.Printf("warning: failed to mark build failed after size mismatch: %v", err)
		}
		writeError(w, http.StatusBadRequest, "uploaded size does not match finalized size")
		return
	}

	buildFile.Size = actualSize
	buildFile.State = "uploaded"

	err = h.db.UpdateBuildFile(buildFile)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	go func() {
		if err := h.checkAndUpdateBuildState(buildID); err != nil {
			log.Printf("warning: failed to update build %d state: %v", buildID, err)
		}
	}()

	writeJSON(w, http.StatusOK, FinalizedBuildFileEnvelopeResponse{File: FinalizedBuildFileResponse{
		ID: buildFile.ID, Size: buildFile.Size, State: buildFile.State,
	}})
}
