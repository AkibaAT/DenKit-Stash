package models

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/lib/pq"
)

type PostgresDatabase struct {
	db *sql.DB
}

func NewPostgresDatabase() (*PostgresDatabase, error) {
	host := cmp.Or(os.Getenv("POSTGRES_HOST"), "localhost")
	port := cmp.Or(os.Getenv("POSTGRES_PORT"), "5432")
	user := cmp.Or(os.Getenv("POSTGRES_USER"), "postgres")
	password := cmp.Or(os.Getenv("POSTGRES_PASSWORD"), "postgres")
	dbname := cmp.Or(os.Getenv("POSTGRES_DB"), "butler")
	sslmode := cmp.Or(os.Getenv("POSTGRES_SSLMODE"), "disable")

	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		host, port, user, password, dbname, sslmode)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %v", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %v", err)
	}

	if _, err := APIKeyHashSecret(); err != nil {
		return nil, err
	}

	pgDB := &PostgresDatabase{db: db}

	if err := pgDB.migrate(); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %v", err)
	}

	return pgDB, nil
}

func (d *PostgresDatabase) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id SERIAL PRIMARY KEY,
			username VARCHAR(255) UNIQUE NOT NULL,
			display_name VARCHAR(255) NOT NULL,
			api_key VARCHAR(255) UNIQUE NOT NULL,
			role VARCHAR(50) DEFAULT 'user' CHECK (role IN ('user', 'admin')),
			is_active BOOLEAN DEFAULT true,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS games (
			id SERIAL PRIMARY KEY,
			user_id INTEGER REFERENCES users(id),
			title VARCHAR(255) NOT NULL,
			short_text TEXT,
			type VARCHAR(50) DEFAULT 'default',
			classification VARCHAR(50) DEFAULT 'game',
			url VARCHAR(255),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS uploads (
			id SERIAL PRIMARY KEY,
			game_id INTEGER REFERENCES games(id),
			filename VARCHAR(255),
			display_name VARCHAR(255),
			storage VARCHAR(255),
			size BIGINT DEFAULT 0,
			type VARCHAR(50) DEFAULT 'default',
			platforms TEXT DEFAULT '[]',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS channels (
			id SERIAL PRIMARY KEY,
			upload_id INTEGER REFERENCES uploads(id),
			name VARCHAR(255) NOT NULL,
			current_build_id INTEGER,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(name, upload_id)
		)`,
		`CREATE TABLE IF NOT EXISTS builds (
			id SERIAL PRIMARY KEY,
			upload_id INTEGER REFERENCES uploads(id),
			channel_name VARCHAR(255) DEFAULT '',
			parent_build_id INTEGER REFERENCES builds(id),
			user_version VARCHAR(255),
			state VARCHAR(50) DEFAULT 'started',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS build_files (
			id SERIAL PRIMARY KEY,
			build_id INTEGER REFERENCES builds(id),
			type VARCHAR(50) NOT NULL,
			sub_type VARCHAR(50) NOT NULL,
			state VARCHAR(50) DEFAULT 'uploading',
			storage_path VARCHAR(255),
			upload_url TEXT,
			size BIGINT DEFAULT 0,
			last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			evicted_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS upload_sessions (
			id VARCHAR(255) PRIMARY KEY,
			build_file_id INTEGER REFERENCES build_files(id),
			storage_path VARCHAR(255),
			size BIGINT DEFAULT 0,
			state VARCHAR(50) DEFAULT 'uploading',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_channels_current_build_id ON channels(current_build_id)`,
		`CREATE INDEX IF NOT EXISTS idx_build_files_archive_gc ON build_files(type, sub_type, state, last_accessed_at)`,
	}

	for _, migration := range migrations {
		if _, err := d.db.Exec(migration); err != nil {
			return fmt.Errorf("failed to execute migration: %v", err)
		}
	}

	return nil
}

func (d *PostgresDatabase) Close() error {
	return d.db.Close()
}

const userColumns = `id, username, display_name, api_key, role, is_active, created_at, updated_at`

func scanUser(row interface{ Scan(...interface{}) error }) (*User, error) {
	user := &User{}
	if err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &user.APIKey,
		&user.Role, &user.IsActive, &user.CreatedAt, &user.UpdatedAt); err != nil {
		return nil, err
	}
	return user, nil
}

func (d *PostgresDatabase) GetUserByAPIKey(apiKey string) (*User, error) {
	apiKeyDigest, err := APIKeyDigest(apiKey)
	if err != nil {
		return nil, err
	}
	return scanUser(d.db.QueryRow(`
		SELECT `+userColumns+`
		FROM users WHERE api_key = $1 AND is_active = true`, apiKeyDigest))
}

func (d *PostgresDatabase) GetUserByUsername(username string) (*User, error) {
	return scanUser(d.db.QueryRow(`
		SELECT `+userColumns+`
		FROM users WHERE username = $1`, username))
}

func (d *PostgresDatabase) CreateUser(user *User) error {
	if user.Role == "" {
		user.Role = "user"
	}

	apiKeyDigest, err := APIKeyDigest(user.APIKey)
	if err != nil {
		return err
	}

	err = d.db.QueryRow(`
		INSERT INTO users (username, display_name, api_key, role, is_active)
		VALUES ($1, $2, $3, $4, $5) RETURNING id, created_at, updated_at`,
		user.Username, user.DisplayName, apiKeyDigest, user.Role, user.IsActive).Scan(
		&user.ID, &user.CreatedAt, &user.UpdatedAt)
	return err
}

func (d *PostgresDatabase) UpdateUser(user *User) error {
	apiKeyDigest, err := APIKeyDigest(user.APIKey)
	if err != nil {
		return err
	}
	_, err = d.db.Exec(`
		UPDATE users SET username = $1, display_name = $2, api_key = $3, role = $4, is_active = $5, updated_at = CURRENT_TIMESTAMP
		WHERE id = $6`,
		user.Username, user.DisplayName, apiKeyDigest, user.Role, user.IsActive, user.ID)
	return err
}

func (d *PostgresDatabase) ListUsers() ([]*User, error) {
	rows, err := d.db.Query(`
		SELECT ` + userColumns + `
		FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, nil
}

func (d *PostgresDatabase) GetGameByID(id int64) (*User, *Game, error) {
	game := &Game{}
	user := &User{}

	err := d.db.QueryRow(`
		SELECT
			g.id, g.user_id, g.title, g.short_text, g.type, g.classification, g.url, g.created_at, g.updated_at,
			u.id, u.username, u.display_name, u.api_key, u.role, u.is_active, u.created_at, u.updated_at
		FROM games g
		JOIN users u ON g.user_id = u.id
		WHERE g.id = $1`, id).Scan(
		&game.ID, &game.UserID, &game.Title, &game.ShortText, &game.Type, &game.Classification, &game.URL, &game.CreatedAt, &game.UpdatedAt,
		&user.ID, &user.Username, &user.DisplayName, &user.APIKey, &user.Role, &user.IsActive, &user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		return nil, nil, err
	}

	return user, game, nil
}

func (d *PostgresDatabase) GetGameByUserAndTitle(userID int64, title string) (*Game, error) {
	game := &Game{}
	err := d.db.QueryRow(`
		SELECT id, user_id, title, short_text, type, classification, url, created_at, updated_at
		FROM games WHERE user_id = $1 AND title = $2`, userID, title).Scan(
		&game.ID, &game.UserID, &game.Title, &game.ShortText, &game.Type, &game.Classification, &game.URL, &game.CreatedAt, &game.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return game, nil
}

func (d *PostgresDatabase) CreateGame(game *Game) error {
	err := d.db.QueryRow(`
		INSERT INTO games (user_id, title, short_text, type, classification, url)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, created_at, updated_at`,
		game.UserID, game.Title, game.ShortText, game.Type, game.Classification, game.URL).Scan(
		&game.ID, &game.CreatedAt, &game.UpdatedAt)
	return err
}

func (d *PostgresDatabase) GetUploadsByGameID(gameID int64) ([]*Upload, error) {
	rows, err := d.db.Query(`
		SELECT id, game_id, filename, display_name, storage, size, type, platforms, created_at, updated_at
		FROM uploads WHERE game_id = $1`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var uploads []*Upload
	for rows.Next() {
		upload := &Upload{}
		err := rows.Scan(&upload.ID, &upload.GameID, &upload.Filename, &upload.DisplayName,
			&upload.Storage, &upload.Size, &upload.Type, &upload.Platforms, &upload.CreatedAt, &upload.UpdatedAt)
		if err != nil {
			return nil, err
		}
		uploads = append(uploads, upload)
	}
	return uploads, nil
}

func (d *PostgresDatabase) CreateUpload(upload *Upload) error {
	if upload.Type == "" {
		upload.Type = "default"
	}
	if upload.Platforms == "" {
		upload.Platforms = "[]"
	}

	err := d.db.QueryRow(`
		INSERT INTO uploads (game_id, filename, display_name, storage, size, type, platforms)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, created_at, updated_at`,
		upload.GameID, upload.Filename, upload.DisplayName, upload.Storage, upload.Size, upload.Type, upload.Platforms).Scan(
		&upload.ID, &upload.CreatedAt, &upload.UpdatedAt)
	return err
}

func (d *PostgresDatabase) UpdateUpload(upload *Upload) error {
	_, err := d.db.Exec(`
		UPDATE uploads
		SET game_id = $1, filename = $2, display_name = $3, storage = $4, size = $5,
			type = $6, platforms = $7, updated_at = NOW()
		WHERE id = $8`,
		upload.GameID, upload.Filename, upload.DisplayName, upload.Storage, upload.Size,
		upload.Type, upload.Platforms, upload.ID)
	return err
}

func (d *PostgresDatabase) GetUploadByID(id int64) (*Upload, error) {
	upload := &Upload{}
	err := d.db.QueryRow(`
		SELECT id, game_id, filename, display_name, storage, size, type, platforms, created_at, updated_at
		FROM uploads WHERE id = $1`, id).Scan(
		&upload.ID, &upload.GameID, &upload.Filename, &upload.DisplayName,
		&upload.Storage, &upload.Size, &upload.Type, &upload.Platforms, &upload.CreatedAt, &upload.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return upload, nil
}

func (d *PostgresDatabase) GetGamesByUserID(userID int64) ([]*Game, error) {
	rows, err := d.db.Query(`
		SELECT id, user_id, title, short_text, type, classification, url, created_at, updated_at
		FROM games WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var games []*Game
	for rows.Next() {
		game := &Game{}
		err := rows.Scan(&game.ID, &game.UserID, &game.Title, &game.ShortText, &game.Type,
			&game.Classification, &game.URL, &game.CreatedAt, &game.UpdatedAt)
		if err != nil {
			return nil, err
		}
		games = append(games, game)
	}
	return games, nil
}

const (
	buildColumns          = `id, upload_id, channel_name, parent_build_id, user_version, state, created_at, updated_at`
	qualifiedBuildColumns = `b.id, b.upload_id, b.channel_name, b.parent_build_id, b.user_version, b.state, b.created_at, b.updated_at`
)

func scanBuild(row interface{ Scan(...interface{}) error }) (*Build, error) {
	build := &Build{}
	var parentBuildID sql.NullInt64
	if err := row.Scan(&build.ID, &build.UploadID, &build.ChannelName, &parentBuildID,
		&build.UserVersion, &build.State, &build.CreatedAt, &build.UpdatedAt); err != nil {
		return nil, err
	}
	if parentBuildID.Valid {
		build.ParentBuildID = &parentBuildID.Int64
	}
	return build, nil
}

func (d *PostgresDatabase) GetBuildByID(id int64) (*Build, error) {
	return scanBuild(d.db.QueryRow(`
		SELECT `+buildColumns+`
		FROM builds WHERE id = $1`, id))
}

func (d *PostgresDatabase) CreateBuild(build *Build) error {
	var parentBuildID interface{}
	if build.ParentBuildID != nil {
		parentBuildID = *build.ParentBuildID
	}
	err := d.db.QueryRow(`
		INSERT INTO builds (upload_id, channel_name, parent_build_id, user_version, state)
		VALUES ($1, $2, $3, $4, $5) RETURNING id, created_at, updated_at`,
		build.UploadID, build.ChannelName, parentBuildID, build.UserVersion, build.State).Scan(
		&build.ID, &build.CreatedAt, &build.UpdatedAt)
	return err
}

func (d *PostgresDatabase) UpdateBuild(build *Build) error {
	var parentBuildID interface{}
	if build.ParentBuildID != nil {
		parentBuildID = *build.ParentBuildID
	}
	_, err := d.db.Exec(`
		UPDATE builds SET upload_id = $1, channel_name = $2, parent_build_id = $3, user_version = $4, state = $5, updated_at = CURRENT_TIMESTAMP
		WHERE id = $6`,
		build.UploadID, build.ChannelName, parentBuildID, build.UserVersion, build.State, build.ID)
	return err
}

func (d *PostgresDatabase) GetBuildsByUploadID(uploadID int64) ([]*Build, error) {
	rows, err := d.db.Query(`
		SELECT `+buildColumns+`
		FROM builds WHERE upload_id = $1 ORDER BY id DESC`, uploadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var builds []*Build
	for rows.Next() {
		build, err := scanBuild(rows)
		if err != nil {
			return nil, err
		}
		builds = append(builds, build)
	}
	return builds, nil
}

func (d *PostgresDatabase) GetBuildsByGameAndChannel(gameID int64, channel string) ([]*Build, error) {
	rows, err := d.db.Query(`
		SELECT `+qualifiedBuildColumns+`
		FROM builds b
		JOIN uploads u ON u.id = b.upload_id
		WHERE u.game_id = $1
			AND b.channel_name = $2
		ORDER BY b.id DESC`, gameID, channel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var builds []*Build
	for rows.Next() {
		build, err := scanBuild(rows)
		if err != nil {
			return nil, err
		}
		builds = append(builds, build)
	}
	return builds, nil
}

func (d *PostgresDatabase) GetLatestCompletedBuildByGameChannelVersion(gameID int64, channel string, userVersion string) (*Build, error) {
	return scanBuild(d.db.QueryRow(`
		SELECT `+qualifiedBuildColumns+`
		FROM builds b
		JOIN uploads u ON u.id = b.upload_id
		WHERE u.game_id = $1
			AND b.channel_name = $2
			AND b.user_version = $3
			AND b.state = 'completed'
		ORDER BY b.id DESC
		LIMIT 1`, gameID, channel, userVersion))
}

const (
	buildFileColumns          = `id, build_id, type, sub_type, state, storage_path, upload_url, size, last_accessed_at, evicted_at, created_at, updated_at`
	qualifiedBuildFileColumns = `bf.id, bf.build_id, bf.type, bf.sub_type, bf.state, bf.storage_path, bf.upload_url, bf.size, bf.last_accessed_at, bf.evicted_at, bf.created_at, bf.updated_at`
)

func scanBuildFile(row interface{ Scan(...interface{}) error }) (*BuildFile, error) {
	file := &BuildFile{}
	var evictedAt sql.NullTime
	err := row.Scan(&file.ID, &file.BuildID, &file.Type, &file.SubType,
		&file.State, &file.StoragePath, &file.UploadURL, &file.Size,
		&file.LastAccessedAt, &evictedAt, &file.CreatedAt, &file.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if evictedAt.Valid {
		file.EvictedAt = &evictedAt.Time
	}
	return file, nil
}

func (d *PostgresDatabase) GetBuildFilesByBuildID(buildID int64) ([]*BuildFile, error) {
	rows, err := d.db.Query(`
		SELECT `+buildFileColumns+`
		FROM build_files WHERE build_id = $1`, buildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []*BuildFile
	for rows.Next() {
		file, err := scanBuildFile(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func (d *PostgresDatabase) GetBuildFileByID(id int64) (*BuildFile, error) {
	return scanBuildFile(d.db.QueryRow(`
		SELECT `+buildFileColumns+`
		FROM build_files WHERE id = $1`, id))
}

func (d *PostgresDatabase) CreateBuildFile(file *BuildFile) error {
	err := d.db.QueryRow(`
		INSERT INTO build_files (build_id, type, sub_type, state, storage_path, upload_url, size)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, last_accessed_at, created_at, updated_at`,
		file.BuildID, file.Type, file.SubType, file.State, file.StoragePath, file.UploadURL, file.Size).Scan(
		&file.ID, &file.LastAccessedAt, &file.CreatedAt, &file.UpdatedAt)
	return err
}

// UpdateBuildFile intentionally leaves last_accessed_at alone; use
// TouchBuildFileAccess so unrelated updates can't skew eviction TTLs.
func (d *PostgresDatabase) UpdateBuildFile(file *BuildFile) error {
	var evictedAt interface{}
	if file.EvictedAt != nil {
		evictedAt = *file.EvictedAt
	}
	_, err := d.db.Exec(`
		UPDATE build_files SET build_id = $1, type = $2, sub_type = $3, state = $4, storage_path = $5, upload_url = $6, size = $7, evicted_at = $8, updated_at = CURRENT_TIMESTAMP
		WHERE id = $9`,
		file.BuildID, file.Type, file.SubType, file.State, file.StoragePath, file.UploadURL, file.Size, evictedAt, file.ID)
	return err
}

func (d *PostgresDatabase) TouchBuildFileAccess(id int64) error {
	_, err := d.db.Exec(`UPDATE build_files SET last_accessed_at = CURRENT_TIMESTAMP WHERE id = $1`, id)
	return err
}

func (d *PostgresDatabase) ListEvictableArchiveFiles(lastAccessedBefore time.Time, limit int) ([]*BuildFile, error) {
	rows, err := d.db.Query(`
		SELECT `+qualifiedBuildFileColumns+`
		FROM build_files bf
		JOIN builds b ON b.id = bf.build_id
		WHERE bf.type = 'archive' AND bf.sub_type = 'default'
		  AND bf.state = 'uploaded'
		  AND b.state = 'completed'
		  AND COALESCE(bf.last_accessed_at, bf.updated_at) < $1
		  AND NOT EXISTS (SELECT 1 FROM channels c WHERE c.current_build_id = b.id)
		ORDER BY bf.last_accessed_at ASC
		LIMIT $2`, lastAccessedBefore, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []*BuildFile
	for rows.Next() {
		file, err := scanBuildFile(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func (d *PostgresDatabase) ListEvictedArchiveFilesWithStorage(limit int) ([]*BuildFile, error) {
	rows, err := d.db.Query(`
		SELECT `+buildFileColumns+`
		FROM build_files
		WHERE type = 'archive' AND sub_type = 'default' AND state = 'evicted'
		  AND storage_path IS NOT NULL AND storage_path <> ''
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []*BuildFile
	for rows.Next() {
		file, err := scanBuildFile(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func (d *PostgresDatabase) IsChannelHead(buildID int64) (bool, error) {
	var isHead bool
	err := d.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM channels WHERE current_build_id = $1)`, buildID).Scan(&isHead)
	return isHead, err
}

// Archive advisory locks. The lock is session-scoped, so it must be taken on a
// pinned connection and is released automatically if that connection dies.
const archiveLockNamespace int64 = 0x44454E4B // "DENK"

func archiveLockKey(buildID int64) int64 {
	return archiveLockNamespace<<32 | (buildID & 0xFFFFFFFF)
}

type buildArchiveLock struct {
	conn *sql.Conn
	key  int64
}

func (l *buildArchiveLock) Release() error {
	_, unlockErr := l.conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, l.key)
	closeErr := l.conn.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

func (d *PostgresDatabase) AcquireBuildArchiveLock(ctx context.Context, buildID int64) (BuildArchiveLock, error) {
	conn, err := d.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	key := archiveLockKey(buildID)
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		conn.Close()
		return nil, err
	}
	return &buildArchiveLock{conn: conn, key: key}, nil
}

func (d *PostgresDatabase) TryAcquireBuildArchiveLock(ctx context.Context, buildID int64) (BuildArchiveLock, bool, error) {
	conn, err := d.db.Conn(ctx)
	if err != nil {
		return nil, false, err
	}
	key := archiveLockKey(buildID)
	var acquired bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&acquired); err != nil {
		conn.Close()
		return nil, false, err
	}
	if !acquired {
		conn.Close()
		return nil, false, nil
	}
	return &buildArchiveLock{conn: conn, key: key}, true, nil
}

const channelColumns = `id, upload_id, name, current_build_id, created_at, updated_at`

func scanChannel(row interface{ Scan(...interface{}) error }) (*Channel, error) {
	channel := &Channel{}
	var buildID sql.NullInt64
	if err := row.Scan(&channel.ID, &channel.UploadID, &channel.Name, &buildID,
		&channel.CreatedAt, &channel.UpdatedAt); err != nil {
		return nil, err
	}
	if buildID.Valid {
		channel.CurrentBuildID = &buildID.Int64
	}
	return channel, nil
}

func (d *PostgresDatabase) GetChannelsByUploadID(uploadID int64) ([]*Channel, error) {
	rows, err := d.db.Query(`
		SELECT `+channelColumns+`
		FROM channels WHERE upload_id = $1`, uploadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []*Channel
	for rows.Next() {
		channel, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		channels = append(channels, channel)
	}
	return channels, nil
}

func (d *PostgresDatabase) GetChannelByName(name string, uploadID int64) (*Channel, error) {
	return scanChannel(d.db.QueryRow(`
		SELECT `+channelColumns+`
		FROM channels WHERE name = $1 AND upload_id = $2`, name, uploadID))
}

func (d *PostgresDatabase) CreateChannel(channel *Channel) error {
	var currentBuildID interface{}
	if channel.CurrentBuildID != nil {
		currentBuildID = *channel.CurrentBuildID
	}
	err := d.db.QueryRow(`
		INSERT INTO channels (upload_id, name, current_build_id)
		VALUES ($1, $2, $3) RETURNING id, created_at, updated_at`,
		channel.UploadID, channel.Name, currentBuildID).Scan(
		&channel.ID, &channel.CreatedAt, &channel.UpdatedAt)
	return err
}

func (d *PostgresDatabase) UpdateChannel(channel *Channel) error {
	var currentBuildID interface{}
	if channel.CurrentBuildID != nil {
		currentBuildID = *channel.CurrentBuildID
	}
	_, err := d.db.Exec(`
		UPDATE channels SET upload_id = $1, name = $2, current_build_id = $3, updated_at = CURRENT_TIMESTAMP
		WHERE id = $4`,
		channel.UploadID, channel.Name, currentBuildID, channel.ID)
	return err
}

func (d *PostgresDatabase) GetUploadSessionByID(id string) (*UploadSession, error) {
	session := &UploadSession{}
	err := d.db.QueryRow(`
		SELECT id, build_file_id, storage_path, size, state, created_at, updated_at
		FROM upload_sessions WHERE id = $1`, id).Scan(
		&session.ID, &session.BuildFileID, &session.StoragePath, &session.Size,
		&session.State, &session.CreatedAt, &session.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (d *PostgresDatabase) CreateUploadSession(session *UploadSession) error {
	_, err := d.db.Exec(`
		INSERT INTO upload_sessions (id, build_file_id, storage_path, size, state)
		VALUES ($1, $2, $3, $4, $5)`,
		session.ID, session.BuildFileID, session.StoragePath, session.Size, session.State)
	return err
}

func (d *PostgresDatabase) UpdateUploadSession(session *UploadSession) error {
	_, err := d.db.Exec(`
		UPDATE upload_sessions SET build_file_id = $1, storage_path = $2, size = $3, state = $4, updated_at = CURRENT_TIMESTAMP
		WHERE id = $5`,
		session.BuildFileID, session.StoragePath, session.Size, session.State, session.ID)
	return err
}
