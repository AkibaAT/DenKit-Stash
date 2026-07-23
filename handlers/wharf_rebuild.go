package handlers

import (
	"context"
	"denkit-stash/models"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/itchio/lake/tlc"
)

const defaultArchiveRebuildTimeout = 20 * time.Minute

// maxArchiveChainLength guards the parent walk against cyclic build rows.
const maxArchiveChainLength = 10000

func (h *WharfHandlers) SetArchiveRebuildTimeout(timeout time.Duration) {
	h.archiveRebuildTimeout = timeout
}

func (h *WharfHandlers) rebuildTimeout() time.Duration {
	if h.archiveRebuildTimeout > 0 {
		return h.archiveRebuildTimeout
	}
	return defaultArchiveRebuildTimeout
}

// ensureArchive guarantees a warm archive/default for the build and returns its
// row: it touches the access timestamp when the archive is already in object
// storage, and otherwise replays the build's patch chain under an advisory lock
// until the archive exists again. Blocks for the duration of the rebuild.
func (h *WharfHandlers) ensureArchive(ctx context.Context, buildID int64) (*models.BuildFile, error) {
	if h.storage == nil {
		return nil, fmt.Errorf("object storage client is required")
	}

	if file, warm := h.warmArchive(ctx, buildID); warm {
		return file, nil
	}

	ctx, cancel := context.WithTimeout(ctx, h.rebuildTimeout())
	defer cancel()

	lock, err := h.db.AcquireBuildArchiveLock(ctx, buildID)
	if err != nil {
		return nil, fmt.Errorf("failed to lock build %d for archive rebuild: %w", buildID, err)
	}
	defer lock.Release()

	// Another request may have rebuilt the archive while we waited on the lock.
	if file, warm := h.warmArchive(ctx, buildID); warm {
		return file, nil
	}

	return h.rebuildArchive(ctx, buildID)
}

// warmArchive reports whether the build's archive row is uploaded and its
// object actually present, touching the access timestamp when it is.
func (h *WharfHandlers) warmArchive(ctx context.Context, buildID int64) (*models.BuildFile, bool) {
	file, err := h.findBuildFileAnyState(buildID, "archive", "default")
	if err != nil || file == nil {
		return nil, false
	}
	if file.State != "uploaded" || file.StoragePath == "" {
		return nil, false
	}
	// The Head check self-heals rows whose object vanished out-of-band.
	if _, err = h.storage.Head(ctx, file.StoragePath); err != nil {
		return nil, false
	}
	if err = h.db.TouchBuildFileAccess(file.ID); err != nil {
		fmt.Printf("Warning: failed to touch archive access for build %d: %v\n", buildID, err)
	}
	return file, true
}

// findBuildFileAnyState is findBuildFile without the state filter; the archive
// cache needs to see evicted/rebuilding rows, while butler-facing lookups keep
// only seeing uploaded ones.
func (h *WharfHandlers) findBuildFileAnyState(buildID int64, fileType string, subType string) (*models.BuildFile, error) {
	files, err := h.db.GetBuildFilesByBuildID(buildID)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if file.Type == fileType && file.SubType == subType {
			return file, nil
		}
	}
	return nil, nil
}

// rebuildArchive must be called with the build's archive advisory lock held.
func (h *WharfHandlers) rebuildArchive(ctx context.Context, buildID int64) (*models.BuildFile, error) {
	build, err := h.db.GetBuildByID(buildID)
	if err != nil {
		return nil, fmt.Errorf("build %d not found: %w", buildID, err)
	}
	if build.State != "completed" {
		return nil, fmt.Errorf("cannot rebuild archive for build %d in state %q", buildID, build.State)
	}

	archiveFile, err := h.findBuildFileAnyState(buildID, "archive", "default")
	if err != nil {
		return nil, err
	}
	wasEvicted := archiveFile != nil && archiveFile.EvictedAt != nil
	if archiveFile == nil {
		archiveFile = &models.BuildFile{
			BuildID: buildID,
			Type:    "archive",
			SubType: "default",
			State:   "rebuilding",
		}
		if err = h.db.CreateBuildFile(archiveFile); err != nil {
			return nil, err
		}
	} else {
		archiveFile.State = "rebuilding"
		if err = h.db.UpdateBuildFile(archiveFile); err != nil {
			return nil, err
		}
	}

	rebuilt, err := h.replayArchiveChain(ctx, build, archiveFile)
	if err != nil {
		// Revert to the previous cache state so the next request retries.
		if wasEvicted {
			archiveFile.State = "evicted"
			if revertErr := h.db.UpdateBuildFile(archiveFile); revertErr != nil {
				fmt.Printf("Warning: failed to revert archive state for build %d: %v\n", buildID, revertErr)
			}
		}
		return nil, fmt.Errorf("failed to rebuild archive for build %d: %w", buildID, err)
	}
	return rebuilt, nil
}

