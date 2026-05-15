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

func serializeBuildFile(file *models.BuildFile) map[string]interface{} {
	return map[string]interface{}{
		"id":        file.ID,
		"size":      file.Size,
		"state":     file.State,
		"type":      file.Type,
		"subType":   file.SubType,
		"createdAt": formatTime(file.CreatedAt),
		"updatedAt": formatTime(file.UpdatedAt),
	}
}

func serializeBuildFileList(files []*models.BuildFile) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(files))
	for _, file := range files {
		out = append(out, serializeBuildFile(file))
	}
	return out
}

func serializeBuild(build *models.Build, files []*models.BuildFile) map[string]interface{} {
	parentBuildID := int64(0)
	if build.ParentBuildID != nil {
		parentBuildID = *build.ParentBuildID
	}

	out := map[string]interface{}{
		"id":            build.ID,
		"uploadId":      build.UploadID,
		"parentBuildId": parentBuildID,
		"version":       build.ID,
		"state":         build.State,
		"userVersion":   build.UserVersion,
		"files":         serializeBuildFileList(files),
		"createdAt":     formatTime(build.CreatedAt),
		"updatedAt":     formatTime(build.UpdatedAt),
	}
	if parentBuildID != 0 {
		out["parentBuild"] = map[string]interface{}{
			"id": parentBuildID,
		}
	} else {
		out["parentBuild"] = nil
	}
	return out
}

func serializeUpload(upload *models.Upload) map[string]interface{} {
	return map[string]interface{}{
		"id":          upload.ID,
		"gameId":      upload.GameID,
		"filename":    upload.Filename,
		"displayName": upload.DisplayName,
		"size":        upload.Size,
		"storage":     upload.Storage,
		"type":        upload.Type,
		"platforms":   upload.Platforms,
		"createdAt":   formatTime(upload.CreatedAt),
		"updatedAt":   formatTime(upload.UpdatedAt),
	}
}

func serializeChannel(channel *models.Channel, upload *models.Upload, head *models.Build, files []*models.BuildFile) map[string]interface{} {
	out := map[string]interface{}{
		"name": channel.Name,
		"upload": map[string]interface{}{
			"id": upload.ID,
		},
	}
	if head != nil {
		out["head"] = serializeBuild(head, files)
	}
	return out
}
