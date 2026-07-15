package handlers

import (
	archivezip "archive/zip"
	"context"
	"denkit-stash/models"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/itchio/headway/state"
	"github.com/itchio/lake/pools/fspool"
	"github.com/itchio/lake/tlc"
	"github.com/itchio/savior/seeksource"
	"github.com/itchio/wharf/archiver/containerarchiver"
	_ "github.com/itchio/wharf/decompressors/cbrotli"
	_ "github.com/itchio/wharf/decompressors/gzip"
	"github.com/itchio/wharf/pwr"
	"github.com/itchio/wharf/pwr/bowl"
	"github.com/itchio/wharf/pwr/patcher"
)

func (h *WharfHandlers) checkAndUpdateBuildState(buildID int64) error {
	build, err := h.db.GetBuildByID(buildID)
	if err != nil {
		return fmt.Errorf("failed to get build: %w", err)
	}

	if build.State != "started" && build.State != "processing" {
		return nil
	}

	buildFiles, err := h.db.GetBuildFilesByBuildID(buildID)
	if err != nil {
		return fmt.Errorf("failed to get build files: %w", err)
	}

	filesByKind := map[string]*models.BuildFile{}
	for _, file := range buildFiles {
		if file.State == "failed" {
			build.State = "failed"
			return h.db.UpdateBuild(build)
		}
		if file.State == "uploaded" {
			filesByKind[file.Type+"/"+file.SubType] = file
		}
	}

	hasPatch := h.hasReadyRequiredFile(filesByKind, "patch/default")
	hasSignature := h.hasReadyRequiredFile(filesByKind, "signature/default")
	hasArchive := h.hasReadyRequiredFile(filesByKind, "archive/default")

	if hasPatch && hasSignature && !hasArchive && build.State == "started" {
		build.State = "processing"
		if err = h.db.UpdateBuild(build); err != nil {
			return err
		}
		if err = h.generateArchiveDefault(build, filesByKind["patch/default"], filesByKind["signature/default"]); err != nil {
			return fmt.Errorf("failed to generate archive/default: %w", err)
		}
		return h.checkAndUpdateBuildState(buildID)
	}
	if !(hasPatch && hasSignature && hasArchive) {
		return nil
	}

	if err = h.updateUploadSizeFromArchive(build, filesByKind["archive/default"]); err != nil {
		return fmt.Errorf("failed to update upload size: %w", err)
	}

	build.State = "completed"
	if err = h.db.UpdateBuild(build); err != nil {
		return fmt.Errorf("failed to update build state to completed: %w", err)
	}
	return h.advanceCompletedChannelHead(build)
}

