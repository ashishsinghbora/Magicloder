import React, { useState } from 'react';
import { CreateUploadPayload } from '../types/api';

interface AddUploadModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSubmit: (payload: CreateUploadPayload) => Promise<void>;
}

export const AddUploadModal: React.FC<AddUploadModalProps> = ({
  isOpen,
  onClose,
  onSubmit,
}) => {
  const [sourcePath, setSourcePath] = useState('');
  const [tusEndpoint, setTusEndpoint] = useState('');
  const [metadata, setMetadata] = useState('');
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  if (!isOpen) return null;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!sourcePath.trim() || !tusEndpoint.trim()) {
      setError('Source path and Tus endpoint are required');
      return;
    }

    try {
      setSubmitting(true);
      setError('');
      await onSubmit({
        source_path: sourcePath.trim(),
        tus_endpoint: tusEndpoint.trim(),
        metadata: metadata.trim() || undefined,
      });
      setSourcePath('');
      setTusEndpoint('');
      setMetadata('');
      onClose();
    } catch (err: any) {
      setError(err.message || 'Failed to create upload');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="modal-overlay" role="dialog" aria-modal="true">
      <div className="modal-content">
        <div className="modal-header">
          <h2>New Tus Upload</h2>
          <button className="btn-close" onClick={onClose} aria-label="Close">✕</button>
        </div>

        {error && <div className="card-error mb-4">{error}</div>}

        <form onSubmit={handleSubmit}>
          <div className="form-group">
            <label htmlFor="source-input">Source File Path *</label>
            <input
              id="source-input"
              type="text"
              className="form-control"
              placeholder="/absolute/path/to/local/file.mp4"
              value={sourcePath}
              onChange={(e) => setSourcePath(e.target.value)}
              required
              autoFocus
            />
          </div>

          <div className="form-group">
            <label htmlFor="endpoint-input">Tus Server Endpoint *</label>
            <input
              id="endpoint-input"
              type="url"
              className="form-control"
              placeholder="https://upload.example.com/files"
              value={tusEndpoint}
              onChange={(e) => setTusEndpoint(e.target.value)}
              required
            />
          </div>

          <div className="form-group">
            <label htmlFor="meta-input">Metadata (Optional)</label>
            <input
              id="meta-input"
              type="text"
              className="form-control"
              placeholder="filename file.mp4,filetype video/mp4"
              value={metadata}
              onChange={(e) => setMetadata(e.target.value)}
            />
          </div>

          <div className="modal-footer">
            <button type="button" className="btn-secondary" onClick={onClose}>
              Cancel
            </button>
            <button type="submit" className="btn-primary" disabled={submitting}>
              {submitting ? 'Starting...' : 'Upload File'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
};
