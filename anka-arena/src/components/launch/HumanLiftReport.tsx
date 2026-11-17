'use client';

import { Fragment } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Badge } from '@/components/ui/Badge';
import { getGate, getHumanLift, verdictTone, type ChannelStats } from '@/lib/launch-api';
import { Empty, fmtNum, fmtPct, fmtTime, fmtUsd, pnlColor, tCat, tdRight, tdStyle, thRight, thStyle, theadRow } from '@/components/launch/shared';

function StatsCells({ s }: { s: ChannelStats | undefined }) {
  return (
    <>
      <td style={tdRight} className="mono">{s?.trades ?? 0}</td>
      <td style={{ ...tdRight, color: pnlColor(s?.net_pnl_usd) }} className="mono">{fmtUsd(s?.net_pnl_usd)}</td>
      <td style={tdRight} className="mono">{fmtPct(s?.win_rate)}</td>
      <td style={tdRight} className="mono">{fmtNum(s?.profit_factor)}</td>
    </>
  );
}

export function HumanLiftReport() {
  const t = useTranslations('Launch');
  const { data, error, isLoading } = useSWR('launch:human-lift', getHumanLift, { revalidateOnFocus: false, refreshInterval: 30_000 });
  const { data: gate } = useSWR('launch:gate', () => getGate(20), { revalidateOnFocus: false, refreshInterval: 30_000 });
  const head = (
    <tr style={theadRow}>
      <th style={thStyle} />
      <th style={thRight}>{t('col_trades')}</th><th style={thRight}>{t('col_net')}</th>
      <th style={thRight}>{t('col_win_rate')}</th><th style={thRight}>{t('col_pf')}</th>
    </tr>
  );
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr)', gap: 16 }}>
      <Panel>
        <PanelHeader title={t('lift_title')} sub={t('lift_sub')}>
          {data && <Badge variant={data.status === 'OK' ? 'ok' : 'micro'}>{data.status === 'OK' ? 'OK' : t('verdict_INSUFFICIENT_DATA')}</Badge>}
        </PanelHeader>
        <PanelBody>
          {error ? <Empty text={t('load_error')} /> : isLoading || !data ? <Empty text={t('loading')} /> : (
            <>
              {data.status === 'INSUFFICIENT_DATA' && (
                <div style={{ fontSize: 12, color: 'var(--warn)', marginBottom: 10 }}>{t('lift_insufficient', { n: data.min_trades })}</div>
              )}
              <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
                <thead>{head}</thead>
                <tbody>
                  {(['manual', 'counterfactual', 'auto'] as const).map((ch) => (
                    <tr key={ch}><td style={tdStyle}><b>{t(`channel_${ch}` as never)}</b></td><StatsCells s={data.channels?.[ch]} /></tr>
                  ))}
                </tbody>
              </table>
              <div style={{ marginTop: 10, fontSize: 12, color: 'var(--tx-dim)' }}>
                {t('lift_diff')}:{' '}
                <b className="mono" style={{ color: pnlColor(data.manual_minus_counterfactual_net_usd) }}>
                  {fmtUsd(data.manual_minus_counterfactual_net_usd)}
                </b>
              </div>
              <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', margin: '14px 0 4px' }}>{t('by_category')}</div>
              {(data.by_category ?? []).length === 0 ? <Empty text={t('empty')} /> : (
                <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
                  <thead>{head}</thead>
                  <tbody>
                    {data.by_category.map((c) => (
                      <Fragment key={c.category}>
                        <tr><td style={tdStyle}><b>{tCat(t, c.category)}</b> · {t('channel_manual')}</td><StatsCells s={c.manual} /></tr>
                        <tr><td style={{ ...tdStyle, color: 'var(--tx-dim)' }}>&nbsp;&nbsp;{t('channel_counterfactual')}</td><StatsCells s={c.counterfactual} /></tr>
                      </Fragment>
                    ))}
                  </tbody>
                </table>
              )}
            </>
          )}
        </PanelBody>
      </Panel>

      <Panel>
        <PanelHeader title={t('gate_history')} />
        <PanelBody>
          {!gate || gate.history.length === 0 ? <Empty text={t('empty')} /> : (
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
              <thead>
                <tr style={theadRow}>
                  <th style={thStyle}>{t('col_evaluated')}</th><th style={thStyle}>{t('col_verdict')}</th>
                  <th style={thRight}>{t('col_trades')}</th><th style={thRight}>{t('col_net')}</th><th style={thRight}>{t('col_pf')}</th>
                </tr>
              </thead>
              <tbody>
                {gate.history.map((g) => (
                  <tr key={g.id}>
                    <td style={tdStyle}>{fmtTime(g.evaluated_at)}</td>
                    <td style={tdStyle}><Badge variant={verdictTone(g.verdict)}>{t(`verdict_${g.verdict}` as never)}</Badge></td>
                    <td style={tdRight} className="mono">{g.trades}</td>
                    <td style={{ ...tdRight, color: pnlColor(g.net_pnl_usd) }} className="mono">{fmtUsd(g.net_pnl_usd)}</td>
                    <td style={tdRight} className="mono">{fmtNum(g.profit_factor)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </PanelBody>
      </Panel>
    </div>
  );
}
