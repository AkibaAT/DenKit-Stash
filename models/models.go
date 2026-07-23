package models

import (
	"context"
	"time"
)

// BuildArchiveLock is a held advisory lock on a build's archive lifecycle.
type BuildArchiveLock interface {
	Release() error
}

// User represents a user account
type User struct {
	ID          int64     `json:"id" db:"id"`
	Username    string    `json:"username" db:"username"`
	DisplayName string    `json:"display_name" db:"display_name"`
	APIKey      string    `json:"api_key" db:"api_key"`
	Role        string    `json:"role" db:"role"`
	IsActive    bool      `json:"is_active" db:"is_active"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// IsAdmin returns true if the user has admin role
func (u *User) IsAdmin() bool {
	return u.Role == "admin"
}

// CanAccessNamespace returns true if the user can access the given namespace
func (u *User) CanAccessNamespace(namespace string) bool {
	// Admin users can access any namespace
	if u.IsAdmin() {
		return true
	}
	// Regular users can only access their own namespace
	return u.Username == namespace
}

// Game represents a game
type Game struct {
	ID             int64     `json:"id" db:"id"`
	UserID         int64     `json:"user_id" db:"user_id"`
	Title          string    `json:"title" db:"title"`
	ShortText      string    `json:"short_text" db:"short_text"`
	Type           string    `json:"type" db:"type"`
	Classification string    `json:"classification" db:"classification"`
	URL            string    `json:"url" db:"url"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}

// Upload represents a file upload for a game
type Upload struct {
	ID          int64     `json:"id" db:"id"`
	GameID      int64     `json:"game_id" db:"game_id"`
	Filename    string    `json:"filename" db:"filename"`
	DisplayName string    `json:"display_name" db:"display_name"`
	Size        int64     `json:"size" db:"size"`
	Storage     string    `json:"storage" db:"storage"`
	Type        string    `json:"type" db:"type"`
	Platforms   string    `json:"platforms" db:"platforms"` // JSON array as string
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// Build represents a wharf build
type Build struct {
	ID            int64     `json:"id" db:"id"`
	UploadID      int64     `json:"upload_id" db:"upload_id"`
	UserVersion   string    `json:"user_version" db:"user_version"`
	ChannelName   string    `json:"channel_name" db:"channel_name"`
	ParentBuildID *int64    `json:"parent_build_id" db:"parent_build_id"`
	State         string    `json:"state" db:"state"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time `json:"updated_at" db:"updated_at"`
}

// BuildFile represents a file within a build
type BuildFile struct {
	ID          int64  `json:"id" db:"id"`
	BuildID     int64  `json:"build_id" db:"build_id"`
	Type        string `json:"type" db:"type"`
	SubType     string `json:"sub_type" db:"sub_type"`
	Size        int64  `json:"size" db:"size"`
	State       string `json:"state" db:"state"`
	StoragePath string `json:"storage_path" db:"storage_path"`
	UploadURL   string `json:"upload_url" db:"upload_url"`
	// LastAccessedAt drives archive cache eviction; bump it only through
	// Database.TouchBuildFileAccess, never via UpdateBuildFile.
	LastAccessedAt time.Time  `json:"last_accessed_at" db:"last_accessed_at"`
	EvictedAt      *time.Time `json:"evicted_at,omitempty" db:"evicted_at"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`
}

// Channel represents a wharf channel
type Channel struct {
	ID             int64     `json:"id" db:"id"`
	Name           string    `json:"name" db:"name"`
	UploadID       int64     `json:"upload_id" db:"upload_id"`
	CurrentBuildID *int64    `json:"current_build_id" db:"current_build_id"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}

// UploadSession tracks a server-owned resumable build-file upload.
type UploadSession struct {
	ID          string    `json:"id" db:"id"`
	BuildFileID int64     `json:"build_file_id" db:"build_file_id"`
	StoragePath string    `json:"storage_path" db:"storage_path"`
	Size        int64     `json:"size" db:"size"`
	State       string    `json:"state" db:"state"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// Database interface for testing
type Database interface {
	// Users
	GetUserByAPIKey(apiKey string) (*User, error)
	GetUserByID(id int64) (*User, error)
	GetUserByUsername(username string) (*User, error)
	CreateUser(user *User) error
	UpdateUser(user *User) error
	ListUsers() ([]*User, error)

	// Games
	GetGameByID(id int64) (*User, *Game, error)
	GetGamesByUserID(userID int64) ([]*Game, error)
	GetGameByUserAndTitle(userID int64, title string) (*Game, error)
	CreateGame(game *Game) error

	// Uploads
	GetUploadByID(id int64) (*Upload, error)
	GetUploadsByGameID(gameID int64) ([]*Upload, error)
	CreateUpload(upload *Upload) error
	UpdateUpload(upload *Upload) error

	// Builds
	GetBuildByID(id int64) (*Build, error)
	GetBuildsByUploadID(uploadID int64) ([]*Build, error)
	GetBuildsByGameAndChannel(gameID int64, channel string) ([]*Build, error)
	GetLatestCompletedBuildByGameChannelVersion(gameID int64, channel string, userVersion string) (*Build, error)
	CreateBuild(build *Build) error
	UpdateBuild(build *Build) error

	// Build Files
	GetBuildFileByID(id int64) (*BuildFile, error)
	GetBuildFilesByBuildID(buildID int64) ([]*BuildFile, error)
	CreateBuildFile(buildFile *BuildFile) error
	UpdateBuildFile(buildFile *BuildFile) error
	TouchBuildFileAccess(id int64) error
	ListEvictableArchiveFiles(lastAccessedBefore time.Time, limit int) ([]*BuildFile, error)
	ListEvictedArchiveFilesWithStorage(limit int) ([]*BuildFile, error)
	IsChannelHead(buildID int64) (bool, error)

	// Archive locks serialize rebuild/eviction of a build's archive across
	// processes; they are Postgres advisory locks and die with the connection.
	AcquireBuildArchiveLock(ctx context.Context, buildID int64) (BuildArchiveLock, error)
	TryAcquireBuildArchiveLock(ctx context.Context, buildID int64) (BuildArchiveLock, bool, error)

	// Channels
	GetChannelByName(name string, uploadID int64) (*Channel, error)
	GetChannelsByUploadID(uploadID int64) ([]*Channel, error)
	CreateChannel(channel *Channel) error
	UpdateChannel(channel *Channel) error

	// Upload Sessions
	GetUploadSessionByID(id string) (*UploadSession, error)
	CreateUploadSession(session *UploadSession) error
	UpdateUploadSession(session *UploadSession) error

	Close() error
}
