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

	"github.com/gorilla/mux"
)

// GET /wharf/builds/{buildId}/files/{fileId}/download - Download build file.
func (h *WharfHandlers) GetBuildFileDownload(w http.ResponseWriter, r *http.Request) {
	buildIDStr := mux.Vars(r)["buildId"]
	fileIDStr := mux.Vars(r)["fileId"]

	fmt.Printf("GetBuildFileDownload request: buildId=%s, fileId=%s\n", buildIDStr, fileIDStr)

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

	buildFile, err := h.db.GetBuildFileByID(fileID)
	if err != nil {
		http.Error(w, `{"errors":["build file not found"]}`, http.StatusNotFound)
		return
	}
	if buildFile.BuildID != buildID {
		http.Error(w, `{"errors":["build file does not belong to build"]}`, http.StatusBadRequest)
		return
	}
	if buildFile.Type == "archive" && buildFile.SubType == "default" {
		h.serveArchiveDownload(w, r, buildID)
		return
	}
	h.redirectBuildFile(w, r, buildFile)
}

// serveArchiveDownload serves an archive/default download through the archive
// cache: warm archives redirect immediately, evicted ones block while the
// patch chain is replayed.
func (h *WharfHandlers) serveArchiveDownload(w http.ResponseWriter, r *http.Request, buildID int64) {
	buildFile, err := h.ensureArchive(r.Context(), buildID)
	if err != nil {
		fmt.Printf("Failed to ensure archive for build %d: %v\n", buildID, err)
		http.Error(w, `{"errors":["archive unavailable"]}`, http.StatusNotFound)
		return
	}
	h.redirectBuildFile(w, r, buildFile)
}

// GET /builds/{buildId}/download/{type}/{subType}
func (h *WharfHandlers) GetBuildDownloadByType(w http.ResponseWriter, r *http.Request) {
	buildID, err := strconv.ParseInt(mux.Vars(r)["buildId"], 10, 64)
	if err != nil {
		http.Error(w, `{"errors":["invalid build id"]}`, http.StatusBadRequest)
		return
	}
	fileType := mux.Vars(r)["type"]
	subType := mux.Vars(r)["subType"]

	if err := h.authorizeBuildAccess(r, buildID); err != nil {
		if _, ok := auth.GetUser(r.Context()); !ok {
			http.Error(w, `{"errors":["missing api_key"]}`, http.StatusUnauthorized)
			return
		}
		http.Error(w, `{"errors":["access denied"]}`, http.StatusForbidden)
		return
	}

	if fileType == "archive" && subType == "default" {
		h.serveArchiveDownload(w, r, buildID)
		return
	}

	buildFile, err := h.findBuildFile(buildID, fileType, subType)
	if err != nil {
		http.Error(w, `{"errors":["build file not found"]}`, http.StatusNotFound)
		return
	}
	h.redirectBuildFile(w, r, buildFile)
}

// GET /{namespace}/{game}/{channel}/archive/default
func (h *WharfHandlers) GetLatestChannelArchive(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	gameName := vars["game"]
	channelName := vars["channel"]

	requestUser, ok := auth.GetUser(r.Context())
	if !ok {
		http.Error(w, `{"errors":["missing api_key"]}`, http.StatusUnauthorized)
		return
	}
	if err := h.validateNamespaceAccess(requestUser, namespace); err != nil {
		http.Error(w, `{"errors":["access denied"]}`, http.StatusForbidden)
		return
	}

	user, err := h.db.GetUserByUsername(namespace)
	if err != nil {
		http.Error(w, `{"errors":["namespace not found"]}`, http.StatusNotFound)
		return
	}
	game, err := h.db.GetGameByUserAndTitle(user.ID, gameName)
	if err != nil {
		http.Error(w, `{"errors":["game not found"]}`, http.StatusNotFound)
		return
	}
	uploads, err := h.db.GetUploadsByGameID(game.ID)
	if err != nil {
		http.Error(w, `{"errors":["failed to load uploads"]}`, http.StatusInternalServerError)
		return
	}
	for _, upload := range uploads {
		channel, err := h.db.GetChannelByName(channelName, upload.ID)
		if err != nil || channel.CurrentBuildID == nil {
			continue
		}
		buildFile, err := h.findBuildFileAnyState(*channel.CurrentBuildID, "archive", "default")
		if err == nil && buildFile != nil {
			h.serveArchiveDownload(w, r, *channel.CurrentBuildID)
			return
		}
	}
	http.Error(w, `{"errors":["archive not found"]}`, http.StatusNotFound)
}