func (h *WharfHandlers) generateArchiveDefault(build *models.Build, patchFile *models.BuildFile, signatureFile *models.BuildFile) error {
	if h.storageClient == nil {
		return fmt.Errorf("object storage client is required")
	}

	ctx := context.Background()
	workDir, err := os.MkdirTemp("", fmt.Sprintf("denkit-build-%d-*", build.ID))
	if err != nil {
		return err
	}
	defer os.RemoveAll(workDir)

	patchPath := filepath.Join(workDir, "patch.pwr")
	if err = h.downloadObject(ctx, patchFile.StoragePath, patchPath); err != nil {
		return err
	}
	signaturePath := filepath.Join(workDir, "signature.pws")
	if err = h.downloadObject(ctx, signatureFile.StoragePath, signaturePath); err != nil {
		return err
	}

	signature, err := h.readSignature(ctx, signaturePath)
	if err != nil {
		return err
	}

	targetDir := filepath.Join(workDir, "target")
	outputDir := filepath.Join(workDir, "output")
	if err = os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}
	if build.ParentBuildID != nil {
		if err = h.restoreParentArchive(ctx, *build.ParentBuildID, targetDir); err != nil {
			return err
		}
	}

	patchHandle, err := os.Open(patchPath)
	if err != nil {
		return err
	}
	defer patchHandle.Close()

	consumer := &state.Consumer{}
	pat, err := patcher.New(seeksource.FromFile(patchHandle), consumer)
	if err != nil {
		return err
	}
	var parentSignature *pwr.SignatureInfo
	if build.ParentBuildID != nil {
		parentSignature, err = h.readParentSignature(ctx, *build.ParentBuildID, workDir)
		if err != nil {
			return fmt.Errorf("failed to read parent signature: %w", err)
		}
	}
	if err = validatePatchTargetContainer(build, pat.GetTargetContainer(), parentSignature); err != nil {
		return err
	}

	targetPool := fspool.New(pat.GetTargetContainer(), targetDir)
	freshBowl, err := bowl.NewFreshBowl(bowl.FreshBowlParams{
		TargetContainer: pat.GetTargetContainer(),
		TargetPool:      targetPool,
		SourceContainer: pat.GetSourceContainer(),
		OutputFolder:    outputDir,
	})
	if err != nil {
		return err
	}
	if err = pat.Resume(nil, targetPool, freshBowl); err != nil {
		return err
	}
	if err = freshBowl.Commit(); err != nil {
		return err
	}

	sourceContainer := pat.GetSourceContainer()
	if err = sourceContainer.EnsureEqual(signature.Container); err != nil {
		return fmt.Errorf("patch source tree does not match signature: %w", err)
	}

	metadata := readArchiveOptimizationMetadata(outputDir)
	archiveFormat := archiveFormatFromMetadata(metadata)
	archivePath := filepath.Join(workDir, "archive."+archiveFormat)
	if archiveFormat == "zip" {
		archiveHandle, err := os.Create(archivePath)
		if err != nil {
			return err
		}
		outputPool := fspool.New(sourceContainer, outputDir)
		_, compressErr := containerarchiver.CompressZip(archiveHandle, sourceContainer, outputPool, consumer)
		closeErr := archiveHandle.Close()
		if compressErr != nil {
			return compressErr
		}
		if closeErr != nil {
			return closeErr
		}
	} else if err = createArchiveFromDirectory(outputDir, archivePath, archiveFormat); err != nil {
		return err
	}

	archiveInfo, err := os.Stat(archivePath)
	if err != nil {
		return err
	}
	archiveStoragePath := fmt.Sprintf("builds/%d/archive_default_%s.%s", build.ID, uuid.New().String(), archiveFormat)
	if err = h.uploadObject(ctx, archivePath, archiveStoragePath, archiveInfo.Size(), contentTypeForArchiveFormat(archiveFormat)); err != nil {
		return err
	}
	if err = h.updateUploadFromArchiveMetadata(build, metadata, archiveInfo.Size()); err != nil {
		return err
	}

	archiveFile := &models.BuildFile{
		BuildID:     build.ID,
		Type:        "archive",
		SubType:     "default",
		State:       "uploaded",
		Size:        archiveInfo.Size(),
		StoragePath: archiveStoragePath,
	}
	return h.db.CreateBuildFile(archiveFile)
}

func (h *WharfHandlers) downloadObject(ctx context.Context, objectName string, destPath string) error {
	object, err := h.storageClient.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(h.bucketName),
		Key:    aws.String(objectName),
	})
	if err != nil {
		return err
	}
	defer object.Body.Close()

	dest, err := os.Create(destPath)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dest, object.Body)
	closeErr := dest.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func (h *WharfHandlers) uploadObject(ctx context.Context, sourcePath string, objectName string, size int64, contentType string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	_, err = h.storageClient.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(h.bucketName),
		Key:           aws.String(objectName),
		Body:          source,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(contentType),
	})
	return err
}

func (h *WharfHandlers) readSignature(ctx context.Context, signaturePath string) (*pwr.SignatureInfo, error) {
	signatureHandle, err := os.Open(signaturePath)
	if err != nil {
		return nil, err
	}
	defer signatureHandle.Close()
	signatureSource := seeksource.FromFile(signatureHandle)
	if _, err = signatureSource.Resume(nil); err != nil {
		return nil, err
	}
	return pwr.ReadSignature(ctx, signatureSource)
}

func (h *WharfHandlers) readParentSignature(ctx context.Context, parentBuildID int64, workDir string) (*pwr.SignatureInfo, error) {
	parentSignature, err := h.findBuildFile(parentBuildID, "signature", "default")
	if err != nil {
		return nil, err
	}
	signaturePath := filepath.Join(workDir, fmt.Sprintf("parent-%d-signature.pws", parentBuildID))
	if err = h.downloadObject(ctx, parentSignature.StoragePath, signaturePath); err != nil {
		return nil, err
	}
	return h.readSignature(ctx, signaturePath)
}

