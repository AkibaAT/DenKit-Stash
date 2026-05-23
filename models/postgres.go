package models

import (
	"database/sql"
	"fmt"
	"os"

	_ "github.com/lib/pq"
)

// PostgresDatabase implements the Database interface using PostgreSQL
type PostgresDatabase struct {
	db *sql.DB
}

// NewPostgresDatabase creates a new PostgreSQL database connection
func NewPostgresDatabase() (*PostgresDatabase, error) {
	host := getEnvOrDefault("POSTGRES_HOST", "localhost")
	port := getEnvOrDefault("POSTGRES_PORT", "5432")
	user := getEnvOrDefault("POSTGRES_USER", "postgres")
	password := getEnvOrDefault("POSTGRES_PASSWORD", "postgres")
	dbname := getEnvOrDefault("POSTGRES_DB", "butler")
	sslmode := getEnvOrDefault("POSTGRES_SSLMODE", "disable")

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

	// Run migrations
	if err := pgDB.migrate(); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %v", err)
	}

	return pgDB, nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// migrate runs the database migrations
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
	}

	for _, migration := range migrations {
		if _, err := d.db.Exec(migration); err != nil {
			return fmt.Errorf("failed to execute migration: %v", err)
		}
	}

	alterStatements := []string{
		`ALTER TABLE builds ADD COLUMN IF NOT EXISTS channel_name VARCHAR(255) DEFAULT ''`,
		`ALTER TABLE build_files ADD COLUMN IF NOT EXISTS upload_url TEXT`,
		`ALTER TABLE channels ADD COLUMN IF NOT EXISTS current_build_id INTEGER`,
		`ALTER TABLE uploads ALTER COLUMN type SET DEFAULT 'default'`,
		`ALTER TABLE uploads ALTER COLUMN platforms SET DEFAULT '[]'`,
		`UPDATE uploads SET type = 'default' WHERE type IS NULL`,
		`UPDATE uploads SET platforms = '[]' WHERE platforms IS NULL`,
	}
	for _, stmt := range alterStatements {
		if _, err := d.db.Exec(stmt); err != nil {
			return fmt.Errorf("failed to update schema: %v", err)
		}
	}

	return d.convertLegacyAPIKeys()
}

func (d *PostgresDatabase) convertLegacyAPIKeys() error {
	rows, err := d.db.Query(`SELECT id, api_key FROM users WHERE api_key NOT LIKE $1`, apiKeyDigestPrefix+"%")
	if err != nil {
		return err
	}
	defer rows.Close()

	type legacyKey struct {
		id     int64
		apiKey string
	}
	var keys []legacyKey
	for rows.Next() {
		key := legacyKey{}
		if err = rows.Scan(&key.id, &key.apiKey); err != nil {
			return err
		}
		keys = append(keys, key)
	}
	if err = rows.Err(); err != nil {
		return err
	}

	for _, key := range keys {
		digest, err := APIKeyDigest(key.apiKey)
		if err != nil {
			return fmt.Errorf("failed to digest legacy API key for user %d: %w", key.id, err)
		}
		if _, err = d.db.Exec(`UPDATE users SET api_key = $1 WHERE id = $2`, digest, key.id); err != nil {
			return err
		}
	}
	return nil
}

// Close closes the database connection
func (d *PostgresDatabase) Close() error {
	return d.db.Close()
}

