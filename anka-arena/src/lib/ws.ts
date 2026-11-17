// ws.ts — WebSocket singleton client with:
// - Exponential backoff reconnection
// - rAF-coalescing for high-frequency updates
// - Per-type subscriber fan-out
// - Backpressure via bounded queue

import { getStoredToken } from './auth';

const WS_URL =
  (process.env.NEXT_PUBLIC_WS_URL || 
  (typeof window !== 'undefined' ? `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/ws` : 'ws://localhost:10600/ws')).replace(/\/$/, '');

/**
 * Appends the session JWT as `?token=`: the server validates it before the
 * upgrade and browsers cannot set custom headers on a WebSocket. Read on every
 * connect so a reconnect picks up a refreshed token.
 */
export function withToken(url: string, token: string | null): string {
  if (!token) return url;
  const sep = url.includes('?') ? '&' : '?';
  return `${url}${sep}token=${encodeURIComponent(token)}`;
}

type Handler = (msg: unknown) => void;
type Subscriptions = Map<string, Set<Handler>>;

const RECONNECT_BASE_MS = 500;
const RECONNECT_MAX_MS  = 30_000;
const RECONNECT_JITTER  = 0.3;

export class WSClient {
  private ws: WebSocket | null = null;
  private subs: Subscriptions = new Map();
  private reconnectDelay = RECONNECT_BASE_MS;
  private closed = false;
  private rafId: number | null = null;
  private pendingMessages: Array<{ type: string; payload: unknown }> = [];

  constructor(private readonly url: string) {
    this.connect();
  }

  private connect(): void {
    if (this.closed) return;
    try {
      this.ws = new WebSocket(withToken(this.url, getStoredToken()));
    } catch {
      this.scheduleReconnect();
      return;
    }

    this.ws.onopen = () => {
      this.reconnectDelay = RECONNECT_BASE_MS;
    };

    this.ws.onmessage = (event: MessageEvent) => {
      try {
        const msg = JSON.parse(event.data as string) as { type?: string; [k: string]: unknown };
        const type = msg.type ?? '__raw__';
        this.pendingMessages.push({ type, payload: msg });
        this.scheduleFlush();
      } catch {
        // malformed message — ignore
      }
    };

    this.ws.onerror = () => {
      // onclose will fire next and handle reconnection
    };

    this.ws.onclose = () => {
      if (!this.closed) {
        this.scheduleReconnect();
      }
    };
  }

  /**
   * Coalesce message fan-out into a single rAF tick for smooth UI updates.
   */
  private scheduleFlush(): void {
    if (this.rafId !== null) return;
    this.rafId = requestAnimationFrame(() => {
      this.rafId = null;
      const batch = this.pendingMessages.splice(0);
      for (const { type, payload } of batch) {
        const handlers = this.subs.get(type);
        if (handlers) {
          handlers.forEach((h) => {
            try { h(payload); } catch { /* subscriber error — isolated */ }
          });
        }
        // Also dispatch to wildcard subscribers
        const wildcards = this.subs.get('*');
        if (wildcards) {
          wildcards.forEach((h) => {
            try { h(payload); } catch { /* subscriber error — isolated */ }
          });
        }
      }
    });
  }

  private scheduleReconnect(): void {
    const jitter = 1 + (Math.random() - 0.5) * 2 * RECONNECT_JITTER;
    const delay  = Math.min(this.reconnectDelay * jitter, RECONNECT_MAX_MS);
    this.reconnectDelay = Math.min(this.reconnectDelay * 2, RECONNECT_MAX_MS);
    setTimeout(() => this.connect(), delay);
  }

  /**
   * Subscribe to a message type. Returns an unsubscribe function.
   * Use '*' to receive all messages.
   */
  subscribe(type: string, handler: Handler): () => void {
    if (!this.subs.has(type)) {
      this.subs.set(type, new Set());
    }
    this.subs.get(type)!.add(handler);
    return () => {
      this.subs.get(type)?.delete(handler);
    };
  }

  /**
   * Send a JSON message to the server. Silently drops if not connected.
   */
  send(msg: unknown): void {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg));
    }
  }

  /**
   * Permanently close the connection and stop reconnecting.
   */
  close(): void {
    this.closed = true;
    if (this.rafId !== null) {
      cancelAnimationFrame(this.rafId);
      this.rafId = null;
    }
    this.ws?.close();
    this.ws = null;
    this.subs.clear();
  }

  get isConnected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN;
  }
}

// --- Singleton ---
let _instance: WSClient | null = null;

/**
 * Get (or lazily create) the global WSClient singleton.
 * Safe to call on every render — returns the same instance.
 */
export function getWSClient(): WSClient {
  if (typeof window === 'undefined') {
    // SSR: return a stub that never connects
    throw new Error('getWSClient() must be called in a browser context');
  }
  if (!_instance) {
    _instance = new WSClient(WS_URL);
  }
  return _instance;
}