// GET /builds/{installedBuildId}/upgrade-paths/{targetBuildId}
func (h *WharfHandlers) GetUpgradePath(w http.ResponseWriter, r *http.Request) {
	installedBuildID, err := strconv.ParseInt(mux.Vars(r)["installedBuildId"], 10, 64)
	if err != nil {
		http.Error(w, `{"errors":["invalid installed build id"]}`, http.StatusBadRequest)
		return
	}
	targetBuildID, err := strconv.ParseInt(mux.Vars(r)["targetBuildId"], 10, 64)
	if err != nil {
		http.Error(w, `{"errors":["invalid target build id"]}`, http.StatusBadRequest)
		return
	}

	if err := h.authorizeBuildAccess(r, targetBuildID); err != nil {
		if _, ok := auth.GetUser(r.Context()); !ok {
			http.Error(w, `{"errors":["missing api_key"]}`, http.StatusUnauthorized)
			return
		}
		http.Error(w, `{"errors":["access denied"]}`, http.StatusForbidden)
		return
	}

	builds, err := h.resolveUpgradePath(installedBuildID, targetBuildID)
	if err != nil {
		http.Error(w, `{"errors":["upgrade path not found"]}`, http.StatusNotFound)
		return
	}

	responseBuilds := make([]map[string]interface{}, 0, len(builds))
	for _, build := range builds {
		files, err := h.db.GetBuildFilesByBuildID(build.ID)
		if err != nil {
			http.Error(w, `{"errors":["failed to load build files"]}`, http.StatusInternalServerError)
			return
		}
		responseBuilds = append(responseBuilds, serializeBuild(build, files))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"upgradePath": map[string]interface{}{
			"builds": responseBuilds,
		},
	})
}

func (h *WharfHandlers) resolveUpgradePath(installedBuildID int64, targetBuildID int64) ([]*models.Build, error) {
	target, err := h.db.GetBuildByID(targetBuildID)
	if err != nil {
		return nil, err
	}
	path := []*models.Build{target}
	current := target
	for current.ID != installedBuildID {
		if current.ParentBuildID == nil {
			return nil, fmt.Errorf("no parent build")
		}
		parent, err := h.db.GetBuildByID(*current.ParentBuildID)
		if err != nil {
			return nil, err
		}
		if parent.UploadID != target.UploadID || parent.ChannelName != target.ChannelName {
			return nil, fmt.Errorf("upgrade path crosses upload or channel")
		}
		path = append(path, parent)
		current = parent
	}

	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	for _, build := range path[1:] {
		if _, err := h.findBuildFile(build.ID, "patch", "default"); err != nil {
			return nil, err
		}
	}
	return path, nil
}

func (h *WharfHandlers) findBuildFile(buildID int64, fileType string, subType string) (*models.BuildFile, error) {
	files, err := h.db.GetBuildFilesByBuildID(buildID)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if file.Type == fileType && file.SubType == subType && file.State == "uploaded" {
			return file, nil
		}
	}
	return nil, fmt.Errorf("build file not found")
}

func (h *WharfHandlers) redirectBuildFile(w http.ResponseWriter, r *http.Request, buildFile *models.BuildFile) {
	if !h.FileExists(buildFile.StoragePath) {
		http.Error(w, `{"errors":["file not found in storage"]}`, http.StatusNotFound)
		return
	}
	signedURL, err := h.GetSignedURL(buildFile.StoragePath, time.Hour)
	if err != nil {
		http.Error(w, `{"errors":["could not generate download URL"]}`, http.StatusInternalServerError)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") || r.URL.Query().Get("json") == "1" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"url": signedURL})
		return
	}
	http.Redirect(w, r, signedURL, http.StatusTemporaryRedirect)
}
