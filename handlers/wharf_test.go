package handlers

import (
	"denkit-stash/auth"
	"denkit-stash/internal/testdb"
	"denkit-stash/models"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/itchio/lake/tlc"
	"github.com/itchio/wharf/pwr"
)

func newTestWharfHandler(t *testing.T) (*WharfHandlers, models.Database, *models.Upload, *models.Channel) {
	t.Helper()

	db := testdb.New(t)

	user := &models.User{Username: "testuser", DisplayName: "Test User", APIKey: "test-key", Role: "user", IsActive: true}
	if err := db.CreateUser(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	game := &models.Game{UserID: user.ID, Title: "test-game", Type: "default", Classification: "game"}
	if err := db.CreateGame(game); err != nil {
		t.Fatalf("create game: %v", err)
	}
	upload := &models.Upload{GameID: game.ID, Filename: "test-game.zip", DisplayName: "test-game", Storage: "hosted", Type: "default", Platforms: "[]"}
	if err := db.CreateUpload(upload); err != nil {
		t.Fatalf("create upload: %v", err)
	}
	channel := &models.Channel{Name: "main", UploadID: upload.ID}
	if err := db.CreateChannel(channel); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	return NewWharfHandlers(db, nil, "test-bucket"), db, upload, channel
}

func createBuild(t *testing.T, db models.Database, uploadID int64, parentBuildID *int64) *models.Build {
	t.Helper()
	build := &models.Build{
		UploadID:      uploadID,
		ChannelName:   "main",
		ParentBuildID: parentBuildID,
		State:         "started",
	}
	if err := db.CreateBuild(build); err != nil {
		t.Fatalf("create build: %v", err)
	}
	return build
}

func createUploadedBuildFile(t *testing.T, db models.Database, buildID int64, fileType string, subType string) *models.BuildFile {
	t.Helper()
	file := &models.BuildFile{
		BuildID:     buildID,
		Type:        fileType,
		SubType:     subType,
		State:       "uploaded",
		Size:        42,
		StoragePath: "test/object",
	}
	if err := db.CreateBuildFile(file); err != nil {
		t.Fatalf("create build file: %v", err)
	}
	return file
}

func createTestUser(t *testing.T, db models.Database, username string, role string) *models.User {
	t.Helper()
	user := &models.User{Username: username, DisplayName: username, APIKey: username + "-key", Role: role, IsActive: true}
	if err := db.CreateUser(user); err != nil {
		t.Fatalf("create user %q: %v", username, err)
	}
	return user
}

func TestPlatformsForChannelNameMatchesButlerPlatformTags(t *testing.T) {
	tests := map[string]string{
		"win-linux-mac-stable": `["windows","linux","osx"]`,
		"windows-demo":         `["windows"]`,
		"WIN_linux-MAC-stable": `["windows","linux","osx"]`,
		"linux-nightly":        `["linux"]`,
		"osx-beta":             `["osx"]`,
		"android-release":      `["android"]`,
		"stable":               `[]`,
		"darwin-experimental":  `[]`,
		"twinkle-release":      `[]`,
		"windowless-demo":      `[]`,
	}

	for channel, expected := range tests {
		t.Run(channel, func(t *testing.T) {
			if got := platformsForChannelName(channel); got != expected {
				t.Fatalf("platformsForChannelName(%q) = %s, want %s", channel, got, expected)
			}
		})
	}
}

func TestCreateBuildCreatesUploadWithChannelPlatforms(t *testing.T) {
	handler, db, upload, _ := newTestWharfHandler(t)
	defer db.Close()

	user, err := db.GetUserByUsername("testuser")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/wharf/builds", strings.NewReader(`{
		"target": "testuser/test-game",
		"channel": "win-linux-mac-stable",
		"user_version": "1.0"
	}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.SetUser(req.Context(), user))
	rec := httptest.NewRecorder()

	handler.CreateBuild(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	uploads, err := db.GetUploadsByGameID(upload.GameID)
	if err != nil {
		t.Fatalf("get uploads: %v", err)
	}

	var taggedUpload *models.Upload
	for _, candidate := range uploads {
		if candidate.ID != upload.ID {
			taggedUpload = candidate
			break
		}
	}
	if taggedUpload == nil {
		t.Fatalf("expected build creation to create a channel-specific upload")
	}
	if taggedUpload.Platforms != `["windows","linux","osx"]` {
		t.Fatalf("expected inferred platforms, got %s", taggedUpload.Platforms)
	}
}

