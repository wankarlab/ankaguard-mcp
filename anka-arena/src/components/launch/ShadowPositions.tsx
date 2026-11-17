'use client';

import { Fragment, useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Badge } from '@/components/ui/Badge';
import { CHANNELS, listPositions, type LaunchPosition, type PositionCosts } from '@/lib/launch-api';
import {
  Empty, fmtNum, fmtTime, fmtUsd, inputStyle, pnlColor, tExit, tdRight, tdStyle, thRight, thStyle, theadRow,
} from '@/components/launch/shared';

export function totalCosts(c: PositionCosts): number {
  return c.venue_fees_usd + c.network_fees_usd + c.rent_usd + c.slippage_usd + c.token_tax_usd;
}

function CostBreakdown({ p }: { p: LaunchPosition }) {
  const t = useTranslations('Launch');
  const c = p.costs;
  const rows: [string, string][] = [
    [t('cost_venue'), fmtUsd(c.venue_fees_usd, 4)],
    [t('cost_network'), fmtUsd(c.network_fees_usd, 4)],
    [t('cost_rent'), fmtUsd(c.rent_usd, 4)],
    [t('cost_slippage'), fmtUsd(c.slippage_usd, 4)],
    [t('cost_tax'), fmtUsd(c.token_tax_usd, 4)],
    [t('cost_dropped'), String(c.dropped_txs)],
    [t('cost_total'), fmtUsd(totalCosts(c), 4)],
  ];
  return (
    <div style={{ padding: 12, background: 'var(--bg-inset)', display: 'flex', flexWrap: 'wrap', gap: 20 }}>
      {rows.map(([k, v]) => (
        <div key={k}>
          <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase' }}>{k}</div>
          <div className="mono" style={{ fontSize: 13 }}>{v}</div>
        </div>
      ))}
      {p.size_reason && <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>{t('size_reason')}: {p.size_reason}</div>}
    </div>
  );
}

export function ShadowPositions() {
  const t = useTranslations('Launch');
  const [channel, setChannel] = useState('');
  const [open, setOpen] = useState<number | null>(null);
  const { data, error, isLoading } = useSWR(
    `launch:positions:${channel}`,
    () => listPositions({ channel, limit: 50 }),
    { revalidateOnFocus: false, refreshInterval: 5_000 },
  );
  const items = data?.items ?? [];
  return (
    <Panel>
      <PanelHeader title={t('tab_positions')} sub={t('cf_note')}>
        <select value={channel} onChange={(e) => setChannel(e.target.value)} style={inputStyle} aria-label={t('f_channel')}>
          <option value="">{t('f_channel')}: {t('f_all')}</option>
          {CHANNELS.map((c) => <option key={c} value={c}>{t(`channel_${c}` as never)}</option>)}
        </select>
      </PanelHeader>
      <PanelBody>
        {error ? <Empty text={t('load_error')} /> : isLoading ? <Empty text={t('loading')} /> : items.length === 0 ? <Empty text={t('empty')} /> : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
              <thead>
                <tr style={theadRow}>
                  <th style={thStyle}>{t('col_token')}</th><th style={thStyle}>{t('col_channel')}</th>
                  <th style={thStyle}>{t('f_state')}</th><th style={thRight}>{t('col_invested')}</th>
                  <th style={thRight}>{t('col_gross')}</th><th style={thRight}>{t('col_costs')}</th>
                  <th style={thRight}>{t('col_net')}</th><th style={thStyle}>{t('col_exit')}</th>
                  <th style={thStyle}>{t('col_opened')}</th>
                </tr>
              </thead>
              <tbody>
                {items.map((p) => (
                  <Fragment key={p.id}>
                    <tr onClick={() => setOpen(open === p.id ? null : p.id)} style={{ cursor: 'pointer' }}>
                      <td style={tdStyle}>
                        <b>{p.symbol ?? '—'}</b>{' '}
                        {p.fraud_flag && <Badge variant="real">{t('fraud_flag')}</Badge>}
                      </td>
                      <td style={tdStyle}>
                        <Badge variant={p.entry_channel === 'manual' ? 'brand' : p.entry_channel === 'counterfactual' ? 'neutral' : 'ok'}>
                          {t(`channel_${p.entry_channel}` as never)}
                        </Badge>
                      </td>
                      <td style={tdStyle}>{t(`state_${p.state}` as never)}</td>
                      <td style={tdRight} className="mono">{fmtUsd(p.invested_usd)}</td>
                      <td style={{ ...tdRight, color: pnlColor(p.gross_pnl_usd) }} className="mono">{fmtUsd(p.gross_pnl_usd)}</td>
                      <td style={tdRight} className="mono">{fmtUsd(totalCosts(p.costs), 3)}</td>
                      <td style={{ ...tdRight, color: pnlColor(p.net_pnl_usd), fontWeight: 700 }} className="mono">{fmtUsd(p.net_pnl_usd)}</td>
                      <td style={tdStyle}>{p.exit_reason ? tExit(t, p.exit_reason) : '—'}</td>
                      <td style={tdStyle}>{fmtTime(p.opened_at)}</td>
                    </tr>
                    {open === p.id && <tr><td colSpan={9} style={{ padding: 0 }}><CostBreakdown p={p} /></td></tr>}
                  </Fragment>
                ))}
              </tbody>
            </table>
            <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 6 }}>
              {t('col_trades')}: {fmtNum(items.length, 0)}
            </div>
          </div>
        )}
      </PanelBody>
    </Panel>
  );
}
