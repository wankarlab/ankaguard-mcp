'use client';

import { Fragment, useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Badge } from '@/components/ui/Badge';
import { BANDS, OPPORTUNITY_STATUSES, listOpportunities, type Opportunity } from '@/lib/launch-api';
import {
  Empty, fmtNum, fmtTime, inputStyle, tBand, tCat, tReason, tScreen, tStatus, tdRight, tdStyle, thRight, thStyle, theadRow,
} from '@/components/launch/shared';

export function OpportunityDetail({ opp }: { opp: Opportunity }) {
  const t = useTranslations('Launch');
  return (
    <div style={{ display: 'grid', gap: 12, padding: 12, background: 'var(--bg-inset)' }}>
      {opp.reject_reason && (
        <div style={{ fontSize: 12, color: 'var(--down)' }}>
          {t('reject_reason')}: {tReason(t, opp.reject_reason)}
        </div>
      )}
      <div>
        <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', marginBottom: 4 }}>{t('breakdown_title')}</div>
        {opp.breakdown.length === 0 ? <Empty text={t('empty')} /> : (
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
            <thead>
              <tr style={theadRow}>
                <th style={thStyle}>{t('col_factor')}</th><th style={thRight}>{t('col_raw')}</th>
                <th style={thRight}>{t('col_norm')}</th><th style={thRight}>{t('col_weight')}</th>
                <th style={thRight}>{t('col_confidence')}</th><th style={thRight}>{t('col_points')}</th>
              </tr>
            </thead>
            <tbody>
              {opp.breakdown.map((f) => (
                <tr key={f.factor}>
                  <td style={tdStyle}>{f.factor}</td>
                  <td style={tdRight} className="mono">{fmtNum(f.raw)}</td>
                  <td style={tdRight} className="mono">{fmtNum(f.normalized)}</td>
                  <td style={tdRight} className="mono">{fmtNum(f.weight)}</td>
                  <td style={tdRight} className="mono">{fmtNum(f.confidence)}</td>
                  <td style={tdRight} className="mono">{fmtNum(f.points, 1)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      <Thesis opp={opp} />
      {opp.decision && (
        <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>
          <b>{t('decision_title')}:</b>{' '}
          {t('decision_line', {
            action: t(`action_${opp.decision.action}` as never),
            actor: opp.decision.actor ?? '—',
            category: tCat(t, opp.decision.category),
          })}
          {opp.decision.comment ? ` — ${opp.decision.comment}` : ''}
        </div>
      )}
    </div>
  );
}

export function Thesis({ opp }: { opp: Pick<Opportunity, 'thesis'> }) {
  const t = useTranslations('Launch');
  if (!opp.thesis) return <Empty text={t('thesis_none')} />;
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(240px, 1fr))', gap: 12 }}>
      <div>
        <div className="mono" style={{ fontSize: 10, color: 'var(--up)', textTransform: 'uppercase' }}>{t('thesis_for')}</div>
        <ul style={{ margin: '4px 0', paddingLeft: 18, fontSize: 12, color: 'var(--tx-dim)' }}>
          {opp.thesis.for.map((x, i) => <li key={i}>{x}</li>)}
        </ul>
      </div>
      <div>
        <div className="mono" style={{ fontSize: 10, color: 'var(--down)', textTransform: 'uppercase' }}>{t('thesis_against')}</div>
        <ul style={{ margin: '4px 0', paddingLeft: 18, fontSize: 12, color: 'var(--tx-dim)' }}>
          {opp.thesis.against.map((x, i) => <li key={i}>{x}</li>)}
        </ul>
      </div>
    </div>
  );
}

export function OpportunitiesTable() {
  const t = useTranslations('Launch');
  const [band, setBand] = useState('');
  const [status, setStatus] = useState('');
  const [open, setOpen] = useState<number | null>(null);
  const { data, error, isLoading } = useSWR(
    `launch:opportunities:${band}:${status}`,
    () => listOpportunities({ band, status, limit: 50 }),
    { revalidateOnFocus: false, refreshInterval: 30_000 },
  );
  const items = data?.items ?? [];
  return (
    <Panel>
      <PanelHeader title={t('tab_opportunities')}>
        <div style={{ display: 'flex', gap: 8 }}>
          <select value={band} onChange={(e) => setBand(e.target.value)} style={inputStyle} aria-label={t('f_band')}>
            <option value="">{t('f_band')}: {t('f_all')}</option>
            {BANDS.map((b) => <option key={b} value={b}>{tBand(t, b)}</option>)}
          </select>
          <select value={status} onChange={(e) => setStatus(e.target.value)} style={inputStyle} aria-label={t('f_status')}>
            <option value="">{t('f_status')}: {t('f_all')}</option>
            {OPPORTUNITY_STATUSES.map((s) => <option key={s} value={s}>{tStatus(t, s)}</option>)}
          </select>
        </div>
      </PanelHeader>
      <PanelBody>
        {error ? <Empty text={t('load_error')} /> : isLoading ? <Empty text={t('loading')} /> : items.length === 0 ? <Empty text={t('empty')} /> : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
              <thead>
                <tr style={theadRow}>
                  <th style={thStyle}>{t('col_token')}</th><th style={thStyle}>{t('col_dex')}</th>
                  <th style={thStyle}>{t('col_screen')}</th><th style={thRight}>{t('col_score')}</th>
                  <th style={thStyle}>{t('col_band')}</th><th style={thStyle}>{t('col_status')}</th>
                  <th style={thStyle}>{t('col_created')}</th><th style={thStyle} />
                </tr>
              </thead>
              <tbody>
                {items.map((o) => (
                  <Fragment key={o.id}>
                    <tr>
                      <td style={tdStyle}>
                        <b>{o.symbol ?? '—'}</b>
                        <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>{o.mint.slice(0, 8)}…</div>
                      </td>
                      <td style={tdStyle}>{o.dex ?? '—'}{o.graduated ? ` · ${t('graduated')}` : ''}</td>
                      <td style={tdStyle}>
                        <Badge variant={o.screen_result === 'PASSED' ? 'ok' : o.screen_result === 'BLOCKED' ? 'neutral' : 'real'}>{tScreen(t, o.screen_result)}</Badge>
                        {o.reject_reason && <div style={{ fontSize: 10, color: 'var(--tx-dim)' }}>{tReason(t, o.reject_reason)}</div>}
                      </td>
                      <td style={tdRight} className="mono">{o.score == null ? '—' : fmtNum(o.score, 1)}</td>
                      <td style={tdStyle}>{o.band ? tBand(t, o.band) : '—'}</td>
                      <td style={tdStyle}>{tStatus(t, o.status)}</td>
                      <td style={tdStyle}>{fmtTime(o.created_at)}</td>
                      <td style={tdStyle}>
                        <button
                          type="button"
                          onClick={() => setOpen(open === o.id ? null : o.id)}
                          style={{ background: 'none', border: 'none', color: 'var(--brand)', cursor: 'pointer', fontSize: 11 }}
                        >
                          {open === o.id ? t('collapse') : t('expand')}
                        </button>
                      </td>
                    </tr>
                    {open === o.id && (
                      <tr><td colSpan={8} style={{ padding: 0 }}><OpportunityDetail opp={o} /></td></tr>
                    )}
                  </Fragment>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </PanelBody>
    </Panel>
  );
}
