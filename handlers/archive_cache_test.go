package handlers

import (
	archivezip "archive/zip"
	"bytes"
	"context"
	"denkit-stash/models"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/itchio/headway/state"
	"github.com/itchio/lake/pools/fspool"
	"github.com/itchio/lake/tlc"
	"github.com/itchio/wharf/pwr"
)

// fixtureBuild is a build pushed through the real archive pipeline, with its
// source tree kept on disk so children can diff against it.
type fixtureBuild struct {
	build       *models.Build
	archiveFile *models.BuildFile
	sourceDir   string
	files       map[string]string
}

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

// makeWharfFixture produces real patch+signature bytes transforming
// parentDir (empty string means the empty container) into sourceDir.
func makeWharfFixture(t *testing.T, parentDir string, sourceDir string) ([]byte, []byte) {
	t.Helper()
	ctx := context.Background()
	consumer := &state.Consumer{}

	targetContainer := &tlc.Container{}
	var targetPool = fspool.New(targetContainer, t.TempDir())
	if parentDir != "" {
		var err error
		targetContainer, err = tlc.WalkDir(parentDir, tlc.WalkOpts{})
		if err != nil {
			t.Fatalf("walk parent dir: %v", err)
		}
		targetPool = fspool.New(targetContainer, parentDir)
	}
	targetSignature, err := pwr.ComputeSignature(ctx, targetContainer, targetPool, consumer)
	if err != nil {
		t.Fatalf("compute target signature: %v", err)
	}

	sourceContainer, err := tlc.WalkDir(sourceDir, tlc.WalkOpts{})
	if err != nil {
		t.Fatalf("walk source dir: %v", err)
	}

	diff := &pwr.DiffContext{
		Compression:     &pwr.CompressionSettings{Algorithm: pwr.CompressionAlgorithm_NONE},
		Consumer:        consumer,
		SourceContainer: sourceContainer,
		Pool:            fspool.New(sourceContainer, sourceDir),
		TargetContainer: targetContainer,
		TargetSignature: targetSignature,
	}

	var patchBuf, signatureBuf bytes.Buffer
	if err = diff.WritePatch(ctx, &patchBuf, &signatureBuf); err != nil {
		t.Fatalf("write patch: %v", err)
	}
	return patchBuf.Bytes(), signatureBuf.Bytes()
}

// pushFixtureBuild drives the real push pipeline: patch+signature land in the
// fake storage, checkAndUpdateBuildState generates the archive and advances
// the channel head.
func pushFixtureBuild(t *testing.T, handler *WharfHandlers, db models.Database, storage *memStorage, uploadID int64, parent *fixtureBuild, files map[string]string) *fixtureBuild {
	t.Helper()
	sourceDir := t.TempDir()
	writeTree(t, sourceDir, files)

	parentDir := ""
	var parentID *int64
	if parent != nil {
		parentDir = parent.sourceDir
		parentID = &parent.build.ID
	}
	patch, signature := makeWharfFixture(t, parentDir, sourceDir)

	build := createBuild(t, db, uploadID, parentID)
	for _, artifact := range []struct {
		fileType string
		data     []byte
	}{{"patch", patch}, {"signature", signature}} {
		storagePath := fmt.Sprintf("builds/%d/%s_default_test", build.ID, artifact.fileType)
		if err := storage.Put(context.Background(), storagePath, bytes.NewReader(artifact.data), int64(len(artifact.data)), "application/octet-stream"); err != nil {
			t.Fatalf("store %s: %v", artifact.fileType, err)
		}
		file := &models.BuildFile{
			BuildID:     build.ID,
			Type:        artifact.fileType,
			SubType:     "default",
			State:       "uploaded",
			Size:        int64(len(artifact.data)),
			StoragePath: storagePath,
		}
		if err := db.CreateBuildFile(file); err != nil {
			t.Fatalf("create %s build file: %v", artifact.fileType, err)
		}
	}

	if err := handler.checkAndUpdateBuildState(build.ID); err != nil {
		t.Fatalf("complete build: %v", err)
	}

	completed, err := db.GetBuildByID(build.ID)
	if err != nil {
		t.Fatalf("reload build: %v", err)
	}
	if completed.State != "completed" {
		t.Fatalf("expected build %d completed, got %q", build.ID, completed.State)
	}
	archiveFile, err := handler.findBuildFile(build.ID, "archive", "default")
	if err != nil {
		t.Fatalf("find archive build file: %v", err)
	}
	if _, ok := storage.object(archiveFile.StoragePath); !ok {
		t.Fatalf("archive object %s missing from storage", archiveFile.StoragePath)
	}
	return &fixtureBuild{build: completed, archiveFile: archiveFile, sourceDir: sourceDir, files: files}
}

