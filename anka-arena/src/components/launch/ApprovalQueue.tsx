'use client';

import { useState } from 'react';
import useSWR, { useSWRConfig } from 'swr';
import { useTranslations } from 'next-intl';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Badge } from '@/components/ui/Badge';
import {
  DECISION_CATEGORIES, canDecide, canOperate, decideOpportunity, launchErrorInfo, launchErrorKey,
  listApprovals, shouldRefreshQueue, sortByExpiry, timeRemaining,
  type DecisionAction, type DecisionCategory, type Opportunity,
} from '@/lib/launch-api';
import { Empty, btnStyle, fmtNum, inputStyle, useNow, useOperatePermission } from '@/components/launch/shared';
import { OpportunityDetail } from '@/components/launch/OpportunitiesTable';

interface Draft {
  oppId: number;
  action: DecisionAction;
  category: DecisionCategory | '';
  comment: string;
  confirmed: boolean;
}

export function ApprovalQueue() {
  const t = useTranslations('Launch');
  const perm = useOperatePermission();
  const now = useNow();
  const { mutate: globalMutate } = useSWRConfig();
  const { data, error, isLoading, mutate } = useSWR('launch:approvals', listApprovals, {
    revalidateOnFocus: false,
    refreshInterval: 5_000,
  });
  const [draft, setDraft] = useState<Draft | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [expanded, setExpanded] = useState<number | null>(null);

  // Mount/permissions guard: backend flag AND user level must both allow.
  const operate = perm.allowed && canOperate(perm.level, data?.can_decide);
  const items = sortByExpiry(data?.items ?? []);
  const readOnlyReason = !perm.ready ? t('perm_loading') : t('no_permission');

  async function submit() {
    if (!draft || draft.category === '') return;
    setBusy(true);
    setMsg(null);
    try {
      const res = await decideOpportunity(draft.oppId, {
        action: draft.action,
        category: draft.category,
        ...(draft.comment.trim() ? { comment: draft.comment.trim() } : {}),
      });
      setMsg({ ok: true, text: t('decided_ok', { action: t(`action_${res.action === 'approve' ? 'approve' : 'reject'}` as never), id: res.opportunity_id }) });
      setDraft(null);
      await mutate();
      // The approved item becomes a shadow position: refresh the positions tab.
      void globalMutate((k) => typeof k === 'string' && k.startsWith('launch:positions'));
    } catch (e) {
      const info = launchErrorInfo(e);
      setMsg({ ok: false, text: t(launchErrorKey(info) as never) });
      if (shouldRefreshQueue(info)) {
        setDraft(null);
        await mutate();
      }
    } finally {
      setBusy(false);
    }
  }

  function row(o: Opportunity) {
    const tr = timeRemaining(o.expires_at, now);
    const allowed = operate && canDecide(o, true, now || Date.now());
    const d = draft?.oppId === o.id ? draft : null;
    const sendOk = d != null && d.category !== '' && d.confirmed && !busy;
    return (
      <div key={o.id} style={{ border: '1px solid var(--line)', borderRadius: 'var(--r)', padding: 12, display: 'grid', gap: 10 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
          <b style={{ fontSize: 14 }}>{o.symbol ?? o.mint.slice(0, 8)}</b>
          <span className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{o.dex ?? ''} #{o.id}</span>
          <Badge variant="brand">{t('col_score')} {o.score == null ? '—' : fmtNum(o.score, 1)}</Badge>
          <span style={{ flex: 1 }} />
          <span className="mono" suppressHydrationWarning style={{ fontSize: 12, color: tr.totalSeconds < 60 ? 'var(--down)' : 'var(--warn)' }}>
            {t('time_left')}: {now === 0 ? '—' : tr.expired ? t('expired') : tr.label}
          </span>
          <button type="button" onClick={() => setExpanded(expanded === o.id ? null : o.id)} style={{ background: 'none', border: 'none', color: 'var(--brand)', cursor: 'pointer', fontSize: 11 }}>
            {expanded === o.id ? t('collapse') : t('expand')}
          </button>
        </div>
        {expanded === o.id && <OpportunityDetail opp={o} />}
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
          <button type="button" disabled={!allowed} suppressHydrationWarning
            onClick={() => setDraft({ oppId: o.id, action: 'approve', category: '', comment: '', confirmed: false })}
            style={btnStyle('up', !allowed)}>{t('approve')}</button>
          <button type="button" disabled={!allowed} suppressHydrationWarning
            onClick={() => setDraft({ oppId: o.id, action: 'reject', category: '', comment: '', confirmed: false })}
            style={btnStyle('down', !allowed)}>{t('reject')}</button>
          {!operate && (
            <span className="mono" suppressHydrationWarning style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
              {t('appr_readonly', { reason: readOnlyReason })}
            </span>
          )}
        </div>
        {d && (
          <div style={{ padding: 12, borderRadius: 'var(--r)', border: `1px solid ${d.action === 'approve' ? 'var(--up)' : 'var(--down)'}`, display: 'grid', gap: 8 }}>
            <label style={{ fontSize: 11, color: 'var(--tx-dim)' }}>{t('category_label')}</label>
            <select value={d.category} onChange={(e) => setDraft({ ...d, category: e.target.value as DecisionCategory | '' })} style={inputStyle}>
              <option value="">{t('category_pick')}</option>
              {DECISION_CATEGORIES.map((c) => <option key={c} value={c}>{t(`cat_${c}` as never)}</option>)}
            </select>
            <label style={{ fontSize: 11, color: 'var(--tx-dim)' }}>{t('comment_label')}</label>
            <input value={d.comment} onChange={(e) => setDraft({ ...d, comment: e.target.value })} style={inputStyle} maxLength={500} />
            <label style={{ fontSize: 12, display: 'flex', gap: 8, alignItems: 'center' }}>
              <input type="checkbox" checked={d.confirmed} onChange={(e) => setDraft({ ...d, confirmed: e.target.checked })} />
              {d.action === 'approve' ? t('confirm_approve') : t('confirm_reject')}
            </label>
            <div style={{ display: 'flex', gap: 8 }}>
              <button type="button" disabled={!sendOk || !allowed} onClick={submit} style={btnStyle(d.action === 'approve' ? 'up' : 'down', !sendOk || !allowed)}>
                {busy ? t('working') : t('decide_send')}
              </button>
              <button type="button" onClick={() => setDraft(null)} style={btnStyle('neutral', false)}>{t('ctrl_cancel')}</button>
            </div>
          </div>
        )}
      </div>
    );
  }

  return (
    <Panel>
      <PanelHeader title={t('appr_title')} sub={t('appr_sub')} />
      <PanelBody>
        {msg && <div style={{ marginBottom: 10, fontSize: 12, color: msg.ok ? 'var(--up)' : 'var(--down)' }}>{msg.text}</div>}
        {error ? <Empty text={t('load_error')} /> : isLoading ? <Empty text={t('loading')} /> : items.length === 0 ? <Empty text={t('appr_empty')} /> : (
          <div style={{ display: 'grid', gap: 12 }}>{items.map(row)}</div>
        )}
      </PanelBody>
    </Panel>
  );
}
