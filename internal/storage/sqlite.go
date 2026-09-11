package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ashishsinghbora/magicloder/internal/model"
	_ "modernc.org/sqlite"
)

var (
	ErrNotFound = errors.New("resource not found in storage")
)

// Store defines the persistence contract for Magicloder.
type Store interface {
	// Downloads
	CreateDownload(ctx context.Context, dl *model.Download) error
	GetDownload(ctx context.Context, id string) (*model.Download, error)
	ListDownloads(ctx context.Context, statusFilter model.DownloadStatus) ([]*model.Download, error)
	UpdateDownload(ctx context.Context, dl *model.Download) error
	UpdateDownloadStatus(ctx context.Context, id string, status model.DownloadStatus, errorMsg string) error
	UpdateDownloadProgress(ctx context.Context, id string, completedBytes int64) error
	DeleteDownload(ctx context.Context, id string) error
	GetUnfinishedDownloads(ctx context.Context) ([]*model.Download, error)

	// Chunks
	SaveChunks(ctx context.Context, chunks []model.Chunk) error
	GetChunks(ctx context.Context, downloadID string) ([]model.Chunk, error)
	UpdateChunkProgress(ctx context.Context, downloadID string, chunkIndex int, completedBytes int64, status model.ChunkStatus) error
	BatchUpdateChunks(ctx context.Context, downloadID string, chunks []model.Chunk) error

	// Uploads
	CreateUpload(ctx context.Context, up *model.Upload) error
	GetUpload(ctx context.Context, id string) (*model.Upload, error)
	ListUploads(ctx context.Context, statusFilter model.UploadStatus) ([]*model.Upload, error)
	UpdateUpload(ctx context.Context, up *model.Upload) error
	UpdateUploadProgress(ctx context.Context, id string, uploadedBytes int64, uploadURL string, status model.UploadStatus) error
	DeleteUpload(ctx context.Context, id string) error
	GetUnfinishedUploads(ctx context.Context) ([]*model.Upload, error)

	Close() error
}

// SQLiteStore implements Store using modernc.org/sqlite.
type SQLiteStore struct {
	db *sql.DB
	mu sync.RWMutex
}

// Open initializes or opens a SQLite database with WAL and runs migrations.
func Open(dbPath string) (*SQLiteStore, error) {
	if dbPath != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
			return nil, fmt.Errorf("failed to create db directory: %w", err)
		}
	}

	dsn := fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	store := &SQLiteStore{db: db}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	return store, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) migrate() error {
	migrationSQL := `
	CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS downloads (
		id TEXT PRIMARY KEY,
		url TEXT NOT NULL,
		destination TEXT NOT NULL,
		temporary_path TEXT NOT NULL,
		total_size INTEGER NOT NULL,
		completed_bytes INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL,
		etag TEXT,
		last_modified TEXT,
		content_type TEXT,
		supports_range INTEGER NOT NULL DEFAULT 0,
		max_connections INTEGER NOT NULL DEFAULT 4,
		priority INTEGER NOT NULL DEFAULT 0,
		error_msg TEXT,
		expected_hash TEXT,
		actual_hash TEXT,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_downloads_status ON downloads(status);

	CREATE TABLE IF NOT EXISTS chunks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		download_id TEXT NOT NULL,
		chunk_index INTEGER NOT NULL,
		start_byte INTEGER NOT NULL,
		end_byte INTEGER NOT NULL,
		completed_bytes INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL,
		retry_count INTEGER NOT NULL DEFAULT 0,
		error_msg TEXT,
		updated_at DATETIME NOT NULL,
		FOREIGN KEY(download_id) REFERENCES downloads(id) ON DELETE CASCADE,
		UNIQUE(download_id, chunk_index)
	);
	CREATE INDEX IF NOT EXISTS idx_chunks_download_id ON chunks(download_id);

	CREATE TABLE IF NOT EXISTS uploads (
		id TEXT PRIMARY KEY,
		source_path TEXT NOT NULL,
		tus_endpoint TEXT NOT NULL,
		upload_url TEXT,
		total_size INTEGER NOT NULL,
		uploaded_bytes INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL,
		file_fingerprint TEXT NOT NULL,
		metadata TEXT,
		error_msg TEXT,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_uploads_status ON uploads(status);
	`
	_, err := s.db.Exec(migrationSQL)
	return err
}