func newArchiveCacheTestHandler(t *testing.T) (*WharfHandlers, models.Database, *memStorage, *models.Upload) {
	t.Helper()
	handler, db, upload, _ := newTestWharfHandler(t)
	storage := newMemStorage()
	handler.storage = storage
	return handler, db, storage, upload
}

// evictFixtureArchive simulates a completed GC eviction: row marked, object gone.
func evictFixtureArchive(t *testing.T, db models.Database, storage *memStorage, fixture *fixtureBuild) {
	t.Helper()
	now := time.Now()
	fixture.archiveFile.State = "evicted"
	fixture.archiveFile.EvictedAt = &now
	if _, ok := storage.object(fixture.archiveFile.StoragePath); ok {
		if err := storage.Delete(context.Background(), fixture.archiveFile.StoragePath); err != nil {
			t.Fatalf("delete archive object: %v", err)
		}
	}
	fixture.archiveFile.StoragePath = ""
	if err := db.UpdateBuildFile(fixture.archiveFile); err != nil {
		t.Fatalf("mark archive evicted: %v", err)
	}
}

func assertArchiveContents(t *testing.T, storage *memStorage, storagePath string, files map[string]string) {
	t.Helper()
	data, ok := storage.object(storagePath)
	if !ok {
		t.Fatalf("archive object %s missing from storage", storagePath)
	}
	reader, err := archivezip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open rebuilt archive: %v", err)
	}
	found := map[string]string{}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		handle, err := file.Open()
		if err != nil {
			t.Fatalf("open archive entry %s: %v", file.Name, err)
		}
		content, err := io.ReadAll(handle)
		handle.Close()
		if err != nil {
			t.Fatalf("read archive entry %s: %v", file.Name, err)
		}
		found[file.Name] = string(content)
	}
	if len(found) != len(files) {
		t.Fatalf("archive has %d files, expected %d: %#v", len(found), len(files), found)
	}
	for name, content := range files {
		if found[name] != content {
			t.Fatalf("archive entry %s = %q, expected %q", name, found[name], content)
		}
	}
}

func TestEnsureArchiveWarmTouchesAccessWithoutUploading(t *testing.T) {
	handler, db, storage, upload := newArchiveCacheTestHandler(t)
	defer db.Close()

	fixture := pushFixtureBuild(t, handler, db, storage, upload.ID, nil, map[string]string{"game/data.txt": "v1"})
	archivePuts := storage.putCountWithPrefix(fmt.Sprintf("builds/%d/archive_default_", fixture.build.ID))

	time.Sleep(20 * time.Millisecond)
	file, err := handler.ensureArchive(context.Background(), fixture.build.ID)
	if err != nil {
		t.Fatalf("ensure warm archive: %v", err)
	}
	if file.StoragePath != fixture.archiveFile.StoragePath {
		t.Fatalf("warm archive path changed: %s -> %s", fixture.archiveFile.StoragePath, file.StoragePath)
	}
	if got := storage.putCountWithPrefix(fmt.Sprintf("builds/%d/archive_default_", fixture.build.ID)); got != archivePuts {
		t.Fatalf("warm ensureArchive uploaded a new archive (%d -> %d puts)", archivePuts, got)
	}

	touched, err := db.GetBuildFileByID(file.ID)
	if err != nil {
		t.Fatalf("reload archive file: %v", err)
	}
	if !touched.LastAccessedAt.After(fixture.archiveFile.LastAccessedAt) {
		t.Fatalf("last_accessed_at was not bumped: %v -> %v", fixture.archiveFile.LastAccessedAt, touched.LastAccessedAt)
	}
}

