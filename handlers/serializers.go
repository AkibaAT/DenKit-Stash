package handlers

import (
	"denkit-stash/models"
	"time"
)

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func newWharfBuildFileResponse(file *models.BuildFile) WharfBuildFileResponse {
	return WharfBuildFileResponse{
		ID:        file.ID,
		Size:      file.Size,
		State:     file.State,
		Type:      file.Type,
		SubType:   file.SubType,
		CreatedAt: formatTime(file.CreatedAt),
		UpdatedAt: formatTime(file.UpdatedAt),
	}
}

func newWharfBuildFileResponses(files []*models.BuildFile) []WharfBuildFileResponse {
	out := make([]WharfBuildFileResponse, 0, len(files))
	for _, file := range files {
		out = append(out, newWharfBuildFileResponse(file))
	}
	return out
}

func newWharfBuildResponse(build *models.Build, files []*models.BuildFile) WharfBuildResponse {
	parentBuildID := int64(0)
	var parentBuild *WharfParentBuildResponse
	if build.ParentBuildID != nil {
		parentBuildID = *build.ParentBuildID
		parentBuild = &WharfParentBuildResponse{ID: parentBuildID}
	}

	return WharfBuildResponse{
		ID:            build.ID,
		UploadID:      build.UploadID,
		ParentBuildID: parentBuildID,
		ParentBuild:   parentBuild,
		Version:       build.ID,
		State:         build.State,
		UserVersion:   build.UserVersion,
		Files:         newWharfBuildFileResponses(files),
		CreatedAt:     formatTime(build.CreatedAt),
		UpdatedAt:     formatTime(build.UpdatedAt),
	}
}

func newCoreUploadResponse(upload *models.Upload) CoreUploadResponse {
	return CoreUploadResponse{
		ID:          upload.ID,
		Filename:    upload.Filename,
		DisplayName: upload.DisplayName,
		Size:        upload.Size,
		Storage:     upload.Storage,
		Type:        upload.Type,
		Platforms:   upload.Platforms,
	}
}

func newCoreBuildResponse(build *models.Build, includeUploadID bool) CoreBuildResponse {
	response := CoreBuildResponse{
		ID:            build.ID,
		UserVersion:   build.UserVersion,
		State:         build.State,
		ParentBuildID: build.ParentBuildID,
		CreatedAt:     formatTime(build.CreatedAt),
	}
	if includeUploadID {
		response.UploadID = build.UploadID
	}
	return response
}

func newWharfChannelResponse(channel *models.Channel, upload *models.Upload, head *models.Build, files []*models.BuildFile) WharfChannelResponse {
	out := WharfChannelResponse{Name: channel.Name, Upload: WharfUploadRefResponse{ID: upload.ID}}
	if head != nil {
		response := newWharfBuildResponse(head, files)
		out.Head = &response
	}
	return out
}
