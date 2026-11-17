'use client';

// /license — status and install/replace. The pasted document is never shown
// again after a successful install, and a rejected one leaves the previous
// license in place (the status below is only replaced by a 200 response).

import { useState } from 'react';
import { mutate } from 'swr';
import { useTranslations } from 'next-intl';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Spinner } from '@/components/ui/Spinner';
import { installLicense, licenseRejectReason, describeApiError } from '@/lib/product-api';
import { useLicense } from './hooks';
import { LicenseCard, LicenseExpiryWarning } from './LicenseParts';
import { Notice, PageHeader, inputStyle, labelStyle } from './shared';

export function LicenseScreen() {
  const t = useTranslations('Product');
  const { data, error, isLoading } = useLicense();
  const [doc, setDoc] = useState('');
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ kind: 'ok' } | { kind: 'rejected'; reason: string } | { kind: 'error'; text: string } | null>(null);

  async function install(e: React.FormEvent) {
    e.preventDefault();
    if (!doc.trim() || busy) return;
    setBusy(true);
    setResult(null);
    try {
      const status = await installLicense(doc.trim());
      setDoc(''); // the document is not kept once installed
      await mutate('product:license', status, { revalidate: false });
      await mutate('product:status');
      setResult({ kind: 'ok' });
    } catch (err) {
      const reason = licenseRejectReason(err);
      setResult(
        reason !== 'unknown'
          ? { kind: 'rejected', reason }
          : { kind: 'error', text: describeApiError(err, t('lic_install_error')) },
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="content-pad" style={{ maxWidth: 900, margin: '0 auto' }}>
      <PageHeader eyebrow={t('lic_eyebrow')} title={t('lic_title')} />
      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        <LicenseExpiryWarning status={data} />
        <Panel>
          <PanelHeader title={t('lic_status_title')} />
          <PanelBody>
            {isLoading && !data && <div className="mono" style={{ fontSize: 12, color: 'var(--tx-faint)' }}>{t('loading')}</div>}
            {error && !data && <Notice tone="error">{describeApiError(error, t('unavailable'))}</Notice>}
            {data && <LicenseCard status={data} />}
          </PanelBody>
        </Panel>
        <Panel>
          <PanelHeader title={t('lic_install_title')} sub={t('lic_install_sub')} />
          <PanelBody>
            <form onSubmit={install} style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              <label>
                <span className="mono" style={labelStyle}>{t('lic_document')}</span>
                <textarea
                  value={doc}
                  onChange={(e) => setDoc(e.target.value)}
                  rows={6}
                  spellCheck={false}
                  autoComplete="off"
                  style={{ ...inputStyle, resize: 'vertical' }}
                />
              </label>
              <button
                type="submit"
                className="btn sm primary"
                disabled={busy || !doc.trim()}
                style={{ alignSelf: 'flex-start', fontSize: 12 }}
              >
                {busy ? <Spinner size={12} color="#fff" /> : t('lic_install')}
              </button>
              {result?.kind === 'ok' && <Notice tone="ok">{t('lic_installed')}</Notice>}
              {result?.kind === 'rejected' && (
                <Notice tone="error" title={t('lic_rejected')}>
                  {t(`lic_reject_${result.reason}`)} {t('lic_kept')}
                </Notice>
              )}
              {result?.kind === 'error' && <Notice tone="error">{result.text}</Notice>}
            </form>
          </PanelBody>
        </Panel>
      </div>
    </div>
  );
}