// User methods
func (d *PostgresDatabase) GetUserByAPIKey(apiKey string) (*User, error) {
	apiKeyDigest, err := APIKeyDigest(apiKey)
	if err != nil {
		return nil, err
	}
	user := &User{}
	err = d.db.QueryRow(`
		SELECT id, username, display_name, api_key, role, is_active, created_at, updated_at 
		FROM users WHERE api_key = $1 AND is_active = true`, apiKeyDigest).Scan(
		&user.ID, &user.Username, &user.DisplayName, &user.APIKey,
		&user.Role, &user.IsActive, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (d *PostgresDatabase) GetUserByID(id int64) (*User, error) {
	user := &User{}
	err := d.db.QueryRow(`
		SELECT id, username, display_name, api_key, role, is_active, created_at, updated_at 
		FROM users WHERE id = $1`, id).Scan(
		&user.ID, &user.Username, &user.DisplayName, &user.APIKey,
		&user.Role, &user.IsActive, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (d *PostgresDatabase) GetUserByUsername(username string) (*User, error) {
	user := &User{}
	err := d.db.QueryRow(`
		SELECT id, username, display_name, api_key, role, is_active, created_at, updated_at 
		FROM users WHERE username = $1`, username).Scan(
		&user.ID, &user.Username, &user.DisplayName, &user.APIKey,
		&user.Role, &user.IsActive, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (d *PostgresDatabase) CreateUser(user *User) error {
	// Set default values if not provided
	if user.Role == "" {
		user.Role = "user"
	}
	if !user.IsActive {
		user.IsActive = true
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
		SELECT id, username, display_name, api_key, role, is_active, created_at, updated_at
		FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		user := &User{}
		err := rows.Scan(&user.ID, &user.Username, &user.DisplayName, &user.APIKey,
			&user.Role, &user.IsActive, &user.CreatedAt, &user.UpdatedAt)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, nil
}

// Game methods
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

// Upload methods
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

// Build methods
func (d *PostgresDatabase) GetBuildByID(id int64) (*Build, error) {
	build := &Build{}
	var parentBuildID sql.NullInt64
	err := d.db.QueryRow(`
		SELECT id, upload_id, channel_name, parent_build_id, user_version, state, created_at, updated_at
		FROM builds WHERE id = $1`, id).Scan(
		&build.ID, &build.UploadID, &build.ChannelName, &parentBuildID, &build.UserVersion,
		&build.State, &build.CreatedAt, &build.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if parentBuildID.Valid {
		build.ParentBuildID = &parentBuildID.Int64
	}
	return build, nil
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
		SELECT id, upload_id, channel_name, parent_build_id, user_version, state, created_at, updated_at
		FROM builds WHERE upload_id = $1 ORDER BY id DESC`, uploadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var builds []*Build
	for rows.Next() {
		build := &Build{}
		var parentBuildID sql.NullInt64
		err := rows.Scan(&build.ID, &build.UploadID, &build.ChannelName, &parentBuildID, &build.UserVersion,
			&build.State, &build.CreatedAt, &build.UpdatedAt)
		if err != nil {
			return nil, err
		}
		if parentBuildID.Valid {
			build.ParentBuildID = &parentBuildID.Int64
		}
		builds = append(builds, build)
	}
	return builds, nil
}

func (d *PostgresDatabase) GetLatestCompletedBuildByGameChannelVersion(gameID int64, channel string, userVersion string) (*Build, error) {
	build := &Build{}
	var parentBuildID sql.NullInt64
	err := d.db.QueryRow(`
		SELECT b.id, b.upload_id, b.channel_name, b.parent_build_id, b.user_version, b.state, b.created_at, b.updated_at
		FROM builds b
		JOIN uploads u ON u.id = b.upload_id
		WHERE u.game_id = $1
			AND b.channel_name = $2
			AND b.user_version = $3
			AND b.state = 'completed'
		ORDER BY b.id DESC
		LIMIT 1`, gameID, channel, userVersion).Scan(
		&build.ID, &build.UploadID, &build.ChannelName, &parentBuildID, &build.UserVersion,
		&build.State, &build.CreatedAt, &build.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if parentBuildID.Valid {
		build.ParentBuildID = &parentBuildID.Int64
	}
	return build, nil
}

// BuildFile methods
func (d *PostgresDatabase) GetBuildFilesByBuildID(buildID int64) ([]*BuildFile, error) {
	rows, err := d.db.Query(`
		SELECT id, build_id, type, sub_type, state, storage_path, upload_url, size, created_at, updated_at
		FROM build_files WHERE build_id = $1`, buildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []*BuildFile
	for rows.Next() {
		file := &BuildFile{}
		err := rows.Scan(&file.ID, &file.BuildID, &file.Type, &file.SubType,
			&file.State, &file.StoragePath, &file.UploadURL, &file.Size, &file.CreatedAt, &file.UpdatedAt)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func (d *PostgresDatabase) GetBuildFileByID(id int64) (*BuildFile, error) {
	file := &BuildFile{}
	err := d.db.QueryRow(`
		SELECT id, build_id, type, sub_type, state, storage_path, upload_url, size, created_at, updated_at
		FROM build_files WHERE id = $1`, id).Scan(
		&file.ID, &file.BuildID, &file.Type, &file.SubType,
		&file.State, &file.StoragePath, &file.UploadURL, &file.Size, &file.CreatedAt, &file.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func (d *PostgresDatabase) CreateBuildFile(file *BuildFile) error {
	err := d.db.QueryRow(`
		INSERT INTO build_files (build_id, type, sub_type, state, storage_path, upload_url, size)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, created_at, updated_at`,
		file.BuildID, file.Type, file.SubType, file.State, file.StoragePath, file.UploadURL, file.Size).Scan(
		&file.ID, &file.CreatedAt, &file.UpdatedAt)
	return err
}

func (d *PostgresDatabase) UpdateBuildFile(file *BuildFile) error {
	_, err := d.db.Exec(`
		UPDATE build_files SET build_id = $1, type = $2, sub_type = $3, state = $4, storage_path = $5, upload_url = $6, size = $7, updated_at = CURRENT_TIMESTAMP
		WHERE id = $8`,
		file.BuildID, file.Type, file.SubType, file.State, file.StoragePath, file.UploadURL, file.Size, file.ID)
	return err
}

// Channel methods
func (d *PostgresDatabase) GetChannelsByUploadID(uploadID int64) ([]*Channel, error) {
	rows, err := d.db.Query(`
		SELECT id, upload_id, name, current_build_id, created_at, updated_at
		FROM channels WHERE upload_id = $1`, uploadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []*Channel
	for rows.Next() {
		channel := &Channel{}
		var buildID sql.NullInt64
		err := rows.Scan(&channel.ID, &channel.UploadID, &channel.Name, &buildID,
			&channel.CreatedAt, &channel.UpdatedAt)
		if err != nil {
			return nil, err
		}
		if buildID.Valid {
			channel.CurrentBuildID = &buildID.Int64
		}
		channels = append(channels, channel)
	}
	return channels, nil
}

func (d *PostgresDatabase) GetChannelByName(name string, uploadID int64) (*Channel, error) {
	channel := &Channel{}
	var buildID sql.NullInt64
	err := d.db.QueryRow(`
		SELECT id, upload_id, name, current_build_id, created_at, updated_at
		FROM channels WHERE name = $1 AND upload_id = $2`, name, uploadID).Scan(
		&channel.ID, &channel.UploadID, &channel.Name, &buildID,
		&channel.CreatedAt, &channel.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if buildID.Valid {
		channel.CurrentBuildID = &buildID.Int64
	}
	return channel, nil
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
