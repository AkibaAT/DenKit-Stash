package handlers

import (
	"context"
	"denkit-stash/models"
	"log"
	"math/rand"
	"time"
)

// ArchiveGCConfig tunes the archive cache eviction worker. Eviction is opt-in:
// with Enabled false (the default) nothing is ever deleted; rebuild-on-demand
// stays active either way.
type ArchiveGCConfig struct {
	Enabled    bool
	TTL        time.Duration
	Interval   time.Duration
	BatchLimit int
}

func DefaultArchiveGCConfig() ArchiveGCConfig {
	return ArchiveGCConfig{
		Enabled:    false,
		TTL:        720 * time.Hour,
		Interval:   time.Hour,
		BatchLimit: 100,
	}
}

type archiveGCStats struct {
	evicted      int
	bytesFreed   int64
	skippedLock  int
	skippedGuard int
	sweptObjects int
	errors       int
}

func (h *WharfHandlers) StartArchiveGC(ctx context.Context, cfg ArchiveGCConfig) {
	if !cfg.Enabled {
		log.Printf("archive gc disabled; archives are never evicted")
		return
	}
	if h.storage == nil {
		log.Printf("archive gc disabled: no object storage configured")
		return
	}
	go func() {
		// Jitter the first pass so multiple replicas don't sweep in lockstep;
		// advisory locks make overlap safe, this just spreads the load.
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(rand.Int63n(int64(cfg.Interval)))):
		}
		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()
		for {
			stats := h.runArchiveGCOnce(ctx, cfg)
			log.Printf("archive-gc: evicted=%d bytes_freed=%d swept=%d skipped_lock=%d skipped_guard=%d errors=%d",
				stats.evicted, stats.bytesFreed, stats.sweptObjects, stats.skippedLock, stats.skippedGuard, stats.errors)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (h *WharfHandlers) runArchiveGCOnce(ctx context.Context, cfg ArchiveGCConfig) archiveGCStats {
	stats := archiveGCStats{}

	candidates, err := h.db.ListEvictableArchiveFiles(time.Now().Add(-cfg.TTL), cfg.BatchLimit)
	if err != nil {
		log.Printf("archive-gc: failed to list eviction candidates: %v", err)
		stats.errors++
		return stats
	}

	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return stats
		}
		h.evictArchive(ctx, cfg, candidate, &stats)
	}

	h.sweepEvictedObjects(ctx, &stats)
	return stats
}

func (h *WharfHandlers) evictArchive(ctx context.Context, cfg ArchiveGCConfig, candidate *models.BuildFile, stats *archiveGCStats) {
	lock, acquired, err := h.db.TryAcquireBuildArchiveLock(ctx, candidate.BuildID)
	if err != nil {
		log.Printf("archive-gc: failed to lock build %d: %v", candidate.BuildID, err)
		stats.errors++
		return
	}
	if !acquired {
		stats.skippedLock++
		return
	}
	defer lock.Release()

	// Re-verify everything under the lock; state may have changed since the
	// candidate list was built.
	file, err := h.db.GetBuildFileByID(candidate.ID)
	if err != nil {
		stats.errors++
		return
	}
	if file.State != "uploaded" || file.StoragePath == "" {
		stats.skippedGuard++
		return
	}
	if file.LastAccessedAt.After(time.Now().Add(-cfg.TTL)) {
		stats.skippedGuard++
		return
	}
	isHead, err := h.db.IsChannelHead(file.BuildID)
	if err != nil || isHead {
		stats.skippedGuard++
		return
	}
	build, err := h.db.GetBuildByID(file.BuildID)
	if err != nil || build.State != "completed" {
		stats.skippedGuard++
		return
	}

	// Never evict an archive whose patch chain link is not durably stored:
	// patch + signature rows must be uploaded and their objects present.
	for _, required := range []string{"patch", "signature"} {
		requiredFile, err := h.findBuildFile(file.BuildID, required, "default")
		if err != nil || requiredFile.StoragePath == "" {
			log.Printf("archive-gc: refusing to evict build %d: missing %s/default row", file.BuildID, required)
			stats.skippedGuard++
			return
		}
		if _, err = h.storage.Head(ctx, requiredFile.StoragePath); err != nil {
			log.Printf("archive-gc: refusing to evict build %d: %s object missing in storage: %v", file.BuildID, required, err)
			stats.skippedGuard++
			return
		}
	}

	// Mark first, delete second: a crash in between leaves an evicted row with
	// a still-present object, which the sweep below cleans up later.
	now := time.Now()
	file.State = "evicted"
	file.EvictedAt = &now
	if err = h.db.UpdateBuildFile(file); err != nil {
		stats.errors++
		return
	}

	if err = h.storage.Delete(ctx, file.StoragePath); err != nil {
		log.Printf("archive-gc: failed to delete object for build %d (will retry): %v", file.BuildID, err)
		stats.errors++
		return
	}
	stats.evicted++
	stats.bytesFreed += file.Size

	file.StoragePath = ""
	if err = h.db.UpdateBuildFile(file); err != nil {
		stats.errors++
	}
}

// sweepEvictedObjects retries deletes that failed after a row was already
// marked evicted (or that a crash interrupted).
func (h *WharfHandlers) sweepEvictedObjects(ctx context.Context, stats *archiveGCStats) {
	leftovers, err := h.db.ListEvictedArchiveFilesWithStorage(1000)
	if err != nil {
		log.Printf("archive-gc: failed to list evicted leftovers: %v", err)
		stats.errors++
		return
	}
	for _, file := range leftovers {
		if ctx.Err() != nil {
			return
		}
		lock, acquired, err := h.db.TryAcquireBuildArchiveLock(ctx, file.BuildID)
		if err != nil || !acquired {
			continue
		}
		current, err := h.db.GetBuildFileByID(file.ID)
		if err == nil && current.State == "evicted" && current.StoragePath != "" {
			if err = h.storage.Delete(ctx, current.StoragePath); err == nil {
				current.StoragePath = ""
				if err = h.db.UpdateBuildFile(current); err == nil {
					stats.sweptObjects++
				}
			}
		}
		lock.Release()
	}
}