func TestEnsureArchiveRebuildsSingleStep(t *testing.T) {
	handler, db, storage, upload := newArchiveCacheTestHandler(t)
	defer db.Close()

	first := pushFixtureBuild(t, handler, db, storage, upload.ID, nil, map[string]string{"game/data.txt": "v1"})
	second := pushFixtureBuild(t, handler, db, storage, upload.ID, first, map[string]string{"game/data.txt": "v2", "game/new.txt": "hello"})

	evictFixtureArchive(t, db, storage, first)

	file, err := handler.ensureArchive(context.Background(), first.build.ID)
	if err != nil {
		t.Fatalf("rebuild first build archive: %v", err)
	}
	if file.State != "uploaded" || file.StoragePath == "" || file.EvictedAt != nil {
		t.Fatalf("rebuilt archive row is not warm: %#v", file)
	}
	assertArchiveContents(t, storage, file.StoragePath, first.files)

	// The head build's archive must be untouched.
	headArchive, err := handler.findBuildFile(second.build.ID, "archive", "default")
	if err != nil {
		t.Fatalf("head archive lookup: %v", err)
	}
	if headArchive.StoragePath != second.archiveFile.StoragePath {
		t.Fatalf("head archive changed during rebuild")
	}
}

func TestEnsureArchiveRebuildsChainFromEmptyRoot(t *testing.T) {
	handler, db, storage, upload := newArchiveCacheTestHandler(t)
	defer db.Close()

	first := pushFixtureBuild(t, handler, db, storage, upload.ID, nil, map[string]string{"game/data.txt": "v1"})
	second := pushFixtureBuild(t, handler, db, storage, upload.ID, first, map[string]string{"game/data.txt": "v2", "game/new.txt": "hello"})
	third := pushFixtureBuild(t, handler, db, storage, upload.ID, second, map[string]string{"game/data.txt": "v3"})
	_ = third

	// Evict everything but the head: the second build's rebuild must replay
	// the whole chain starting from the empty container.
	evictFixtureArchive(t, db, storage, first)
	evictFixtureArchive(t, db, storage, second)

	file, err := handler.ensureArchive(context.Background(), second.build.ID)
	if err != nil {
		t.Fatalf("rebuild second build archive: %v", err)
	}
	assertArchiveContents(t, storage, file.StoragePath, second.files)

	// The first build stays evicted; only the requested build is rebuilt.
	firstArchive, err := handler.findBuildFileAnyState(first.build.ID, "archive", "default")
	if err != nil {
		t.Fatalf("first archive lookup: %v", err)
	}
	if firstArchive.State != "evicted" {
		t.Fatalf("expected first build archive to stay evicted, got %q", firstArchive.State)
	}
}

func TestEnsureArchiveFailsWhenPatchMissing(t *testing.T) {
	handler, db, storage, upload := newArchiveCacheTestHandler(t)
	defer db.Close()

	first := pushFixtureBuild(t, handler, db, storage, upload.ID, nil, map[string]string{"game/data.txt": "v1"})
	pushFixtureBuild(t, handler, db, storage, upload.ID, first, map[string]string{"game/data.txt": "v2"})
	evictFixtureArchive(t, db, storage, first)

	patchFile, err := handler.findBuildFile(first.build.ID, "patch", "default")
	if err != nil {
		t.Fatalf("patch lookup: %v", err)
	}
	if err = storage.Delete(context.Background(), patchFile.StoragePath); err != nil {
		t.Fatalf("delete patch object: %v", err)
	}

	if _, err = handler.ensureArchive(context.Background(), first.build.ID); err == nil {
		t.Fatalf("expected rebuild to fail with missing patch object")
	}
	reverted, err := handler.findBuildFileAnyState(first.build.ID, "archive", "default")
	if err != nil {
		t.Fatalf("archive lookup: %v", err)
	}
	if reverted.State != "evicted" {
		t.Fatalf("expected failed rebuild to revert to evicted, got %q", reverted.State)
	}
}

func TestEnsureArchiveConcurrentRebuildUploadsOnce(t *testing.T) {
	handler, db, storage, upload := newArchiveCacheTestHandler(t)
	defer db.Close()

	first := pushFixtureBuild(t, handler, db, storage, upload.ID, nil, map[string]string{"game/data.txt": "v1"})
	pushFixtureBuild(t, handler, db, storage, upload.ID, first, map[string]string{"game/data.txt": "v2"})
	evictFixtureArchive(t, db, storage, first)
	before := storage.putCountWithPrefix(fmt.Sprintf("builds/%d/archive_default_", first.build.ID))

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			_, errs[slot] = handler.ensureArchive(context.Background(), first.build.ID)
		}(i)
	}
	wg.Wait()
	for slot, err := range errs {
		if err != nil {
			t.Fatalf("concurrent ensureArchive %d failed: %v", slot, err)
		}
	}
	after := storage.putCountWithPrefix(fmt.Sprintf("builds/%d/archive_default_", first.build.ID))
	if after-before != 1 {
		t.Fatalf("expected exactly one archive upload from concurrent rebuilds, got %d", after-before)
	}
}

