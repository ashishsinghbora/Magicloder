import React from 'react';
import { Upload } from '../types/api';
import { formatBytes } from '../utils/formatters';

interface UploadCardProps {
  upload: Upload;
  onDelete: (id: string) => void;
}

export const UploadCard: React.FC<UploadCardProps> = ({ upload, onDelete }) => {
  const percent = upload.total_size > 0
    ? Math.min(100, Math.round((upload.uploaded_bytes / upload.total_size) * 100))
    : 0;

  const filename = upload.source_path.split(/[\\/]/).pop() || upload.source_path;

  return (
    <div className="card">
      <div className="card-header">
        <div className="card-title-group">
          <span className="file-name" title={upload.source_path}>{filename}</span>
          <span className={`status-badge ${upload.status === 'completed' ? 'badge-green' : 'badge-purple'}`}>
            {upload.status}
          </span>
        </div>
        <div className="card-actions">
          <button className="btn-action btn-danger" onClick={() => onDelete(upload.id)} title="Delete Upload">
            🗑 Delete
          </button>
        </div>
      </div>

      <div className="progress-container">
        <div className="progress-bar">
          <div
            className={`progress-fill ${upload.status === 'completed' ? 'fill-green' : 'fill-purple'}`}
            style={{ width: `${percent}%` }}
          />
        </div>
        <span className="progress-percent">{percent}%</span>
      </div>

      <div className="card-details">
        <div className="detail-item">
          <span className="detail-label">Endpoint:</span>
          <span className="endpoint-text" title={upload.tus_endpoint}>{upload.tus_endpoint}</span>
        </div>
        <div className="detail-item">
          <span className="detail-label">Uploaded:</span>
          <span>{formatBytes(upload.uploaded_bytes)} / {formatBytes(upload.total_size)}</span>
        </div>
      </div>

      {upload.error_msg && (
        <div className="card-error" role="alert">
          <strong>Error:</strong> {upload.error_msg}
        </div>
      )}
    </div>
  );
};
