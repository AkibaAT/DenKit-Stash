package handlers

import "time"

type ErrorResponse struct {
	Errors []string `json:"errors"`
}

type StatusResponse struct {
	Message string `json:"message" example:"DenKit Stash"`
	Version string `json:"version" example:"1.0.0"`
}

type PublicUserResponse struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

type ProfileResponse struct {
	User PublicUserResponse `json:"user"`
}

type GameResponse struct {
	ID             int64              `json:"id"`
	Title          string             `json:"title"`
	ShortText      string             `json:"short_text"`
	Type           string             `json:"type"`
	Classification string             `json:"classification"`
	URL            string             `json:"url"`
	User           PublicUserResponse `json:"user"`
}

type ProfileGameResponse struct {
	ID             int64     `json:"id"`
	UserID         int64     `json:"user_id"`
	Title          string    `json:"title"`
	ShortText      string    `json:"short_text"`
	Type           string    `json:"type"`
	Classification string    `json:"classification"`
	URL            string    `json:"url"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type GameListResponse struct {
	Games []ProfileGameResponse `json:"games"`
}

type GameEnvelopeResponse struct {
	Game GameResponse `json:"game"`
}

type CoreUploadResponse struct {
	ID          int64  `json:"id"`
	Filename    string `json:"filename"`
	DisplayName string `json:"display_name"`
	Size        int64  `json:"size"`
	Storage     string `json:"storage"`
	Type        string `json:"type"`
	Platforms   string `json:"platforms" doc:"JSON array encoded as a string."`
}

type UploadListResponse struct {
	Uploads []CoreUploadResponse `json:"uploads"`
}

type UploadEnvelopeResponse struct {
	Upload CoreUploadResponse `json:"upload"`
}

type CoreBuildResponse struct {
	ID            int64  `json:"id"`
	UploadID      int64  `json:"upload_id,omitempty"`
	UserVersion   string `json:"user_version"`
	State         string `json:"state"`
	ParentBuildID *int64 `json:"parent_build_id,omitempty"`
	CreatedAt     string `json:"created_at"`
}

type BuildListResponse struct {
	Builds []CoreBuildResponse `json:"builds"`
}

type BuildEnvelopeResponse struct {
	Build CoreBuildResponse `json:"build"`
}

type DownloadSessionResponse struct {
	UUID string `json:"uuid" format:"uuid"`
}

type WharfStatusResponse struct {
	OK bool `json:"ok" example:"true"`
}

type WharfBuildFileResponse struct {
	ID        int64  `json:"id"`
	Size      int64  `json:"size"`
	State     string `json:"state" enum:"uploading,uploaded,failed,evicted,rebuilding"`
	Type      string `json:"type" enum:"archive,patch,signature"`
	SubType   string `json:"subType" example:"default"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type WharfParentBuildResponse struct {
	ID int64 `json:"id"`
}

type WharfBuildResponse struct {
	ID            int64                     `json:"id"`
	UploadID      int64                     `json:"uploadId"`
	ParentBuildID int64                     `json:"parentBuildId"`
	ParentBuild   *WharfParentBuildResponse `json:"parentBuild"`
	Version       int64                     `json:"version"`
	State         string                    `json:"state" enum:"started,processing,completed,failed"`
	UserVersion   string                    `json:"userVersion"`
	Files         []WharfBuildFileResponse  `json:"files"`
	CreatedAt     string                    `json:"createdAt"`
	UpdatedAt     string                    `json:"updatedAt"`
}

type WharfBuildEnvelopeResponse struct {
	Build WharfBuildResponse `json:"build"`
}

type WharfUploadRefResponse struct {
	ID int64 `json:"id"`
}

type WharfChannelResponse struct {
	Name   string                 `json:"name"`
	Upload WharfUploadRefResponse `json:"upload"`
	Head   *WharfBuildResponse    `json:"head,omitempty"`
}

type ChannelEnvelopeResponse struct {
	Channel WharfChannelResponse `json:"channel"`
}

type ChannelMapResponse struct {
	Channels map[string]WharfChannelResponse `json:"channels"`
}

type CreateBuildRequest struct {
	Target      string `json:"target" form:"target" example:"username/game" required:"true"`
	Channel     string `json:"channel" form:"channel" example:"main" required:"true"`
	UserVersion string `json:"user_version,omitempty" form:"user_version"`
}

type CreateBuildFileRequest struct {
	Type       string `json:"type" form:"type" example:"patch" enum:"archive,patch,signature" required:"true"`
	SubType    string `json:"sub_type" form:"sub_type" example:"default"`
	UploadType string `json:"upload_type" form:"upload_type" example:"deferred_resumable" enum:"deferred_resumable,deferred-resumable"`
}

type BuildFileUploadResponse struct {
	ID            int64             `json:"id"`
	UploadURL     string            `json:"uploadUrl" format:"uri"`
	UploadParams  map[string]string `json:"uploadParams"`
	UploadHeaders map[string]string `json:"uploadHeaders"`
}

type BuildFileUploadEnvelopeResponse struct {
	File BuildFileUploadResponse `json:"file"`
}

type FinalizeBuildFileRequest struct {
	Size int64 `json:"size" form:"size"`
}

type FinalizedBuildFileResponse struct {
	ID    int64  `json:"id"`
	Size  int64  `json:"size"`
	State string `json:"state"`
}

type FinalizedBuildFileEnvelopeResponse struct {
	File FinalizedBuildFileResponse `json:"file"`
}

type BuildFilesResponse struct {
	Files []WharfBuildFileResponse `json:"files"`
}

type UpgradePathBody struct {
	Builds []WharfBuildResponse `json:"builds"`
}

type UpgradePathResponse struct {
	UpgradePath UpgradePathBody `json:"upgradePath"`
}

type SignedURLResponse struct {
	URL string `json:"url" format:"uri"`
}

type StorageTestResponse struct {
	Message     string `json:"message"`
	SignedURL   string `json:"signed_url" format:"uri"`
	ExpiresIn   string `json:"expires_in"`
	TestContent string `json:"test_content"`
}