func validatePatchTargetContainer(build *models.Build, patchTarget *tlc.Container, parentSignature *pwr.SignatureInfo) error {
	if patchTarget == nil {
		return fmt.Errorf("patch target tree is missing")
	}
	if build.ParentBuildID == nil {
		if err := patchTarget.EnsureEqual(&tlc.Container{}); err != nil {
			return fmt.Errorf("initial build patch target is not empty: %w", err)
		}
		return nil
	}
	if parentSignature == nil || parentSignature.Container == nil {
		return fmt.Errorf("parent build signature is required")
	}
	if err := patchTarget.EnsureEqual(parentSignature.Container); err != nil {
		return fmt.Errorf("patch target tree does not match parent signature: %w", err)
	}
	return nil
}

type archiveOptimizationMetadata struct {
	Schema          string `json:"schema"`
	OriginalArchive struct {
		Filename string `json:"filename"`
		Format   string `json:"format"`
	} `json:"original_archive"`
}

const maxArchiveOptimizationMetadataBytes = 64 * 1024

func readArchiveOptimizationMetadata(extractPath string) *archiveOptimizationMetadata {
	metadataPath := filepath.Join(extractPath, ".fvn-archive-metadata.json")
	info, err := os.Stat(metadataPath)
	if err != nil {
		return nil
	}
	if !info.Mode().IsRegular() || info.Size() > maxArchiveOptimizationMetadataBytes {
		return nil
	}

	file, err := os.Open(metadataPath)
	if err != nil {
		return nil
	}
	defer file.Close()

	var metadata archiveOptimizationMetadata
	if err = json.NewDecoder(io.LimitReader(file, maxArchiveOptimizationMetadataBytes)).Decode(&metadata); err != nil {
		return nil
	}
	if metadata.Schema != "fvn.archive_optimization.v1" {
		return nil
	}

	return &metadata
}

func archiveFormatFromMetadata(metadata *archiveOptimizationMetadata) string {
	if metadata == nil {
		return "zip"
	}
	return normalizeArchiveFormat(metadata.OriginalArchive.Format)
}

func normalizeArchiveFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "zip":
		return "zip"
	case "tar":
		return "tar"
	case "tar.gz", "tgz":
		return "tar.gz"
	case "tar.bz2", "tbz2":
		return "tar.bz2"
	default:
		return "zip"
	}
}

func archiveFormatFromPath(path string) string {
	name := strings.ToLower(filepath.Base(path))
	switch {
	case strings.HasSuffix(name, ".tar.gz"):
		return "tar.gz"
	case strings.HasSuffix(name, ".tgz"):
		return "tar.gz"
	case strings.HasSuffix(name, ".tar.bz2"):
		return "tar.bz2"
	case strings.HasSuffix(name, ".tbz2"):
		return "tar.bz2"
	case strings.HasSuffix(name, ".tar"):
		return "tar"
	default:
		return strings.TrimPrefix(filepath.Ext(name), ".")
	}
}

func optimizedArchiveFilename(originalFilename string, archiveFormat string) string {
	name := filepath.Base(originalFilename)
	format := normalizeArchiveFormat(archiveFormat)
	suffix := "." + format
	lowerName := strings.ToLower(name)

	if strings.HasSuffix(lowerName, suffix) {
		return name[:len(name)-len(suffix)] + ".optimized." + format
	}

	return "archive.optimized." + format
}

func contentTypeForArchiveFormat(format string) string {
	switch normalizeArchiveFormat(format) {
	case "zip":
		return "application/zip"
	case "tar":
		return "application/x-tar"
	case "tar.gz":
		return "application/gzip"
	case "tar.bz2":
		return "application/x-bzip2"
	default:
		return "application/octet-stream"
	}
}

func createArchiveFromDirectory(sourceDir string, targetPath string, format string) error {
	entries, err := topLevelArchiveEntries(sourceDir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("cannot create archive from empty directory")
	}

	args := []string{}
	switch normalizeArchiveFormat(format) {
	case "tar":
		args = []string{"tar", "-cf", targetPath, "-C", sourceDir, "--"}
	case "tar.gz":
		args = []string{"tar", "-czf", targetPath, "-C", sourceDir, "--"}
	case "tar.bz2":
		args = []string{"tar", "-cjf", targetPath, "-C", sourceDir, "--"}
	default:
		return fmt.Errorf("unsupported archive format: %s", format)
	}
	args = append(args, entries...)

	return runArchiveCommand(args[0], args[1:]...)
}

