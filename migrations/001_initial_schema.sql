-- Magicloder Initial SQLite Schema (Version 1)

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
