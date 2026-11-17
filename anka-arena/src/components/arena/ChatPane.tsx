'use client';

/**
 * ChatPane — Minimal spectator chat sidebar.
 * Receives chat.message via WS and allows sending messages.
 */

import { useEffect, useRef, useState, useCallback, KeyboardEvent } from 'react';
import { useTranslations } from 'next-intl';
import { getWSClient } from '@/lib/ws';
import { color } from '@/design-system/tokens';

interface ChatMessage {
  id:       string;
  username: string;
  text:     string;
  ts:       number;
  isMe?:    boolean;
}

interface WSChatMessage {
  type:     'chat.message';
  id:       string;
  username: string;
  text:     string;
  ts:       number;
}

const MAX_MESSAGES = 100;

export function ChatPane({ tournamentId }: { tournamentId: string }) {
  const t = useTranslations('PageArenaDetail');
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [draft, setDraft]       = useState('');
  const bottomRef               = useRef<HTMLDivElement>(null);

  // Scroll to bottom on new messages
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages.length]);

  // WS subscription
  useEffect(() => {
    if (typeof window === 'undefined') return;
    let ws: ReturnType<typeof getWSClient>;
    try { ws = getWSClient(); } catch { return; }

    const unsub = ws.subscribe('chat.message', (raw) => {
      const msg = raw as WSChatMessage;
      setMessages((prev) => {
        const next = [...prev, {
          id:       msg.id,
          username: msg.username,
          text:     msg.text,
          ts:       msg.ts,
        }];
        return next.length > MAX_MESSAGES ? next.slice(-MAX_MESSAGES) : next;
      });
    });

    return () => unsub();
  }, []);

  const sendMessage = useCallback(() => {
    const text = draft.trim();
    if (!text) return;
    setDraft('');

    // Optimistic local message
    const localMsg: ChatMessage = {
      id:       `local-${Date.now()}`,
      username: t('chat_you'),
      text,
      ts:       Date.now(),
      isMe:     true,
    };
    setMessages((prev) => [...prev, localMsg]);

    try {
      const ws = getWSClient();
      ws.send({ type: 'chat.send', tournament_id: tournamentId, text });
    } catch { /* offline — optimistic only */ }
  }, [draft, tournamentId, t]);

  const handleKey = useCallback((e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      sendMessage();
    }
  }, [sendMessage]);

  return (
    <div style={styles.wrap}>
      <span style={styles.title}>CHAT</span>

      {/* Messages */}
      <div style={styles.feed}>
        {messages.length === 0 ? (
          <span style={styles.empty}>{t('chat_empty')}</span>
        ) : (
          messages.map((m) => (
            <div
              key={m.id}
              style={{
                ...styles.msg,
                background: m.isMe ? `${color.paper}10` : 'transparent',
              }}
            >
              <span style={{ ...styles.user, color: m.isMe ? color.paper : color.brand }}>
                {m.username}
              </span>
              <span style={styles.text}>{m.text}</span>
            </div>
          ))
        )}
        <div ref={bottomRef} />
      </div>

      {/* Input */}
      <div style={styles.inputRow}>
        <input
          style={styles.input}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={handleKey}
          placeholder={t('chat_placeholder')}
          maxLength={200}
          aria-label={t('chat_aria')}
        />
        <button
          style={styles.sendBtn}
          onClick={sendMessage}
          disabled={!draft.trim()}
          aria-label={t('chat_send')}
        >
          ↵
        </button>
      </div>
    </div>
  );
}

const styles = {
  wrap: {
    display:       'flex',
    flexDirection: 'column' as const,
    gap:           8,
    flex:          1,
    minHeight:     0,
  },
  title: {
    fontSize:      10,
    letterSpacing: '0.1em',
    textTransform: 'uppercase' as const,
    color:         color.txFaint,
    fontFamily:    'var(--mono)',
    fontWeight:    600,
  },
  feed: {
    flex:          1,
    overflowY:     'auto' as const,
    display:       'flex',
    flexDirection: 'column' as const,
    gap:           4,
    minHeight:     80,
    maxHeight:     220,
  },
  empty: {
    fontSize:   11,
    color:      color.txFaint,
    fontFamily: 'var(--sans)',
    textAlign:  'center' as const,
    padding:    '12px 0',
  },
  msg: {
    borderRadius: 5,
    padding:      '3px 6px',
    display:      'flex',
    flexDirection:'column' as const,
    gap:          1,
  },
  user: {
    fontSize:   10,
    fontFamily: 'var(--mono)',
    fontWeight: 600,
  },
  text: {
    fontSize:   12,
    color:      color.tx,
    fontFamily: 'var(--sans)',
    lineHeight: 1.4,
    wordBreak:  'break-word' as const,
  },
  inputRow: {
    display: 'flex',
    gap:     6,
  },
  input: {
    flex:         1,
    background:   color.bgInset,
    border:       `1px solid ${color.line2}`,
    borderRadius: 6,
    padding:      '6px 10px',
    fontSize:     12,
    color:        color.txHi,
    fontFamily:   'var(--sans)',
    outline:      'none',
  },
  sendBtn: {
    background:   color.bgPanel2,
    border:       `1px solid ${color.line2}`,
    borderRadius: 6,
    color:        color.brand,
    fontSize:     14,
    width:        32,
    cursor:       'pointer',
    flexShrink:   0,
  },
} as const;
