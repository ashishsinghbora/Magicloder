import { WSEvent } from '../types/api';

export type ConnectionState = 'connecting' | 'connected' | 'disconnected' | 'reconnecting';

export interface WebSocketClientOptions {
  url?: string;
  initialBackoffMs?: number;
  maxBackoffMs?: number;
  onStateChange?: (state: ConnectionState) => void;
  onEvent?: (event: WSEvent) => void;
  onReconnectSync?: () => Promise<void>;
}

export class MagicloderWebSocket {
  private ws: WebSocket | null = null;
  private url: string;
  private initialBackoffMs: number;
  private maxBackoffMs: number;
  private currentBackoffMs: number;
  private state: ConnectionState = 'disconnected';
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private explicitlyClosed = false;

  private isReconnecting = false;

  private onStateChange?: (state: ConnectionState) => void;
  private onEvent?: (event: WSEvent) => void;
  private onReconnectSync?: () => Promise<void>;

  // Deduplication cache: keeps record of (download_id + type + completed_bytes + timestamp)
  private seenEvents = new Set<string>();

  constructor(options: WebSocketClientOptions = {}) {
    this.url = options.url || 'ws://127.0.0.1:8321/api/v1/events';
    this.initialBackoffMs = options.initialBackoffMs || 1000;
    this.maxBackoffMs = options.maxBackoffMs || 10000;
    this.currentBackoffMs = this.initialBackoffMs;

    this.onStateChange = options.onStateChange;
    this.onEvent = options.onEvent;
    this.onReconnectSync = options.onReconnectSync;
  }

  public getState(): ConnectionState {
    return this.state;
  }

  public connect(): void {
    if (this.state === 'connected' || this.state === 'connecting') {
      return;
    }

    this.explicitlyClosed = false;
    this.setState('connecting');

    try {
      this.ws = new WebSocket(this.url);

      this.ws.onopen = async () => {
        const wasReconnecting = this.isReconnecting;
        this.isReconnecting = false;
        this.setState('connected');
        this.currentBackoffMs = this.initialBackoffMs;

        // Perform REST state synchronization after reconnection
        if (wasReconnecting && this.onReconnectSync) {
          try {
            await this.onReconnectSync();
          } catch {
            // Reconnect sync failed, will retry next tick
          }
        }
      };

      this.ws.onmessage = (event) => {
        try {
          const parsed: WSEvent = JSON.parse(event.data);
          if (this.isDuplicate(parsed)) {
            return;
          }
          if (this.onEvent) {
            this.onEvent(parsed);
          }
        } catch {
          // Ignore malformed payloads
        }
      };

      this.ws.onerror = () => {
        // Handled in onclose
      };

      this.ws.onclose = () => {
        this.ws = null;
        if (!this.explicitlyClosed) {
          this.scheduleReconnect();
        } else {
          this.setState('disconnected');
        }
      };
    } catch {
      this.scheduleReconnect();
    }
  }

  public disconnect(): void {
    this.explicitlyClosed = true;
    this.isReconnecting = false;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
    this.setState('disconnected');
  }

  private scheduleReconnect(): void {
    this.isReconnecting = true;
    this.setState('reconnecting');
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
    }

    this.reconnectTimer = setTimeout(() => {
      this.currentBackoffMs = Math.min(this.currentBackoffMs * 1.5, this.maxBackoffMs);
      this.connect();
    }, this.currentBackoffMs);
  }

  private setState(newState: ConnectionState): void {
    if (this.state !== newState) {
      this.state = newState;
      if (this.onStateChange) {
        this.onStateChange(newState);
      }
    }
  }

  private isDuplicate(event: WSEvent): boolean {
    const key = `${event.download_id || 'sys'}:${event.type}:${event.completed_bytes}:${event.status}:${event.timestamp}`;
    if (this.seenEvents.has(key)) {
      return true;
    }

    this.seenEvents.add(key);
    // Bound memory size of deduplication cache
    if (this.seenEvents.size > 1000) {
      const iter = this.seenEvents.values();
      for (let i = 0; i < 200; i++) {
        const item = iter.next().value;
        if (item) this.seenEvents.delete(item);
      }
    }
    return false;
  }
}