func TestServeArchiveDownloadRebuildsAndRedirects(t *testing.T) {
	handler, db, storage, upload := newArchiveCacheTestHandler(t)
	defer db.Close()

	first := pushFixtureBuild(t, handler, db, storage, upload.ID, nil, map[string]string{"game/data.txt": "v1"})
	pushFixtureBuild(t, handler, db, storage, upload.ID, first, map[string]string{"game/data.txt": "v2"})
	evictFixtureArchive(t, db, storage, first)

	req := httptest.NewRequest(http.MethodGet, "/download", nil)
	rec := httptest.NewRecorder()
	handler.serveArchiveDownload(rec, req, first.build.ID)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307, got %d: %s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	rebuilt, err := handler.findBuildFile(first.build.ID, "archive", "default")
	if err != nil {
		t.Fatalf("rebuilt archive lookup: %v", err)
	}
	if location != "https://fake.storage/"+rebuilt.StoragePath {
		t.Fatalf("unexpected redirect location: %s", location)
	}
}

func TestArchiveGCEvictsStaleNonHeadOnly(t *testing.T) {
	handler, db, storage, upload := newArchiveCacheTestHandler(t)
	defer db.Close()

	first := pushFixtureBuild(t, handler, db, storage, upload.ID, nil, map[string]string{"game/data.txt": "v1"})
	second := pushFixtureBuild(t, handler, db, storage, upload.ID, first, map[string]string{"game/data.txt": "v2"})

	cfg := DefaultArchiveGCConfig()
	cfg.Enabled = true
	cfg.TTL = time.Nanosecond
	stats := handler.runArchiveGCOnce(context.Background(), cfg)

	if stats.evicted != 1 {
		t.Fatalf("expected exactly one eviction, got %+v", stats)
	}
	evicted, err := handler.findBuildFileAnyState(first.build.ID, "archive", "default")
	if err != nil {
		t.Fatalf("evicted archive lookup: %v", err)
	}
	if evicted.State != "evicted" || evicted.StoragePath != "" || evicted.EvictedAt == nil {
		t.Fatalf("first archive not fully evicted: %#v", evicted)
	}
	if _, ok := storage.object(first.archiveFile.StoragePath); ok {
		t.Fatalf("evicted archive object still in storage")
	}
	// Head archive survives regardless of staleness.
	if _, ok := storage.object(second.archiveFile.StoragePath); !ok {
		t.Fatalf("head archive object was deleted")
	}
	// Patch and signature objects are never touched.
	for _, buildID := range []int64{first.build.ID, second.build.ID} {
		for _, fileType := range []string{"patch", "signature"} {
			file, err := handler.findBuildFile(buildID, fileType, "default")
			if err != nil {
				t.Fatalf("%s lookup: %v", fileType, err)
			}
			if _, ok := storage.object(file.StoragePath); !ok {
				t.Fatalf("%s object for build %d was deleted", fileType, buildID)
			}
		}
	}
}

func TestArchiveGCSkipsRecentlyAccessed(t *testing.T) {
	handler, db, storage, upload := newArchiveCacheTestHandler(t)
	defer db.Close()

	first := pushFixtureBuild(t, handler, db, storage, upload.ID, nil, map[string]string{"game/data.txt": "v1"})
	pushFixtureBuild(t, handler, db, storage, upload.ID, first, map[string]string{"game/data.txt": "v2"})

	cfg := DefaultArchiveGCConfig()
	cfg.Enabled = true
	cfg.TTL = time.Hour
	stats := handler.runArchiveGCOnce(context.Background(), cfg)
	if stats.evicted != 0 {
		t.Fatalf("expected no evictions for fresh archives, got %+v", stats)
	}
	if _, ok := storage.object(first.archiveFile.StoragePath); !ok {
		t.Fatalf("fresh archive object was deleted")
	}
}

