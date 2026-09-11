import { Config, CreateDownloadPayload, CreateUploadPayload, Download, Upload } from '../types/api';

const BASE_URL = 'http://127.0.0.1:8321/api/v1';

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const url = `${BASE_URL}${path}`;
  const res = await fetch(url, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...options.headers,
    },
  });

  if (!res.ok) {
    let errMsg = `Request failed: HTTP ${res.status}`;
    try {
      const data = await res.json();
      if (data && data.error) {
        errMsg = data.error;
      }
    } catch {
      // Ignored
    }
    throw new Error(errMsg);
  }

  if (res.status === 204) {
    return {} as T;
  }

  return res.json();
}

export const api = {
  getHealth: () => request<{ status: string; uptime_seconds: number }>('/health'),
  getVersion: () => request<{ version: string; git_commit: string; build_date: string }>('/version'),
  getConfig: () => request<Config>('/config'),

  // Downloads
  listDownloads: (status?: string) =>
    request<Download[]>(`/downloads${status ? `?status=${status}` : ''}`),
  getDownload: (id: string) => request<Download>(`/downloads/${id}`),
  createDownload: (payload: CreateDownloadPayload) =>
    request<Download>('/downloads', {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  pauseDownload: (id: string) =>
    request<Download>(`/downloads/${id}/pause`, { method: 'POST' }),
  resumeDownload: (id: string) =>
    request<Download>(`/downloads/${id}/resume`, { method: 'POST' }),
  cancelDownload: (id: string) =>
    request<Download>(`/downloads/${id}/cancel`, { method: 'POST' }),
  retryDownload: (id: string) =>
    request<Download>(`/downloads/${id}/retry`, { method: 'POST' }),
  deleteDownload: (id: string, deleteFiles: boolean = false) =>
    request<void>(`/downloads/${id}?delete_files=${deleteFiles}`, { method: 'DELETE' }),

  // Uploads
  listUploads: (status?: string) =>
    request<Upload[]>(`/uploads${status ? `?status=${status}` : ''}`),
  createUpload: (payload: CreateUploadPayload) =>
    request<Upload>('/uploads', {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  deleteUpload: (id: string) => request<void>(`/uploads/${id}`, { method: 'DELETE' }),
};
