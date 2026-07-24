package handlers

import (
	"denkit-stash/models"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// createVersionedBuild is createBuild plus a user version, which is what the
// download filename is built from.
func createVersionedBuild(t *testing.T, db models.Database, uploadID int64, userVersion string) *models.Build {
	t.Helper()
	build := &models.Build{
		UploadID:    uploadID,
		ChannelName: "main",
		UserVersion: userVersion,
		State:       "started",
	}
	if err := db.CreateBuild(build); err != nil {
		t.Fatalf("create build: %v", err)
	}
	return build
}

func TestDownloadFilenameUsesGameTitleAndUserVersion(t *testing.T) {
	handler, db, upload, _ := newTestWharfHandler(t)
	defer db.Close()

	build := createVersionedBuild(t, db, upload.ID, "1.4.2")
	archive := &models.BuildFile{
		BuildID:     build.ID,
		Type:        "archive",
		SubType:     "default",
		State:       "uploaded",
		StoragePath: fmt.Sprintf("builds/%d/archive_default_9f1c.zip", build.ID),
	}

	if got := handler.downloadFilename(archive); got != "test-game-1.4.2.zip" {
		t.Fatalf("expected test-game-1.4.2.zip, got %q", got)
	}
}

func TestDownloadFilenameKeepsMultiPartArchiveFormat(t *testing.T) {
	handler, db, upload, _ := newTestWharfHandler(t)
	defer db.Close()

	build := createVersionedBuild(t, db, upload.ID, "2.0")
	archive := &models.BuildFile{
		BuildID:     build.ID,
		Type:        "archive",
		SubType:     "default",
		State:       "uploaded",
		StoragePath: fmt.Sprintf("builds/%d/archive_default_9f1c.tar.gz", build.ID),
	}

	if got := handler.downloadFilename(archive); got != "test-game-2.0.tar.gz" {
		t.Fatalf("expected test-game-2.0.tar.gz, got %q", got)
	}
}

func TestDownloadFilenameFallsBackToBuildIDWithoutUserVersion(t *testing.T) {
	handler, db, upload, _ := newTestWharfHandler(t)
	defer db.Close()

	build := createVersionedBuild(t, db, upload.ID, "")
	archive := &models.BuildFile{
		BuildID:     build.ID,
		Type:        "archive",
		SubType:     "default",
		State:       "uploaded",
		StoragePath: fmt.Sprintf("builds/%d/archive_default_9f1c.zip", build.ID),
	}

	want := fmt.Sprintf("test-game-build-%d.zip", build.ID)
	if got := handler.downloadFilename(archive); got != want {
		t.Fatalf("expected %s, got %q", want, got)
	}
}

// Patch and signature files are fetched by butler, which does not care about
// the name; leaving them undecorated keeps their URLs exactly as before.
func TestDownloadFilenameEmptyForNonArchiveFiles(t *testing.T) {
	handler, db, upload, _ := newTestWharfHandler(t)
	defer db.Close()

	build := createVersionedBuild(t, db, upload.ID, "1.4.2")
	for _, fileType := range []string{"patch", "signature"} {
		file := &models.BuildFile{
			BuildID:     build.ID,
			Type:        fileType,
			SubType:     "default",
			State:       "uploaded",
			StoragePath: fmt.Sprintf("builds/%d/%s_default_9f1c", build.ID, fileType),
		}
		if got := handler.downloadFilename(file); got != "" {
			t.Fatalf("expected no download filename for %s file, got %q", fileType, got)
		}
	}
}

func TestSanitizeFilenamePart(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain slug", "dilmurs-tale", "dilmurs-tale"},
		{"semver", "1.4.2", "1.4.2"},
		{"spaces collapse to dashes", "Dilmur's  Tale", "Dilmur-s-Tale"},
		{"runs collapse to one dash", "a///b", "a-b"},
		{"path separators removed", "../../etc/passwd", "etc-passwd"},
		{"quotes removed", `v1"; rm -rf /`, "v1-rm-rf"},
		{"leading dot dropped", ".hidden", "hidden"},
		{"non-ascii dropped", "ゲーム", ""},
		{"empty", "", ""},
		{"punctuation only", "...", ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sanitizeFilenamePart(test.input); got != test.want {
				t.Fatalf("sanitizeFilenamePart(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestSanitizeFilenamePartTruncatesLongValues(t *testing.T) {
	long := ""
	for i := 0; i < 200; i++ {
		long += "a"
	}
	if got := sanitizeFilenamePart(long); len(got) != 80 {
		t.Fatalf("expected truncation to 80 characters, got %d", len(got))
	}
}

// End-to-end: the presigned URL a download redirect points at must carry the
// composed filename, not the opaque archive_default_<uuid> storage key.
func TestServeArchiveDownloadPresignsVersionedFilename(t *testing.T) {
	handler, db, storage, upload := newArchiveCacheTestHandler(t)
	defer db.Close()

	fixture := pushFixtureBuild(t, handler, db, storage, upload.ID, nil, map[string]string{"game/data.txt": "v1"})
	fixture.build.UserVersion = "1.4.2"
	if err := db.UpdateBuild(fixture.build); err != nil {
		t.Fatalf("set user version: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/download", nil)
	rec := httptest.NewRecorder()
	handler.serveArchiveDownload(rec, req, fixture.build.ID)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307, got %d: %s", rec.Code, rec.Body.String())
	}
	archive, err := handler.findBuildFile(fixture.build.ID, "archive", "default")
	if err != nil {
		t.Fatalf("archive lookup: %v", err)
	}
	if got := storage.downloadName(archive.StoragePath); got != "test-game-1.4.2.zip" {
		t.Fatalf("expected presign filename test-game-1.4.2.zip, got %q", got)
	}
}

// Downloading an older version must not inherit the newest version's name:
// the upload row that carries a filename tracks the channel head only.
func TestServeArchiveDownloadNamesOlderVersionCorrectly(t *testing.T) {
	handler, db, storage, upload := newArchiveCacheTestHandler(t)
	defer db.Close()

	first := pushFixtureBuild(t, handler, db, storage, upload.ID, nil, map[string]string{"game/data.txt": "v1"})
	first.build.UserVersion = "1.0.0"
	if err := db.UpdateBuild(first.build); err != nil {
		t.Fatalf("set first user version: %v", err)
	}
	second := pushFixtureBuild(t, handler, db, storage, upload.ID, first, map[string]string{"game/data.txt": "v2"})
	second.build.UserVersion = "2.0.0"
	if err := db.UpdateBuild(second.build); err != nil {
		t.Fatalf("set second user version: %v", err)
	}
	evictFixtureArchive(t, db, storage, first)

	req := httptest.NewRequest(http.MethodGet, "/download", nil)
	rec := httptest.NewRecorder()
	handler.serveArchiveDownload(rec, req, first.build.ID)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307, got %d: %s", rec.Code, rec.Body.String())
	}
	rebuilt, err := handler.findBuildFile(first.build.ID, "archive", "default")
	if err != nil {
		t.Fatalf("rebuilt archive lookup: %v", err)
	}
	if got := storage.downloadName(rebuilt.StoragePath); got != "test-game-1.0.0.zip" {
		t.Fatalf("expected presign filename test-game-1.0.0.zip, got %q", got)
	}
}
