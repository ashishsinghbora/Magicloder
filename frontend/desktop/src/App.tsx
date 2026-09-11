import { useState, useEffect, useCallback } from 'react';
import { Download, Upload, Config, WSEvent } from './types/api';
import { api } from './services/api';
import { MagicloderWebSocket, ConnectionState } from './services/websocket';
import { JobCard } from './components/JobCard';
import { UploadCard } from './components/UploadCard';
import { AddDownloadModal } from './components/AddDownloadModal';
import { AddUploadModal } from './components/AddUploadModal';
import { SettingsModal } from './components/SettingsModal';

type FilterTab = 'all' | 'active' | 'queued' | 'completed' | 'failed' | 'uploads';

export function App() {
  const [downloads, setDownloads] = useState<Download[]>([]);
  const [uploads, setUploads] = useState<Upload[]>([]);
  const [config, setConfig] = useState<Config | null>(null);
  const [connState, setConnState] = useState<ConnectionState>('disconnected');
  const [activeTab, setActiveTab] = useState<FilterTab>('all');
  const [errorBanner, setErrorBanner] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const [showAddDownload, setShowAddDownload] = useState(false);
  const [showAddUpload, setShowAddUpload] = useState(false);
  const [showSettings, setShowSettings] = useState(false);

  // Full state sync from REST API
  const refreshState = useCallback(async () => {
    try {
      setErrorBanner(null);
      const [dls, ups, cfg] = await Promise.all([
        api.listDownloads(),
        api.listUploads(),
        api.getConfig().catch(() => null),
      ]);
      setDownloads(dls);
      setUploads(ups);
      if (cfg) setConfig(cfg);
    } catch (err: any) {
      setErrorBanner(`Failed to communicate with Magicloder daemon: ${err.message}`);
    } finally {
      setLoading(false);
    }
  }, []);

  // Handle incoming live WebSocket event
  const handleWSEvent = useCallback((event: WSEvent) => {
    if (!event.download_id) return;

    setDownloads((prev) => {
      const idx = prev.findIndex((d) => d.id === event.download_id);
      if (idx === -1) {
        // New job not yet in list; refresh list
        refreshState();
        return prev;
      }

      const updated = [...prev];
      const target = { ...updated[idx] };

      if (event.status) {
        target.status = event.status as any;
      }
      if (event.completed_bytes !== undefined) {
        target.completed_bytes = event.completed_bytes;
      }
      if (event.total_size > 0) {
        target.total_size = event.total_size;
      }
      if (event.speed_bytes_per_sec !== undefined) {
        target.speed_bytes_per_sec = event.speed_bytes_per_sec;
      }
      if (event.eta_seconds !== undefined) {
        target.eta_seconds = event.eta_seconds;
      }
      if (event.error_msg) {
        target.error_msg = event.error_msg;
      }

      updated[idx] = target;
      return updated;
    });
  }, [refreshState]);

  // Connect WebSocket on mount
  useEffect(() => {
    refreshState();

    const ws = new MagicloderWebSocket({
      onStateChange: setConnState,
      onEvent: handleWSEvent,
      onReconnectSync: refreshState,
    });

    ws.connect();
    return () => ws.disconnect();
  }, [handleWSEvent, refreshState]);

  // Actions
  const handlePause = async (id: string) => {
    try {
      await api.pauseDownload(id);
      await refreshState();
    } catch (err: any) {
      setErrorBanner(err.message);
    }
  };

  const handleResume = async (id: string) => {
    try {
      await api.resumeDownload(id);
      await refreshState();
    } catch (err: any) {
      setErrorBanner(err.message);
    }
  };

  const handleCancel = async (id: string) => {
    try {
      await api.cancelDownload(id);
      await refreshState();
    } catch (err: any) {
      setErrorBanner(err.message);
    }
  };

  const handleRetry = async (id: string) => {
    try {
      await api.retryDownload(id);
      await refreshState();
    } catch (err: any) {
      setErrorBanner(err.message);
    }
  };

  const handleDelete = async (id: string) => {
    try {
      await api.deleteDownload(id, false);
      setDownloads((prev) => prev.filter((d) => d.id !== id));
    } catch (err: any) {
      setErrorBanner(err.message);
    }
  };

  const handleDeleteUpload = async (id: string) => {
    try {
      await api.deleteUpload(id);
      setUploads((prev) => prev.filter((u) => u.id !== id));
    } catch (err: any) {
      setErrorBanner(err.message);
    }
  };

  // Filter downloads
  const filteredDownloads = downloads.filter((d) => {
    if (activeTab === 'all') return true;
    if (activeTab === 'active') return d.status === 'downloading' || d.status === 'probing';
    if (activeTab === 'queued') return d.status === 'queued';
    if (activeTab === 'completed') return d.status === 'completed';
    if (activeTab === 'failed') return d.status === 'failed' || d.status === 'cancelled';
    return true;
  });

  return (
    <div className="app-container">
      {/* Header */}
      <header className="app-header">
        <div className="logo-group">
          <div className="logo-icon">⚡</div>
          <div>
            <h1 className="logo-text">Magicloder</h1>
            <span className="logo-sub">Transfer Engine</span>
          </div>
        </div>

        <div className="header-actions">
          <div className={`conn-indicator ${connState}`}>
            <span className="dot" />
            <span>{connState}</span>
          </div>

          <button className="btn-primary" onClick={() => setShowAddDownload(true)}>
            + Add URL
          </button>
          <button className="btn-secondary" onClick={() => setShowAddUpload(true)}>
            ↑ Upload
          </button>
          <button className="btn-icon" onClick={() => setShowSettings(true)} title="Settings">
            ⚙
          </button>
          <button className="btn-icon" onClick={refreshState} title="Refresh">
            🔄
          </button>
        </div>
      </header>

      {/* Error notification */}
      {errorBanner && (
        <div className="alert-banner" role="alert">
          <span>{errorBanner}</span>
          <button className="btn-close" onClick={() => setErrorBanner(null)}>✕</button>
        </div>
      )}

      {/* Navigation Tabs */}
      <nav className="tab-nav">
        <button
          className={`tab-btn ${activeTab === 'all' ? 'active' : ''}`}
          onClick={() => setActiveTab('all')}
        >
          All ({downloads.length})
        </button>
        <button
          className={`tab-btn ${activeTab === 'active' ? 'active' : ''}`}
          onClick={() => setActiveTab('active')}
        >
          Active ({downloads.filter((d) => d.status === 'downloading' || d.status === 'probing').length})
        </button>
        <button
          className={`tab-btn ${activeTab === 'queued' ? 'active' : ''}`}
          onClick={() => setActiveTab('queued')}
        >
          Queued ({downloads.filter((d) => d.status === 'queued').length})
        </button>
        <button
          className={`tab-btn ${activeTab === 'completed' ? 'active' : ''}`}
          onClick={() => setActiveTab('completed')}
        >
          Completed ({downloads.filter((d) => d.status === 'completed').length})
        </button>
        <button
          className={`tab-btn ${activeTab === 'failed' ? 'active' : ''}`}
          onClick={() => setActiveTab('failed')}
        >
          Failed ({downloads.filter((d) => d.status === 'failed' || d.status === 'cancelled').length})
        </button>
        <button
          className={`tab-btn ${activeTab === 'uploads' ? 'active' : ''}`}
          onClick={() => setActiveTab('uploads')}
        >
          Uploads ({uploads.length})
        </button>
      </nav>

      {/* Main Content Area */}
      <main className="content-area">
        {loading ? (
          <div className="empty-state">Loading transfers...</div>
        ) : activeTab === 'uploads' ? (
          uploads.length === 0 ? (
            <div className="empty-state">
              <div className="empty-icon">📁</div>
              <h3>No active uploads</h3>
              <p>Click "Upload" above to initiate a resumable Tus upload.</p>
            </div>
          ) : (
            <div className="cards-grid">
              {uploads.map((up) => (
                <UploadCard key={up.id} upload={up} onDelete={handleDeleteUpload} />
              ))}
            </div>
          )
        ) : filteredDownloads.length === 0 ? (
          <div className="empty-state">
            <div className="empty-icon">⬇</div>
            <h3>No downloads in this view</h3>
            <p>Click "+ Add URL" to start downloading high-performance files.</p>
          </div>
        ) : (
          <div className="cards-grid">
            {filteredDownloads.map((dl) => (
              <JobCard
                key={dl.id}
                download={dl}
                onPause={handlePause}
                onResume={handleResume}
                onCancel={handleCancel}
                onRetry={handleRetry}
                onDelete={handleDelete}
              />
            ))}
          </div>
        )}
      </main>

      {/* Modals */}
      <AddDownloadModal
        isOpen={showAddDownload}
        onClose={() => setShowAddDownload(false)}
        onSubmit={async (payload) => {
          await api.createDownload(payload);
          await refreshState();
        }}
      />

      <AddUploadModal
        isOpen={showAddUpload}
        onClose={() => setShowAddUpload(false)}
        onSubmit={async (payload) => {
          await api.createUpload(payload);
          await refreshState();
        }}
      />

      <SettingsModal
        isOpen={showSettings}
        onClose={() => setShowSettings(false)}
        config={config}
      />
    </div>
  );
}
