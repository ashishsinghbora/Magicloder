import { describe, it, expect, vi, beforeEach } from 'vitest';
import { MagicloderWebSocket } from './websocket';
import { WSEvent } from '../types/api';

describe('MagicloderWebSocket', () => {
  let mockWebSocket: any;

  beforeEach(() => {
    mockWebSocket = {
      send: vi.fn(),
      close: vi.fn(),
      onopen: null,
      onclose: null,
      onmessage: null,
      onerror: null,
    };

    // Mock global WebSocket
    (globalThis as any).WebSocket = vi.fn().mockImplementation(() => mockWebSocket);
  });

  it('connects and transitions to connected state', () => {
    const onStateChange = vi.fn();
    const wsClient = new MagicloderWebSocket({ onStateChange });

    wsClient.connect();
    expect(wsClient.getState()).toBe('connecting');
    expect(onStateChange).toHaveBeenCalledWith('connecting');

    mockWebSocket.onopen();
    expect(wsClient.getState()).toBe('connected');
    expect(onStateChange).toHaveBeenCalledWith('connected');
  });

  it('deduplicates identical events', () => {
    const onEvent = vi.fn();
    const wsClient = new MagicloderWebSocket({ onEvent });
    wsClient.connect();
    mockWebSocket.onopen();

    const sampleEvent: WSEvent = {
      type: 'job_progress',
      timestamp: '2026-09-12T00:00:00Z',
      download_id: 'dl-123',
      status: 'downloading',
      completed_bytes: 1024,
      total_size: 4096,
      speed_bytes_per_sec: 500,
      eta_seconds: 6,
      active_connections: 4,
    };

    // First message received
    mockWebSocket.onmessage({ data: JSON.stringify(sampleEvent) });
    expect(onEvent).toHaveBeenCalledTimes(1);

    // Identical duplicate received
    mockWebSocket.onmessage({ data: JSON.stringify(sampleEvent) });
    expect(onEvent).toHaveBeenCalledTimes(1); // Still 1! Deduplicated!

    // Different progress received
    const newProgress = { ...sampleEvent, completed_bytes: 2048, timestamp: '2026-09-12T00:00:01Z' };
    mockWebSocket.onmessage({ data: JSON.stringify(newProgress) });
    expect(onEvent).toHaveBeenCalledTimes(2);
  });

  it('triggers onReconnectSync when reconnecting', async () => {
    const onReconnectSync = vi.fn().mockResolvedValue(undefined);
    const wsClient = new MagicloderWebSocket({ onReconnectSync, initialBackoffMs: 10 });
    wsClient.connect();
    mockWebSocket.onopen();

    // Trigger unexpected close
    mockWebSocket.onclose();
    expect(wsClient.getState()).toBe('reconnecting');

    // Fast-forward reconnect
    await new Promise((r) => setTimeout(r, 20));
    expect(wsClient.getState()).toBe('connecting');

    mockWebSocket.onopen();
    expect(wsClient.getState()).toBe('connected');
    expect(onReconnectSync).toHaveBeenCalledTimes(1);
  });

  it('safely ignores malformed events without throwing', () => {
    const onEvent = vi.fn();
    const wsClient = new MagicloderWebSocket({ onEvent });
    wsClient.connect();
    mockWebSocket.onopen();

    // Send invalid non-JSON string
    expect(() => {
      mockWebSocket.onmessage({ data: 'invalid-non-json-string{[' });
    }).not.toThrow();

    expect(onEvent).not.toHaveBeenCalled();
  });

  it('handles explicit disconnect cleanly', () => {
    const onStateChange = vi.fn();
    const wsClient = new MagicloderWebSocket({ onStateChange });
    wsClient.connect();
    mockWebSocket.onopen();
    expect(wsClient.getState()).toBe('connected');

    wsClient.disconnect();
    expect(wsClient.getState()).toBe('disconnected');
    expect(mockWebSocket.close).toHaveBeenCalledTimes(1);
  });
});