func (h *WharfHandlers) replayArchiveChain(ctx context.Context, build *models.Build, archiveFile *models.BuildFile) (*models.BuildFile, error) {
	chain, baseBuild, err := h.collectRebuildChain(ctx, build)
	if err != nil {
		return nil, err
	}

	workDir, err := os.MkdirTemp("", fmt.Sprintf("denkit-rebuild-%d-*", build.ID))
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(workDir)

	currentDir := filepath.Join(workDir, "tree-base")
	if err = os.MkdirAll(currentDir, 0755); err != nil {
		return nil, err
	}

	var expectedTarget *tlc.Container
	if baseBuild != nil {
		if err = h.materializeArchivedTree(ctx, baseBuild.ID, currentDir); err != nil {
			return nil, fmt.Errorf("failed to materialize base build %d: %w", baseBuild.ID, err)
		}
		baseSignature, err := h.readParentSignature(ctx, baseBuild.ID, workDir)
		if err != nil {
			return nil, fmt.Errorf("failed to read base build %d signature: %w", baseBuild.ID, err)
		}
		expectedTarget = baseSignature.Container
	}

	var sourceContainer *tlc.Container
	for index, chainBuild := range chain {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		outputDir := filepath.Join(workDir, fmt.Sprintf("tree-%d", index))
		source, signatureContainer, err := h.applyPatchStep(ctx, chainBuild, workDir, currentDir, outputDir, expectedTarget)
		if err != nil {
			return nil, err
		}
		if err = os.RemoveAll(currentDir); err != nil {
			return nil, err
		}
		currentDir = outputDir
		expectedTarget = signatureContainer
		sourceContainer = source
	}

	storagePath, size, _, err := h.compressAndUploadArchive(ctx, build, sourceContainer, currentDir, workDir)
	if err != nil {
		return nil, err
	}

	archiveFile.State = "uploaded"
	archiveFile.StoragePath = storagePath
	archiveFile.Size = size
	archiveFile.EvictedAt = nil
	if err = h.db.UpdateBuildFile(archiveFile); err != nil {
		return nil, err
	}
	if err = h.db.TouchBuildFileAccess(archiveFile.ID); err != nil {
		fmt.Printf("Warning: failed to touch archive access for build %d: %v\n", build.ID, err)
	}
	fmt.Printf("Rebuilt archive for build %d from %d patch step(s)\n", build.ID, len(chain))
	return archiveFile, nil
}

// collectRebuildChain walks parents from build until it finds an ancestor with
// a warm archive (returned as baseBuild) or the chain root (baseBuild nil).
// The returned chain is ordered oldest-first and every link is checked for
// uploaded patch+signature rows so a doomed replay fails before any heavy work.
func (h *WharfHandlers) collectRebuildChain(ctx context.Context, build *models.Build) ([]*models.Build, *models.Build, error) {
	chain := []*models.Build{build}
	current := build
	var baseBuild *models.Build
	for current.ParentBuildID != nil {
		if len(chain) > maxArchiveChainLength {
			return nil, nil, fmt.Errorf("build %d parent chain exceeds %d links", build.ID, maxArchiveChainLength)
		}
		parent, err := h.db.GetBuildByID(*current.ParentBuildID)
		if err != nil {
			return nil, nil, fmt.Errorf("parent build %d not found: %w", *current.ParentBuildID, err)
		}
		if parentArchive, err := h.findBuildFileAnyState(parent.ID, "archive", "default"); err == nil &&
			parentArchive != nil && parentArchive.State == "uploaded" && parentArchive.StoragePath != "" {
			if _, err = h.storage.Head(ctx, parentArchive.StoragePath); err == nil {
				baseBuild = parent
				break
			}
		}
		chain = append([]*models.Build{parent}, chain...)
		current = parent
	}

	for _, chainBuild := range chain {
		if _, err := h.findBuildFile(chainBuild.ID, "patch", "default"); err != nil {
			return nil, nil, fmt.Errorf("build %d is missing patch/default; archive cannot be rebuilt: %w", chainBuild.ID, err)
		}
		if _, err := h.findBuildFile(chainBuild.ID, "signature", "default"); err != nil {
			return nil, nil, fmt.Errorf("build %d is missing signature/default; archive cannot be rebuilt: %w", chainBuild.ID, err)
		}
	}
	return chain, baseBuild, nil
}

// materializeArchivedTree extracts an already-warm archive into targetDir
// without recursing into ensureArchive (callers verified the object exists).
func (h *WharfHandlers) materializeArchivedTree(ctx context.Context, buildID int64, targetDir string) error {
	archiveFile, err := h.findBuildFileAnyState(buildID, "archive", "default")
	if err != nil {
		return err
	}
	if archiveFile == nil || archiveFile.State != "uploaded" || archiveFile.StoragePath == "" {
		return fmt.Errorf("build %d has no warm archive to materialize", buildID)
	}
	archivePath := filepath.Join(targetDir, ".parent."+archiveFormatFromPath(archiveFile.StoragePath))
	if err = h.downloadObject(ctx, archiveFile.StoragePath, archivePath); err != nil {
		return err
	}
	if err = extractArchive(archivePath, targetDir); err != nil {
		return err
	}
	return os.Remove(archivePath)
}