func extractArchive(archivePath string, destDir string) error {
	switch archiveFormatFromPath(archivePath) {
	case "zip":
		return extractZipArchive(archivePath, destDir)
	case "tar":
		return runArchiveCommand("tar", "-xf", archivePath, "-C", destDir)
	case "tar.gz":
		return runArchiveCommand("tar", "-xzf", archivePath, "-C", destDir)
	case "tar.bz2":
		return runArchiveCommand("tar", "-xjf", archivePath, "-C", destDir)
	default:
		return fmt.Errorf("unsupported archive format: %s", archiveFormatFromPath(archivePath))
	}
}

func (h *WharfHandlers) restoreParentArchive(ctx context.Context, parentBuildID int64, targetDir string) error {
	parentArchive, err := h.findBuildFile(parentBuildID, "archive", "default")
	if err != nil {
		return err
	}
	archivePath := filepath.Join(targetDir, ".parent."+archiveFormatFromPath(parentArchive.StoragePath))
	if err = h.downloadObject(ctx, parentArchive.StoragePath, archivePath); err != nil {
		return err
	}
	if err = extractArchive(archivePath, targetDir); err != nil {
		return err
	}
	return os.Remove(archivePath)
}

func (h *WharfHandlers) extractZip(archivePath string, destDir string) error {
	return extractZipArchive(archivePath, destDir)
}

func extractZipArchive(archivePath string, destDir string) error {
	reader, err := archivezip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, file := range reader.File {
		cleanName := filepath.Clean(file.Name)
		if cleanName == "." || strings.HasPrefix(cleanName, "..") || filepath.IsAbs(cleanName) {
			return fmt.Errorf("unsafe archive entry path: %s", file.Name)
		}
		targetPath := filepath.Join(destDir, cleanName)
		if file.FileInfo().IsDir() {
			if err = os.MkdirAll(targetPath, file.FileInfo().Mode()); err != nil {
				return err
			}
			continue
		}
		if err = os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}
		input, err := file.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, file.FileInfo().Mode())
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeInputErr := input.Close()
		closeOutputErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeInputErr != nil {
			return closeInputErr
		}
		if closeOutputErr != nil {
			return closeOutputErr
		}
	}
	return nil
}

func runArchiveCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func topLevelArchiveEntries(sourceDir string) ([]string, error) {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func (h *WharfHandlers) hasReadyRequiredFile(files map[string]*models.BuildFile, key string) bool {
	file := files[key]
	return file != nil && file.Size > 0
}

func (h *WharfHandlers) updateUploadSizeFromArchive(build *models.Build, archiveFile *models.BuildFile) error {
	if archiveFile == nil || archiveFile.Size <= 0 {
		return nil
	}
	upload, err := h.db.GetUploadByID(build.UploadID)
	if err != nil {
		return err
	}
	if upload.Size == archiveFile.Size {
		return nil
	}
	upload.Size = archiveFile.Size
	return h.db.UpdateUpload(upload)
}

func (h *WharfHandlers) updateUploadFromArchiveMetadata(build *models.Build, metadata *archiveOptimizationMetadata, archiveSize int64) error {
	if metadata == nil || metadata.OriginalArchive.Filename == "" {
		return nil
	}
	upload, err := h.db.GetUploadByID(build.UploadID)
	if err != nil {
		return err
	}

	upload.Filename = optimizedArchiveFilename(metadata.OriginalArchive.Filename, archiveFormatFromMetadata(metadata))
	upload.DisplayName = strings.TrimSuffix(upload.Filename, "."+archiveFormatFromPath(upload.Filename))
	upload.Size = archiveSize
	if platforms := platformsForArchiveFilename(metadata.OriginalArchive.Filename); platforms != "[]" {
		upload.Platforms = platforms
	}

	return h.db.UpdateUpload(upload)
}

func (h *WharfHandlers) advanceCompletedChannelHead(build *models.Build) error {
	if build.ChannelName == "" {
		return fmt.Errorf("build %d has no channel name", build.ID)
	}
	channel, err := h.db.GetChannelByName(build.ChannelName, build.UploadID)
	if err != nil {
		return fmt.Errorf("failed to load channel %s: %w", build.ChannelName, err)
	}
	channel.CurrentBuildID = &build.ID
	return h.db.UpdateChannel(channel)
}
