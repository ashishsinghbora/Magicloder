import React from 'react';
import { Download } from '../types/api';
import { formatBytes, formatETA, formatSpeed } from '../utils/formatters';

interface JobCardProps {
  download: Download;
  onPause: (id: string) => void;
  onResume: (id: string) => void;
  onCancel: (id: string) => void;
  onRetry: (id: string) => void;
  onDelete: (id: string) => void;
}

export const JobCard: React.FC<JobCardProps> = ({
  download,
  onPause,
  onResume,
  onCancel,
  onRetry,
  onDelete,
}) => {
  const percent = download.total_size > 0
    ? Math.min(100, Math.round((download.completed_bytes / download.total_size) * 100))
    : 0;

  const filename = download.destination.split(/[\\/]/).pop() || download.url;

  const getStatusBadgeClass = (status: string) => {
    switch (status) {
      case 'downloading':
        return 'badge-blue';
      case 'completed':
        return 'badge-green';
      case 'paused':
        return 'badge-yellow';
      case 'failed':
        return 'badge-red';
      case 'cancelled':
        return 'badge-gray';
      default:
        return 'badge-purple';
    }
  };

  return (
    <div className="card">
      <div className="card-header">
        <div className="card-title-group">
          <span className="file-name" title={download.destination}>{filename}</span>
          <span className={`status-badge ${getStatusBadgeClass(download.status)}`}>
            {download.status}
          </span>
        </div>
        <div className="card-actions">
          {download.status === 'downloading' && (
            <button className="btn-action" onClick={() => onPause(download.id)} title="Pause">
              ⏸ Pause
            </button>
          )}
          {download.status === 'paused' && (
            <button className="btn-action" onClick={() => onResume(download.id)} title="Resume">
              ▶ Resume
            </button>
          )}
          {(download.status === 'failed' || download.status === 'cancelled') && (
            <button className="btn-action" onClick={() => onRetry(download.id)} title="Retry">
              🔄 Retry
            </button>
          )}
          {(download.status === 'downloading' || download.status === 'queued' || download.status === 'probing') && (
            <button className="btn-action btn-danger" onClick={() => onCancel(download.id)} title="Cancel">
              ⏹ Cancel
            </button>
          )}
          <button className="btn-action btn-danger" onClick={() => onDelete(download.id)} title="Remove">
            🗑 Delete
          </button>
        </div>
      </div>

      <div className="progress-container">
        <div className="progress-bar">
          <div
            className={`progress-fill ${download.status === 'completed' ? 'fill-green' : 'fill-blue'}`}
            style={{ width: `${percent}%` }}
          />
        </div>
        <span className="progress-percent">{percent}%</span>
      </div>

      <div className="card-details">
        <div className="detail-item">
          <span className="detail-label">Progress:</span>
          <span>
            {formatBytes(download.completed_bytes)} / {formatBytes(download.total_size)}
          </span>
        </div>
        {download.status === 'downloading' && (
          <>
            <div className="detail-item">
              <span className="detail-label">Speed:</span>
              <span className="speed-text">{formatSpeed(download.speed_bytes_per_sec || 0)}</span>
            </div>
            <div className="detail-item">
              <span className="detail-label">ETA:</span>
              <span>{formatETA(download.eta_seconds || 0)}</span>
            </div>
          </>
        )}
        <div className="detail-item">
          <span className="detail-label">Connections:</span>
          <span>{download.max_connections}</span>
        </div>
      </div>

      {download.error_msg && (
        <div className="card-error" role="alert">
          <strong>Error:</strong> {download.error_msg}
        </div>
      )}
    </div>
  );
};
