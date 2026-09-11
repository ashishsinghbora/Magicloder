import React from 'react';
import { Config } from '../types/api';

interface SettingsModalProps {
  isOpen: boolean;
  onClose: () => void;
  config: Config | null;
}

export const SettingsModal: React.FC<SettingsModalProps> = ({
  isOpen,
  onClose,
  config,
}) => {
  if (!isOpen || !config) return null;

  return (
    <div className="modal-overlay" role="dialog" aria-modal="true">
      <div className="modal-content">
        <div className="modal-header">
          <h2>Magicloder Settings</h2>
          <button className="btn-close" onClick={onClose} aria-label="Close">✕</button>
        </div>

        <div className="settings-list">
          <div className="settings-item">
            <span className="settings-label">API Bind Address:</span>
            <span className="settings-val">{config.bind_addr}</span>
          </div>
          <div className="settings-item">
            <span className="settings-label">Default Download Directory:</span>
            <span className="settings-val">{config.download_dir}</span>
          </div>
          <div className="settings-item">
            <span className="settings-label">Internal Data Directory:</span>
            <span className="settings-val">{config.data_dir}</span>
          </div>
          <div className="settings-item">
            <span className="settings-label">Max Concurrent Downloads:</span>
            <span className="settings-val">{config.max_concurrent_downloads}</span>
          </div>
          <div className="settings-item">
            <span className="settings-label">Default Connections Per Download:</span>
            <span className="settings-val">{config.default_workers_per_download}</span>
          </div>
          <div className="settings-item">
            <span className="settings-label">Remote Mode Enabled:</span>
            <span className="settings-val">{config.remote_enabled ? 'Yes' : 'No (Loopback Only)'}</span>
          </div>
        </div>

        <div className="modal-footer">
          <button type="button" className="btn-primary" onClick={onClose}>
            Close
          </button>
        </div>
      </div>
    </div>
  );
};