func TestCreateBuildRejectsOversizedControlRequest(t *testing.T) {
	t.Setenv(maxRequestBodyLimitEnv, "32")

	handler, db, _, _ := newTestWharfHandler(t)
	defer db.Close()

	user, err := db.GetUserByUsername("testuser")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/wharf/builds", strings.NewReader(strings.Repeat("x", 33)))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.SetUser(req.Context(), user))
	rec := httptest.NewRecorder()

	handler.CreateBuild(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected oversized request to return 413, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPutUploadSessionRejectsChunksOverQuota(t *testing.T) {
	t.Setenv(maxUploadSessionBytesEnv, "4")

	handler, db, upload, _ := newTestWharfHandler(t)
	defer db.Close()
	build := createBuild(t, db, upload.ID, nil)
	buildFile := createUploadedBuildFile(t, db, build.ID, "patch", "default")

	session := &models.UploadSession{
		ID:          "session-over-quota",
		BuildFileID: buildFile.ID,
		StoragePath: "builds/1/patch_default",
		State:       "active",
	}
	if err := db.CreateUploadSession(session); err != nil {
		t.Fatalf("create upload session: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/wharf/upload-sessions/session-over-quota", strings.NewReader("hello"))
	req.Header.Set("Content-Range", "bytes 0-4/*")
	req = mux.SetURLVars(req, map[string]string{"id": session.ID})
	rec := httptest.NewRecorder()

	handler.PutUploadSession(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected over-quota chunk to return 413, got %d: %s", rec.Code, rec.Body.String())
	}

	updated, err := db.GetUploadSessionByID(session.ID)
	if err != nil {
		t.Fatalf("get upload session: %v", err)
	}
	if updated.Size != 0 {
		t.Fatalf("expected rejected chunk not to advance session size, got %d", updated.Size)
	}
}

func TestPutUploadSessionRejectsFinalSizeOverQuota(t *testing.T) {
	t.Setenv(maxUploadSessionBytesEnv, "4")

	handler, db, upload, _ := newTestWharfHandler(t)
	defer db.Close()
	build := createBuild(t, db, upload.ID, nil)
	buildFile := createUploadedBuildFile(t, db, build.ID, "patch", "default")

	session := &models.UploadSession{
		ID:          "session-final-over-quota",
		BuildFileID: buildFile.ID,
		StoragePath: "builds/1/patch_default",
		Size:        4,
		State:       "active",
	}
	if err := db.CreateUploadSession(session); err != nil {
		t.Fatalf("create upload session: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/wharf/upload-sessions/session-final-over-quota", nil)
	req.Header.Set("Content-Range", "bytes 4--1/5")
	req = mux.SetURLVars(req, map[string]string{"id": session.ID})
	rec := httptest.NewRecorder()

	handler.PutUploadSession(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected over-quota final size to return 413, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCheckAndUpdateBuildStateDoesNotAdvanceIncompleteBuild(t *testing.T) {
	handler, db, upload, channel := newTestWharfHandler(t)
	defer db.Close()

	build := createBuild(t, db, upload.ID, nil)
	createUploadedBuildFile(t, db, build.ID, "patch", "default")

	if err := handler.checkAndUpdateBuildState(build.ID); err != nil {
		t.Fatalf("check build state: %v", err)
	}

	updatedBuild, err := db.GetBuildByID(build.ID)
	if err != nil {
		t.Fatalf("get build: %v", err)
	}
	if updatedBuild.State != "started" {
		t.Fatalf("expected started state while required files are missing, got %q", updatedBuild.State)
	}

	updatedChannel, err := db.GetChannelByName(channel.Name, channel.UploadID)
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	if updatedChannel.CurrentBuildID != nil {
		t.Fatalf("incomplete build advanced channel head to %d", *updatedChannel.CurrentBuildID)
	}
}

func TestGetBuildDownloadByTypeRequiresAuthenticatedUser(t *testing.T) {
	handler, db, upload, _ := newTestWharfHandler(t)
	defer db.Close()

	build := createBuild(t, db, upload.ID, nil)
	createUploadedBuildFile(t, db, build.ID, "archive", "default")

	req := httptest.NewRequest(http.MethodGet, "/builds/1/download/archive/default", nil)
	req = mux.SetURLVars(req, map[string]string{
		"buildId": strconv.FormatInt(build.ID, 10),
		"type":    "archive",
		"subType": "default",
	})
	rec := httptest.NewRecorder()

	handler.GetBuildDownloadByType(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated download to return 401, got %d", rec.Code)
	}
}

func TestGetBuildDownloadByTypeRejectsOtherNamespace(t *testing.T) {
	handler, db, upload, _ := newTestWharfHandler(t)
	defer db.Close()

	build := createBuild(t, db, upload.ID, nil)
	createUploadedBuildFile(t, db, build.ID, "archive", "default")
	otherUser := createTestUser(t, db, "otheruser", "user")

	req := httptest.NewRequest(http.MethodGet, "/builds/1/download/archive/default", nil)
	req = req.WithContext(auth.SetUser(req.Context(), otherUser))
	req = mux.SetURLVars(req, map[string]string{
		"buildId": strconv.FormatInt(build.ID, 10),
		"type":    "archive",
		"subType": "default",
	})
	rec := httptest.NewRecorder()

	handler.GetBuildDownloadByType(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected cross-namespace download to return 403, got %d", rec.Code)
	}
}

func TestBuildFileEndpointsRejectOtherNamespace(t *testing.T) {
	handler, db, upload, _ := newTestWharfHandler(t)
	defer db.Close()

	build := createBuild(t, db, upload.ID, nil)
	file := createUploadedBuildFile(t, db, build.ID, "archive", "default")
	otherUser := createTestUser(t, db, "otheruser", "user")

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		vars   map[string]string
		call   func(http.ResponseWriter, *http.Request)
	}{
		{
			name:   "list",
			method: http.MethodGet,
			path:   "/wharf/builds/1/files",
			vars: map[string]string{
				"id": strconv.FormatInt(build.ID, 10),
			},
			call: handler.GetBuildFiles,
		},
		{
			name:   "create",
			method: http.MethodPost,
			path:   "/wharf/builds/1/files",
			body:   `{"type":"patch","sub_type":"default","upload_type":"deferred_resumable"}`,
			vars: map[string]string{
				"id": strconv.FormatInt(build.ID, 10),
			},
			call: handler.CreateBuildFile,
		},
		{
			name:   "finalize",
			method: http.MethodPost,
			path:   "/wharf/builds/1/files/1",
			body:   `{"size":42}`,
			vars: map[string]string{
				"buildId": strconv.FormatInt(build.ID, 10),
				"fileId":  strconv.FormatInt(file.ID, 10),
			},
			call: handler.FinalizeBuildFile,
		},
		{
			name:   "download",
			method: http.MethodGet,
			path:   "/wharf/builds/1/files/1/download",
			vars: map[string]string{
				"buildId": strconv.FormatInt(build.ID, 10),
				"fileId":  strconv.FormatInt(file.ID, 10),
			},
			call: handler.GetBuildFileDownload,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(auth.SetUser(req.Context(), otherUser))
			req = mux.SetURLVars(req, tt.vars)
			rec := httptest.NewRecorder()

			tt.call(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected cross-namespace %s to return 403, got %d: %s", tt.name, rec.Code, rec.Body.String())
			}
		})
	}

	buildFiles, err := db.GetBuildFilesByBuildID(build.ID)
	if err != nil {
		t.Fatalf("get build files: %v", err)
	}
	if len(buildFiles) != 1 {
		t.Fatalf("cross-namespace create changed build files, got %d files", len(buildFiles))
	}

	updatedBuild, err := db.GetBuildByID(build.ID)
	if err != nil {
		t.Fatalf("get build: %v", err)
	}
	if updatedBuild.State != "started" {
		t.Fatalf("cross-namespace finalize changed build state to %q", updatedBuild.State)
	}
}

func TestGetLatestChannelArchiveRequiresAuthenticatedUser(t *testing.T) {
	handler, db, _, _ := newTestWharfHandler(t)
	defer db.Close()

	req := httptest.NewRequest(http.MethodGet, "/testuser/test-game/main/archive/default", nil)
	req = mux.SetURLVars(req, map[string]string{
		"namespace": "testuser",
		"game":      "test-game",
		"channel":   "main",
	})
	rec := httptest.NewRecorder()

	handler.GetLatestChannelArchive(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated archive request to return 401, got %d", rec.Code)
	}
}

func TestGetLatestChannelArchiveRejectsOtherNamespace(t *testing.T) {
	handler, db, _, _ := newTestWharfHandler(t)
	defer db.Close()
	otherUser := createTestUser(t, db, "otheruser", "user")

	req := httptest.NewRequest(http.MethodGet, "/testuser/test-game/main/archive/default", nil)
	req = req.WithContext(auth.SetUser(req.Context(), otherUser))
	req = mux.SetURLVars(req, map[string]string{
		"namespace": "testuser",
		"game":      "test-game",
		"channel":   "main",
	})
	rec := httptest.NewRecorder()

	handler.GetLatestChannelArchive(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected cross-namespace archive request to return 403, got %d", rec.Code)
	}
}

func TestGetUpgradePathRequiresAuthenticatedUser(t *testing.T) {
	handler, db, upload, _ := newTestWharfHandler(t)
	defer db.Close()

	installed := createBuild(t, db, upload.ID, nil)
	target := createBuild(t, db, upload.ID, &installed.ID)

	req := httptest.NewRequest(http.MethodGet, "/builds/1/upgrade-paths/2", nil)
	req = mux.SetURLVars(req, map[string]string{
		"installedBuildId": strconv.FormatInt(installed.ID, 10),
		"targetBuildId":    strconv.FormatInt(target.ID, 10),
	})
	rec := httptest.NewRecorder()

	handler.GetUpgradePath(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated upgrade path request to return 401, got %d", rec.Code)
	}
}

func TestGetUpgradePathRejectsOtherNamespace(t *testing.T) {
	handler, db, upload, _ := newTestWharfHandler(t)
	defer db.Close()

	installed := createBuild(t, db, upload.ID, nil)
	target := createBuild(t, db, upload.ID, &installed.ID)
	otherUser := createTestUser(t, db, "otheruser", "user")

	req := httptest.NewRequest(http.MethodGet, "/builds/1/upgrade-paths/2", nil)
	req = req.WithContext(auth.SetUser(req.Context(), otherUser))
	req = mux.SetURLVars(req, map[string]string{
		"installedBuildId": strconv.FormatInt(installed.ID, 10),
		"targetBuildId":    strconv.FormatInt(target.ID, 10),
	})
	rec := httptest.NewRecorder()

	handler.GetUpgradePath(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected cross-namespace upgrade path request to return 403, got %d", rec.Code)
	}
}

func TestCheckAndUpdateBuildStateMarksFailedBuildWithoutAdvancingChannel(t *testing.T) {
	handler, db, upload, channel := newTestWharfHandler(t)
	defer db.Close()

	build := createBuild(t, db, upload.ID, nil)
	file := createUploadedBuildFile(t, db, build.ID, "patch", "default")
	file.State = "failed"
	if err := db.UpdateBuildFile(file); err != nil {
		t.Fatalf("update build file: %v", err)
	}

	if err := handler.checkAndUpdateBuildState(build.ID); err != nil {
		t.Fatalf("check build state: %v", err)
	}

	updatedBuild, err := db.GetBuildByID(build.ID)
	if err != nil {
		t.Fatalf("get build: %v", err)
	}
	if updatedBuild.State != "failed" {
		t.Fatalf("expected failed state, got %q", updatedBuild.State)
	}

	updatedChannel, err := db.GetChannelByName(channel.Name, channel.UploadID)
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	if updatedChannel.CurrentBuildID != nil {
		t.Fatalf("failed build advanced channel head to %d", *updatedChannel.CurrentBuildID)
	}
}

func TestCheckAndUpdateBuildStateAdvancesCompletedBuild(t *testing.T) {
	handler, db, upload, channel := newTestWharfHandler(t)
	defer db.Close()

	build := createBuild(t, db, upload.ID, nil)
	createUploadedBuildFile(t, db, build.ID, "patch", "default")
	createUploadedBuildFile(t, db, build.ID, "signature", "default")
	createUploadedBuildFile(t, db, build.ID, "archive", "default")

	if err := handler.checkAndUpdateBuildState(build.ID); err != nil {
		t.Fatalf("check build state: %v", err)
	}

	updatedBuild, err := db.GetBuildByID(build.ID)
	if err != nil {
		t.Fatalf("get build: %v", err)
	}
	if updatedBuild.State != "completed" {
		t.Fatalf("expected completed state, got %q", updatedBuild.State)
	}

	updatedChannel, err := db.GetChannelByName(channel.Name, channel.UploadID)
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	if updatedChannel.CurrentBuildID == nil || *updatedChannel.CurrentBuildID != build.ID {
		t.Fatalf("completed build did not advance channel head: %#v", updatedChannel.CurrentBuildID)
	}

	updatedUpload, err := db.GetUploadByID(upload.ID)
	if err != nil {
		t.Fatalf("get upload: %v", err)
	}
	if updatedUpload.Size != 42 {
		t.Fatalf("expected upload size to reflect archive size, got %d", updatedUpload.Size)
	}
}

func TestCheckAndUpdateBuildStateCompletesProcessingBuild(t *testing.T) {
	handler, db, upload, channel := newTestWharfHandler(t)
	defer db.Close()

	build := createBuild(t, db, upload.ID, nil)
	build.State = "processing"
	if err := db.UpdateBuild(build); err != nil {
		t.Fatalf("update build: %v", err)
	}
	createUploadedBuildFile(t, db, build.ID, "patch", "default")
	createUploadedBuildFile(t, db, build.ID, "signature", "default")
	createUploadedBuildFile(t, db, build.ID, "archive", "default")

	if err := handler.checkAndUpdateBuildState(build.ID); err != nil {
		t.Fatalf("check build state: %v", err)
	}

	updatedBuild, err := db.GetBuildByID(build.ID)
	if err != nil {
		t.Fatalf("get build: %v", err)
	}
	if updatedBuild.State != "completed" {
		t.Fatalf("expected completed state, got %q", updatedBuild.State)
	}

	updatedChannel, err := db.GetChannelByName(channel.Name, channel.UploadID)
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	if updatedChannel.CurrentBuildID == nil || *updatedChannel.CurrentBuildID != build.ID {
		t.Fatalf("completed processing build did not advance channel head: %#v", updatedChannel.CurrentBuildID)
	}
}

func TestResolveUpgradePathRequiresParentChainAndPatch(t *testing.T) {
	handler, db, upload, _ := newTestWharfHandler(t)
	defer db.Close()

	first := createBuild(t, db, upload.ID, nil)
	first.State = "completed"
	if err := db.UpdateBuild(first); err != nil {
		t.Fatalf("update first build: %v", err)
	}
	second := createBuild(t, db, upload.ID, &first.ID)
	second.State = "completed"
	if err := db.UpdateBuild(second); err != nil {
		t.Fatalf("update second build: %v", err)
	}
	createUploadedBuildFile(t, db, second.ID, "patch", "default")

	path, err := handler.resolveUpgradePath(first.ID, second.ID)
	if err != nil {
		t.Fatalf("resolve upgrade path: %v", err)
	}
	if len(path) != 2 || path[0].ID != first.ID || path[1].ID != second.ID {
		t.Fatalf("unexpected upgrade path: %#v", path)
	}
}

func TestValidatePatchTargetContainerRequiresEmptyTargetForInitialBuild(t *testing.T) {
	build := &models.Build{}
	if err := validatePatchTargetContainer(build, &tlc.Container{}, nil); err != nil {
		t.Fatalf("expected empty target to pass: %v", err)
	}

	nonEmptyTarget := &tlc.Container{
		Files: []*tlc.File{{Path: "game.txt", Size: 15}},
		Size:  15,
	}
	if err := validatePatchTargetContainer(build, nonEmptyTarget, nil); err == nil {
		t.Fatalf("expected non-empty target to fail for initial build")
	}
}

func TestValidatePatchTargetContainerRequiresParentSignatureMatch(t *testing.T) {
	parentID := int64(1)
	build := &models.Build{ParentBuildID: &parentID}
	parentContainer := &tlc.Container{
		Files: []*tlc.File{{Path: "game.txt", Size: 15}},
		Size:  15,
	}
	parentSignature := &pwr.SignatureInfo{Container: parentContainer}

	if err := validatePatchTargetContainer(build, parentContainer, parentSignature); err != nil {
		t.Fatalf("expected matching parent signature to pass: %v", err)
	}
	if err := validatePatchTargetContainer(build, parentContainer, nil); err == nil {
		t.Fatalf("expected missing parent signature to fail")
	}

	mismatchedTarget := &tlc.Container{
		Files: []*tlc.File{{Path: "game.txt", Size: 16}},
		Size:  16,
	}
	if err := validatePatchTargetContainer(build, mismatchedTarget, parentSignature); err == nil {
		t.Fatalf("expected mismatched parent signature to fail")
	}
}

func TestCreateDownloadSessionReturnsUUID(t *testing.T) {
	_, db, upload, _ := newTestWharfHandler(t)
	defer db.Close()

	router := mux.NewRouter()
	router.HandleFunc("/games/{id}/download-sessions", NewCoreHandlers(db).CreateDownloadSession).Methods("POST")

	req := httptest.NewRequest(http.MethodPost, "/games/"+strconv.FormatInt(upload.GameID, 10)+"/download-sessions", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var response struct {
		UUID string `json:"uuid"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.UUID == "" {
		t.Fatalf("expected uuid in response")
	}
}