func TestArchiveGCRefusesWhenSignatureObjectMissing(t *testing.T) {
	handler, db, storage, upload := newArchiveCacheTestHandler(t)
	defer db.Close()

	first := pushFixtureBuild(t, handler, db, storage, upload.ID, nil, map[string]string{"game/data.txt": "v1"})
	pushFixtureBuild(t, handler, db, storage, upload.ID, first, map[string]string{"game/data.txt": "v2"})

	signatureFile, err := handler.findBuildFile(first.build.ID, "signature", "default")
	if err != nil {
		t.Fatalf("signature lookup: %v", err)
	}
	if err = storage.Delete(context.Background(), signatureFile.StoragePath); err != nil {
		t.Fatalf("delete signature object: %v", err)
	}

	cfg := DefaultArchiveGCConfig()
	cfg.Enabled = true
	cfg.TTL = time.Nanosecond
	stats := handler.runArchiveGCOnce(context.Background(), cfg)
	if stats.evicted != 0 || stats.skippedGuard == 0 {
		t.Fatalf("expected guard skip when signature object missing, got %+v", stats)
	}
	untouched, err := handler.findBuildFile(first.build.ID, "archive", "default")
	if err != nil {
		t.Fatalf("archive lookup: %v", err)
	}
	if untouched.State != "uploaded" {
		t.Fatalf("archive without durable signature was evicted: %#v", untouched)
	}
}

func TestArchiveGCSkipsLockedBuilds(t *testing.T) {
	handler, db, storage, upload := newArchiveCacheTestHandler(t)
	defer db.Close()

	first := pushFixtureBuild(t, handler, db, storage, upload.ID, nil, map[string]string{"game/data.txt": "v1"})
	pushFixtureBuild(t, handler, db, storage, upload.ID, first, map[string]string{"game/data.txt": "v2"})

	lock, err := db.AcquireBuildArchiveLock(context.Background(), first.build.ID)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer lock.Release()

	cfg := DefaultArchiveGCConfig()
	cfg.Enabled = true
	cfg.TTL = time.Nanosecond
	stats := handler.runArchiveGCOnce(context.Background(), cfg)
	if stats.evicted != 0 || stats.skippedLock != 1 {
		t.Fatalf("expected locked build to be skipped, got %+v", stats)
	}
}

func TestArchiveGCSweepsLeftoverObjects(t *testing.T) {
	handler, db, storage, upload := newArchiveCacheTestHandler(t)
	defer db.Close()

	first := pushFixtureBuild(t, handler, db, storage, upload.ID, nil, map[string]string{"game/data.txt": "v1"})
	pushFixtureBuild(t, handler, db, storage, upload.ID, first, map[string]string{"game/data.txt": "v2"})

	// Simulate a crash between marking evicted and deleting the object.
	now := time.Now()
	first.archiveFile.State = "evicted"
	first.archiveFile.EvictedAt = &now
	if err := db.UpdateBuildFile(first.archiveFile); err != nil {
		t.Fatalf("mark evicted: %v", err)
	}

	cfg := DefaultArchiveGCConfig()
	cfg.Enabled = true
	cfg.TTL = time.Hour
	stats := handler.runArchiveGCOnce(context.Background(), cfg)
	if stats.sweptObjects != 1 {
		t.Fatalf("expected one swept object, got %+v", stats)
	}
	if _, ok := storage.object(first.archiveFile.StoragePath); ok {
		t.Fatalf("leftover archive object still in storage")
	}
	swept, err := handler.findBuildFileAnyState(first.build.ID, "archive", "default")
	if err != nil {
		t.Fatalf("archive lookup: %v", err)
	}
	if swept.StoragePath != "" {
		t.Fatalf("swept archive row still has storage path %q", swept.StoragePath)
	}
}

func TestGetBuildFilesListingShowsEvictedState(t *testing.T) {
	handler, db, storage, upload := newArchiveCacheTestHandler(t)
	defer db.Close()

	first := pushFixtureBuild(t, handler, db, storage, upload.ID, nil, map[string]string{"game/data.txt": "v1"})
	pushFixtureBuild(t, handler, db, storage, upload.ID, first, map[string]string{"game/data.txt": "v2"})
	evictFixtureArchive(t, db, storage, first)

	// findBuildFile (butler-facing, uploaded-only) must not see the evicted
	// archive, while findBuildFileAnyState must.
	if _, err := handler.findBuildFile(first.build.ID, "archive", "default"); err == nil {
		t.Fatalf("expected uploaded-only lookup to miss evicted archive")
	}
	file, err := handler.findBuildFileAnyState(first.build.ID, "archive", "default")
	if err != nil || file == nil {
		t.Fatalf("any-state lookup failed: %v", err)
	}
	if file.State != "evicted" {
		t.Fatalf("expected evicted state in listing, got %q", file.State)
	}
}
