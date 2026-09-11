export type DownloadStatus =
  | 'queued'
  | 'probing'
  | 'downloading'
  | 'paused'
  | 'completed'
  | 'failed'
  | 'cancelled';

export type UploadStatus =
  | 'queued'
  | 'uploading'
  | 'paused'
  | 'completed'
  | 'failed'
  | 'cancelled';

export interface Download {
  id: string;
  url: string;
  destination: string;
  temporary_path: string;
  total_size: number;
  completed_bytes: number;
  status: DownloadStatus;
  etag?: string;
  last_modified?: string;
  content_type?: string;
  supports_range: boolean;
  max_connections: number;
  priority: number;
  error_msg?: string;
  expected_hash?: string;
  created_at: string;
  updated_at: string;
  // Dynamic client-side metrics calculated via WebSocket
  speed_bytes_per_sec?: number;
  eta_seconds?: number;
}

export interface Upload {
  id: string;
  source_path: string;
  tus_endpoint: string;
  upload_url?: string;
  total_size: number;
  uploaded_bytes: number;
  status: UploadStatus;
  file_fingerprint: string;
  metadata?: string;
  error_msg?: string;
  created_at: string;
  updated_at: string;
}

export interface WSEvent {
  type: string;
  timestamp: string;
  download_id?: string;
  url?: string;
  destination?: string;
  status?: string;
  completed_bytes: number;
  total_size: number;
  speed_bytes_per_sec: number;
  eta_seconds: number;
  active_connections: number;
  error_msg?: string;
}

export interface Config {
  data_dir: string;
  download_dir: string;
  bind_addr: string;
  remote_enabled: boolean;
  max_concurrent_downloads: number;
  default_workers_per_download: number;
}

export interface CreateDownloadPayload {
  url: string;
  filename?: string;
  destination_dir?: string;
  max_connections?: number;
  priority?: number;
  expected_hash?: string;
}

export interface CreateUploadPayload {
  source_path: string;
  tus_endpoint: string;
  metadata?: string;
}
