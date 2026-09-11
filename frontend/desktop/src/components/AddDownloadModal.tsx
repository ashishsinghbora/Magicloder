import React, { useState } from 'react';
import { CreateDownloadPayload } from '../types/api';

interface AddDownloadModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSubmit: (payload: CreateDownloadPayload) => Promise<void>;
}

export const AddDownloadModal: React.FC<AddDownloadModalProps> = ({
  isOpen,
  onClose,
  onSubmit,
}) => {
  const [url, setUrl] = useState('');
  const [filename, setFilename] = useState('');
  const [destinationDir, setDestinationDir] = useState('');
  const [maxConnections, setMaxConnections] = useState(4);
  const [priority, setPriority] = useState(0);
  const [expectedHash, setExpectedHash] = useState('');
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  if (!isOpen) return null;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!url.trim()) {
      setError('URL cannot be empty');
      return;
    }

    try {
      setSubmitting(true);
      setError('');
      await onSubmit({
        url: url.trim(),
        filename: filename.trim() || undefined,
        destination_dir: destinationDir.trim() || undefined,
        max_connections: Number(maxConnections),
        priority: Number(priority),
        expected_hash: expectedHash.trim() || undefined,
      });
      setUrl('');
      setFilename('');
      setDestinationDir('');
      setExpectedHash('');
      onClose();
    } catch (err: any) {
      setError(err.message || 'Failed to create download');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="modal-overlay" role="dialog" aria-modal="true">
      <div className="modal-content">
        <div className="modal-header">
          <h2>Add Download</h2>
          <button className="btn-close" onClick={onClose} aria-label="Close">✕</button>
        </div>

        {error && <div className="card-error mb-4">{error}</div>}

        <form onSubmit={handleSubmit}>
          <div className="form-group">
            <label htmlFor="url-input">Download URL *</label>
            <input
              id="url-input"
              type="url"
              className="form-control"
              placeholder="https://example.com/file.iso"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              required
              autoFocus
            />
          </div>

          <div className="form-group">
            <label htmlFor="filename-input">Custom Filename (Optional)</label>
            <input
              id="filename-input"
              type="text"
              className="form-control"
              placeholder="my-file.iso"
              value={filename}
              onChange={(e) => setFilename(e.target.value)}
            />
          </div>

          <div className="form-group">
            <label htmlFor="dest-input">Destination Directory (Optional)</label>
            <input
              id="dest-input"
              type="text"
              className="form-control"
              placeholder="Leave blank for default ~/Downloads"
              value={destinationDir}
              onChange={(e) => setDestinationDir(e.target.value)}
            />
          </div>

          <div className="form-row">
            <div className="form-group col">
              <label htmlFor="conn-input">Connections</label>
              <input
                id="conn-input"
                type="number"
                min="1"
                max="16"
                className="form-control"
                value={maxConnections}
                onChange={(e) => setMaxConnections(Number(e.target.value))}
              />
            </div>
            <div className="form-group col">
              <label htmlFor="prio-input">Priority</label>
              <input
                id="prio-input"
                type="number"
                min="0"
                max="10"
                className="form-control"
                value={priority}
                onChange={(e) => setPriority(Number(e.target.value))}
              />
            </div>
          </div>

          <div className="form-group">
            <label htmlFor="hash-input">Expected SHA-256 Checksum (Optional)</label>
            <input
              id="hash-input"
              type="text"
              className="form-control"
              placeholder="64-character hex hash"
              value={expectedHash}
              onChange={(e) => setExpectedHash(e.target.value)}
            />
          </div>

          <div className="modal-footer">
            <button type="button" className="btn-secondary" onClick={onClose}>
              Cancel
            </button>
            <button type="submit" className="btn-primary" disabled={submitting}>
              {submitting ? 'Adding...' : 'Start Download'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
};