// CreateDownload persists a new download entity.
func (s *SQLiteStore) CreateDownload(ctx context.Context, dl *model.Download) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
	INSERT INTO downloads (
		id, url, destination, temporary_path, total_size, completed_bytes,
		status, etag, last_modified, content_type, supports_range, max_connections,
		priority, error_msg, expected_hash, actual_hash, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	supportsRangeInt := 0
	if dl.SupportsRange {
		supportsRangeInt = 1
	}

	_, err := s.db.ExecContext(ctx, query,
		dl.ID, dl.URL, dl.Destination, dl.TemporaryPath, dl.TotalSize, dl.CompletedBytes,
		string(dl.Status), dl.ETag, dl.LastModified, dl.ContentType, supportsRangeInt, dl.MaxConnections,
		dl.Priority, dl.ErrorMsg, dl.ExpectedHash, dl.ActualHash, dl.CreatedAt, dl.UpdatedAt,
	)
	return err
}

// GetDownload retrieves a download by ID, including its chunks.
func (s *SQLiteStore) GetDownload(ctx context.Context, id string) (*model.Download, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
	SELECT id, url, destination, temporary_path, total_size, completed_bytes,
	       status, etag, last_modified, content_type, supports_range, max_connections,
	       priority, error_msg, expected_hash, actual_hash, created_at, updated_at
	FROM downloads WHERE id = ?`

	row := s.db.QueryRowContext(ctx, query, id)

	var dl model.Download
	var statusStr string
	var supportsRangeInt int
	var etag, lastMod, contentType, errMsg, expHash, actHash sql.NullString

	err := row.Scan(
		&dl.ID, &dl.URL, &dl.Destination, &dl.TemporaryPath, &dl.TotalSize, &dl.CompletedBytes,
		&statusStr, &etag, &lastMod, &contentType, &supportsRangeInt, &dl.MaxConnections,
		&dl.Priority, &errMsg, &expHash, &actHash, &dl.CreatedAt, &dl.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	dl.Status = model.DownloadStatus(statusStr)
	dl.SupportsRange = supportsRangeInt == 1
	dl.ETag = etag.String
	dl.LastModified = lastMod.String
	dl.ContentType = contentType.String
	dl.ErrorMsg = errMsg.String
	dl.ExpectedHash = expHash.String
	dl.ActualHash = actHash.String

	// Load Chunks
	chunks, err := s.getChunksUnsafe(ctx, id)
	if err == nil {
		dl.Chunks = chunks
	}

	return &dl, nil
}

// ListDownloads lists downloads, optionally filtered by status.
func (s *SQLiteStore) ListDownloads(ctx context.Context, statusFilter model.DownloadStatus) ([]*model.Download, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var query string
	var args []any
	if statusFilter != "" {
		query = `SELECT id, url, destination, temporary_path, total_size, completed_bytes,
		                status, etag, last_modified, content_type, supports_range, max_connections,
		                priority, error_msg, expected_hash, actual_hash, created_at, updated_at
		         FROM downloads WHERE status = ? ORDER BY priority DESC, created_at ASC`
		args = append(args, string(statusFilter))
	} else {
		query = `SELECT id, url, destination, temporary_path, total_size, completed_bytes,
		                status, etag, last_modified, content_type, supports_range, max_connections,
		                priority, error_msg, expected_hash, actual_hash, created_at, updated_at
		         FROM downloads ORDER BY priority DESC, created_at ASC`
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*model.Download
	for rows.Next() {
		var dl model.Download
		var statusStr string
		var supportsRangeInt int
		var etag, lastMod, contentType, errMsg, expHash, actHash sql.NullString

		err := rows.Scan(
			&dl.ID, &dl.URL, &dl.Destination, &dl.TemporaryPath, &dl.TotalSize, &dl.CompletedBytes,
			&statusStr, &etag, &lastMod, &contentType, &supportsRangeInt, &dl.MaxConnections,
			&dl.Priority, &errMsg, &expHash, &actHash, &dl.CreatedAt, &dl.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		dl.Status = model.DownloadStatus(statusStr)
		dl.SupportsRange = supportsRangeInt == 1
		dl.ETag = etag.String
		dl.LastModified = lastMod.String
		dl.ContentType = contentType.String
		dl.ErrorMsg = errMsg.String
		dl.ExpectedHash = expHash.String
		dl.ActualHash = actHash.String
		list = append(list, &dl)
	}

	return list, nil
}

// UpdateDownload updates mutable download properties.
func (s *SQLiteStore) UpdateDownload(ctx context.Context, dl *model.Download) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
	UPDATE downloads SET
		total_size = ?, completed_bytes = ?, status = ?, etag = ?, last_modified = ?,
		content_type = ?, supports_range = ?, max_connections = ?, priority = ?,
		error_msg = ?, expected_hash = ?, actual_hash = ?, updated_at = ?
	WHERE id = ?`

	supportsRangeInt := 0
	if dl.SupportsRange {
		supportsRangeInt = 1
	}

	dl.UpdatedAt = time.Now().UTC()
	res, err := s.db.ExecContext(ctx, query,
		dl.TotalSize, dl.CompletedBytes, string(dl.Status), dl.ETag, dl.LastModified,
		dl.ContentType, supportsRangeInt, dl.MaxConnections, dl.Priority,
		dl.ErrorMsg, dl.ExpectedHash, dl.ActualHash, dl.UpdatedAt, dl.ID,
	)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateDownloadStatus updates the status and error message.
func (s *SQLiteStore) UpdateDownloadStatus(ctx context.Context, id string, status model.DownloadStatus, errorMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `UPDATE downloads SET status = ?, error_msg = ?, updated_at = ? WHERE id = ?`
	res, err := s.db.ExecContext(ctx, query, string(status), errorMsg, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateDownloadProgress updates only progress bytes efficiently.
func (s *SQLiteStore) UpdateDownloadProgress(ctx context.Context, id string, completedBytes int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `UPDATE downloads SET completed_bytes = ?, updated_at = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, completedBytes, time.Now().UTC(), id)
	return err
}

// DeleteDownload removes a download and cascades chunks.
func (s *SQLiteStore) DeleteDownload(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, `DELETE FROM downloads WHERE id = ?`, id)
	return err
}

// GetUnfinishedDownloads returns downloads in non-terminal states.
func (s *SQLiteStore) GetUnfinishedDownloads(ctx context.Context) ([]*model.Download, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
	SELECT id, url, destination, temporary_path, total_size, completed_bytes,
	       status, etag, last_modified, content_type, supports_range, max_connections,
	       priority, error_msg, expected_hash, actual_hash, created_at, updated_at
	FROM downloads
	WHERE status IN (?, ?, ?, ?)
	ORDER BY priority DESC, created_at ASC`

	rows, err := s.db.QueryContext(ctx, query,
		string(model.StatusQueued), string(model.StatusProbing),
		string(model.StatusDownloading), string(model.StatusPaused),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*model.Download
	for rows.Next() {
		var dl model.Download
		var statusStr string
		var supportsRangeInt int
		var etag, lastMod, contentType, errMsg, expHash, actHash sql.NullString

		err := rows.Scan(
			&dl.ID, &dl.URL, &dl.Destination, &dl.TemporaryPath, &dl.TotalSize, &dl.CompletedBytes,
			&statusStr, &etag, &lastMod, &contentType, &supportsRangeInt, &dl.MaxConnections,
			&dl.Priority, &errMsg, &expHash, &actHash, &dl.CreatedAt, &dl.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		dl.Status = model.DownloadStatus(statusStr)
		dl.SupportsRange = supportsRangeInt == 1
		dl.ETag = etag.String
		dl.LastModified = lastMod.String
		dl.ContentType = contentType.String
		dl.ErrorMsg = errMsg.String
		dl.ExpectedHash = expHash.String
		dl.ActualHash = actHash.String

		list = append(list, &dl)
	}
	_ = rows.Close()

	for _, dl := range list {
		chunks, _ := s.getChunksUnsafe(ctx, dl.ID)
		dl.Chunks = chunks
	}

	return list, nil
}

// SaveChunks inserts or updates a slice of chunks in a transaction.
func (s *SQLiteStore) SaveChunks(ctx context.Context, chunks []model.Chunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
	INSERT INTO chunks (
		download_id, chunk_index, start_byte, end_byte, completed_bytes,
		status, retry_count, error_msg, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(download_id, chunk_index) DO UPDATE SET
		completed_bytes = excluded.completed_bytes,
		status = excluded.status,
		retry_count = excluded.retry_count,
		error_msg = excluded.error_msg,
		updated_at = excluded.updated_at
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UTC()
	for _, c := range chunks {
		_, err := stmt.ExecContext(ctx,
			c.DownloadID, c.Index, c.StartByte, c.EndByte, c.CompletedBytes,
			string(c.Status), c.RetryCount, c.ErrorMsg, now,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetChunks returns all chunks for a download.
func (s *SQLiteStore) GetChunks(ctx context.Context, downloadID string) ([]model.Chunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getChunksUnsafe(ctx, downloadID)
}

func (s *SQLiteStore) getChunksUnsafe(ctx context.Context, downloadID string) ([]model.Chunk, error) {
	query := `
	SELECT id, download_id, chunk_index, start_byte, end_byte, completed_bytes,
	       status, retry_count, error_msg, updated_at
	FROM chunks WHERE download_id = ? ORDER BY chunk_index ASC`

	rows, err := s.db.QueryContext(ctx, query, downloadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chunks []model.Chunk
	for rows.Next() {
		var c model.Chunk
		var statusStr string
		var errMsg sql.NullString

		err := rows.Scan(
			&c.ID, &c.DownloadID, &c.Index, &c.StartByte, &c.EndByte, &c.CompletedBytes,
			&statusStr, &c.RetryCount, &errMsg, &c.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		c.Status = model.ChunkStatus(statusStr)
		c.ErrorMsg = errMsg.String
		chunks = append(chunks, c)
	}

	return chunks, nil
}

// UpdateChunkProgress updates an individual chunk's completed bytes and status.
func (s *SQLiteStore) UpdateChunkProgress(ctx context.Context, downloadID string, chunkIndex int, completedBytes int64, status model.ChunkStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `UPDATE chunks SET completed_bytes = ?, status = ?, updated_at = ? WHERE download_id = ? AND chunk_index = ?`
	_, err := s.db.ExecContext(ctx, query, completedBytes, string(status), time.Now().UTC(), downloadID, chunkIndex)
	return err
}

// BatchUpdateChunks persists progress of all chunks in one atomic transaction.
func (s *SQLiteStore) BatchUpdateChunks(ctx context.Context, downloadID string, chunks []model.Chunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
	UPDATE chunks SET completed_bytes = ?, status = ?, retry_count = ?, error_msg = ?, updated_at = ?
	WHERE download_id = ? AND chunk_index = ?
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UTC()
	var totalCompleted int64 = 0

	for _, c := range chunks {
		totalCompleted += c.CompletedBytes
		_, err := stmt.ExecContext(ctx, c.CompletedBytes, string(c.Status), c.RetryCount, c.ErrorMsg, now, downloadID, c.Index)
		if err != nil {
			return err
		}
	}

	// Also update download completed_bytes
	_, err = tx.ExecContext(ctx, `UPDATE downloads SET completed_bytes = ?, updated_at = ? WHERE id = ?`, totalCompleted, now, downloadID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// Upload methods

// CreateUpload stores a new upload job.
func (s *SQLiteStore) CreateUpload(ctx context.Context, up *model.Upload) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
	INSERT INTO uploads (
		id, source_path, tus_endpoint, upload_url, total_size, uploaded_bytes,
		status, file_fingerprint, metadata, error_msg, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.ExecContext(ctx, query,
		up.ID, up.SourcePath, up.TusEndpoint, up.UploadURL, up.TotalSize, up.UploadedBytes,
		string(up.Status), up.FileFingerprint, up.Metadata, up.ErrorMsg, up.CreatedAt, up.UpdatedAt,
	)
	return err
}

// GetUpload retrieves an upload by ID.
func (s *SQLiteStore) GetUpload(ctx context.Context, id string) (*model.Upload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
	SELECT id, source_path, tus_endpoint, upload_url, total_size, uploaded_bytes,
	       status, file_fingerprint, metadata, error_msg, created_at, updated_at
	FROM uploads WHERE id = ?`

	row := s.db.QueryRowContext(ctx, query, id)

	var up model.Upload
	var statusStr string
	var uploadURL, meta, errMsg sql.NullString

	err := row.Scan(
		&up.ID, &up.SourcePath, &up.TusEndpoint, &uploadURL, &up.TotalSize, &up.UploadedBytes,
		&statusStr, &up.FileFingerprint, &meta, &errMsg, &up.CreatedAt, &up.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	up.Status = model.UploadStatus(statusStr)
	up.UploadURL = uploadURL.String
	up.Metadata = meta.String
	up.ErrorMsg = errMsg.String

	return &up, nil
}

// ListUploads lists uploads, optionally filtered by status.
func (s *SQLiteStore) ListUploads(ctx context.Context, statusFilter model.UploadStatus) ([]*model.Upload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var query string
	var args []any
	if statusFilter != "" {
		query = `SELECT id, source_path, tus_endpoint, upload_url, total_size, uploaded_bytes,
		                status, file_fingerprint, metadata, error_msg, created_at, updated_at
		         FROM uploads WHERE status = ? ORDER BY created_at ASC`
		args = append(args, string(statusFilter))
	} else {
		query = `SELECT id, source_path, tus_endpoint, upload_url, total_size, uploaded_bytes,
		                status, file_fingerprint, metadata, error_msg, created_at, updated_at
		         FROM uploads ORDER BY created_at ASC`
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*model.Upload
	for rows.Next() {
		var up model.Upload
		var statusStr string
		var uploadURL, meta, errMsg sql.NullString

		err := rows.Scan(
			&up.ID, &up.SourcePath, &up.TusEndpoint, &uploadURL, &up.TotalSize, &up.UploadedBytes,
			&statusStr, &up.FileFingerprint, &meta, &errMsg, &up.CreatedAt, &up.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		up.Status = model.UploadStatus(statusStr)
		up.UploadURL = uploadURL.String
		up.Metadata = meta.String
		up.ErrorMsg = errMsg.String
		list = append(list, &up)
	}

	return list, nil
}

// UpdateUpload updates mutable upload fields.
func (s *SQLiteStore) UpdateUpload(ctx context.Context, up *model.Upload) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
	UPDATE uploads SET
		upload_url = ?, uploaded_bytes = ?, status = ?, error_msg = ?, updated_at = ?
	WHERE id = ?`

	up.UpdatedAt = time.Now().UTC()
	res, err := s.db.ExecContext(ctx, query, up.UploadURL, up.UploadedBytes, string(up.Status), up.ErrorMsg, up.UpdatedAt, up.ID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateUploadProgress updates upload progress and URL.
func (s *SQLiteStore) UpdateUploadProgress(ctx context.Context, id string, uploadedBytes int64, uploadURL string, status model.UploadStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `UPDATE uploads SET uploaded_bytes = ?, upload_url = ?, status = ?, updated_at = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, uploadedBytes, uploadURL, string(status), time.Now().UTC(), id)
	return err
}

// DeleteUpload removes an upload.
func (s *SQLiteStore) DeleteUpload(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, `DELETE FROM uploads WHERE id = ?`, id)
	return err
}

// GetUnfinishedUploads returns uploads in non-terminal states.
func (s *SQLiteStore) GetUnfinishedUploads(ctx context.Context) ([]*model.Upload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
	SELECT id, source_path, tus_endpoint, upload_url, total_size, uploaded_bytes,
	       status, file_fingerprint, metadata, error_msg, created_at, updated_at
	FROM uploads WHERE status IN (?, ?, ?) ORDER BY created_at ASC`

	rows, err := s.db.QueryContext(ctx, query,
		string(model.UploadQueued), string(model.UploadUploading), string(model.UploadPaused),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*model.Upload
	for rows.Next() {
		var up model.Upload
		var statusStr string
		var uploadURL, meta, errMsg sql.NullString

		err := rows.Scan(
			&up.ID, &up.SourcePath, &up.TusEndpoint, &uploadURL, &up.TotalSize, &up.UploadedBytes,
			&statusStr, &up.FileFingerprint, &meta, &errMsg, &up.CreatedAt, &up.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		up.Status = model.UploadStatus(statusStr)
		up.UploadURL = uploadURL.String
		up.Metadata = meta.String
		up.ErrorMsg = errMsg.String
		list = append(list, &up)
	}

	return list, nil
}
