'use client';

import { useState } from 'react';
import { useRouter } from '@/i18n/routing';
import { useTranslations } from 'next-intl';
import { type RoleId } from '@/lib/rbac';
import { landingPath } from '@/lib/consoleMode';
import { isProductEdition } from '@/lib/productMode';
import { ProductLogin } from '@/components/product/ProductLogin';
import { Spinner } from '@/components/ui/Spinner';
import { useUIStore } from '@/stores/ui';
import {
  loginEmailResend,
  loginEmailStart,
  loginEmailVerify,
  loginGoogle,
  loginPhoneStart,
  loginPhoneVerify,
  type EmailOtpSession,
} from '@/lib/auth';

// Product edition asks the server for its auth mode first; `external` (and every
// other edition) renders the unchanged login below.
export default function LoginPage() {
  return isProductEdition() ? <ProductLogin external={<ExternalLogin />} /> : <ExternalLogin />;
}

function ExternalLogin() {
  const router = useRouter();
  const t = useTranslations('Login');
  const tf = useTranslations('LoginFlow');
  const loginFn = useUIStore((s) => s.login);

  const [isRegister, setIsRegister] = useState(false);
  const [authMethod, setAuthMethod] = useState<'options' | 'email' | 'phone'>('options');
  const [email, setEmail] = useState('');
  const [emailSession, setEmailSession] = useState<EmailOtpSession | null>(null);
  const [emailCode, setEmailCode] = useState('');
  const [notice, setNotice] = useState('');
  const [phone, setPhone] = useState('+593');
  const [otp, setOtp] = useState('');
  const [otpSent, setOtpSent] = useState(false);
  const [otpChannel, setOtpChannel] = useState<'sms' | 'whatsapp'>('whatsapp');
  const [phoneMode, setPhoneMode] = useState<'login' | 'register'>('login');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  function gotoHome(claims: { role: RoleId; kyc: number }) {
    loginFn(claims.role, claims.kyc);
    router.push(landingPath(claims.role));
  }

  // Email is passwordless (RantiPay D7): send a 6-digit code, then verify it.
  async function handleEmailStart(e: React.FormEvent) {
    e.preventDefault();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim())) {
      setError(tf('invalid_email'));
      return;
    }
    setLoading(true);
    setError('');
    setNotice('');
    try {
      setEmailSession(await loginEmailStart(email.trim()));
    } catch (err) {
      setError(err instanceof Error && err.message ? err.message : tf('email_send_error'));
    } finally {
      setLoading(false);
    }
  }

  async function handleEmailVerify(e: React.FormEvent) {
    e.preventDefault();
    if (!emailSession) return;
    if (emailCode.length !== 6) {
      setError(tf('code_6_digits'));
      return;
    }
    setLoading(true);
    setError('');
    try {
      gotoHome(await loginEmailVerify(emailSession, emailCode));
    } catch (err) {
      setError(err instanceof Error && err.message ? err.message : tf('code_invalid_expired'));
    } finally {
      setLoading(false);
    }
  }

  async function handleEmailResend() {
    if (!emailSession) return;
    setLoading(true);
    setError('');
    try {
      await loginEmailResend(emailSession);
      setNotice(tf('code_resent'));
    } catch (err) {
      setError(err instanceof Error && err.message ? err.message : tf('resend_error'));
    } finally {
      setLoading(false);
    }
  }

  function resetEmailFlow() {
    setEmailSession(null);
    setEmailCode('');
    setError('');
    setNotice('');
  }

  async function handleOAuth(provider: string) {
    setError('');
    if (provider !== 'google') {
      setError(tf('provider_unavailable'));
      return;
    }
    setLoading(true);
    try {
      gotoHome(await loginGoogle());
    } catch (err) {
      setError((err instanceof Error && err.message) || tf('google_error'));
    } finally {
      setLoading(false);
    }
  }

  async function handlePhoneStart(e: React.FormEvent) {
    e.preventDefault();
    if (!/^\+[1-9]\d{6,14}$/.test(phone)) {
      setError(tf('phone_format'));
      return;
    }
    setLoading(true);
    setError('');
    try {
      const sent = await loginPhoneStart(phone);
      setOtpChannel(sent.channel);
      setPhoneMode(sent.mode);
      setOtpSent(true);
    } catch (err) {
      setError((err instanceof Error && err.message) || tf('send_code_error'));
    } finally {
      setLoading(false);
    }
  }

  async function handlePhoneVerify(e: React.FormEvent) {
    e.preventDefault();
    if (otp.length !== 6) {
      setError(tf('code_6_digits'));
      return;
    }
    setLoading(true);
    setError('');
    try {
      gotoHome(await loginPhoneVerify(phone, otp, phoneMode));
    } catch (err) {
      setError((err instanceof Error && err.message) || tf('code_invalid'));
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="login-page" style={{ 
      display: 'flex', 
      alignItems: 'center', 
      justifyContent: 'center', 
      minHeight: 'calc(100vh - 100px)',
      padding: 20
    }}>
      {/* CSS hack to hide NavRail and Topbar on the login page without refactoring root layouts */}
      <style>{`
        .rail { display: none !important; }
        .topbar { display: none !important; }
        .app { grid-template-columns: 1fr !important; }
        .main { width: 100vw; height: 100vh; }
      `}</style>

      <div 
        style={{
          width: 360,
          maxWidth: 'calc(100vw - 40px)',
          background: 'var(--bg-card)',
          border: '1px solid var(--line)',
          borderRadius: '16px',
          padding: '32px 28px',
          boxShadow: '0 12px 40px rgba(0,0,0,0.2)',
          position: 'relative',
          overflow: 'hidden',
          margin: 'auto'
        }}
      >
        {/* Decorative ambient light */}
        <div style={{
          position: 'absolute',
          top: -100,
          left: -100,
          width: 250,
          height: 250,
          background: 'radial-gradient(circle, rgba(233, 162, 62, 0.15) 0%, transparent 70%)',
          pointerEvents: 'none'
        }} />

        <div style={{ textAlign: 'center', marginBottom: 32 }}>
          <div style={{ display: 'flex', justifyContent: 'center', marginBottom: 16 }}>
            <svg width="40" height="40" viewBox="0 0 30 30" fill="none">
              <rect width="30" height="30" rx="7" fill="var(--bg-inset)" />
              <path
                d="M9 21L15 9l6 12M11.5 17h7"
                stroke="var(--brand)"
                strokeWidth="1.8"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
          </div>
          <h1 style={{ fontSize: 22, color: 'var(--tx-hi)', margin: '0 0 6px', fontWeight: 600 }}>
            {t('title')}
          </h1>
          <p className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)', margin: 0, letterSpacing: '0.04em' }}>
            {t('subtitle')}
          </p>
        </div>

        {authMethod === 'options' ? (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <button
              onClick={() => handleOAuth('google')}
              style={{
                background: '#fff',
                color: '#000',
                border: 'none',
                padding: '12px 16px',
                borderRadius: 6,
                fontFamily: 'var(--sans)',
                fontSize: 13,
                fontWeight: 600,
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                gap: 10,
                transition: 'transform 0.1s'
              }}
              onMouseDown={(e) => e.currentTarget.style.transform = 'scale(0.98)'}
              onMouseUp={(e) => e.currentTarget.style.transform = 'scale(1)'}
            >
              <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
                <path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z" fill="#4285F4"/>
                <path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853"/>
                <path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" fill="#FBBC05"/>
                <path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335"/>
              </svg>
              {tf('continue_google')}
            </button>

            <button
              onClick={() => { setError(''); setOtpSent(false); setAuthMethod('phone'); }}
              style={{
                background: '#25D366',
                color: '#fff',
                border: 'none',
                padding: '12px 16px',
                borderRadius: 6,
                fontFamily: 'var(--sans)',
                fontSize: 13,
                fontWeight: 600,
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                gap: 10,
                transition: 'transform 0.1s'
              }}
              onMouseDown={(e) => e.currentTarget.style.transform = 'scale(0.98)'}
              onMouseUp={(e) => e.currentTarget.style.transform = 'scale(1)'}
            >
              <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor">
                <path d="M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.888-.788-1.489-1.761-1.663-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51a12.8 12.8 0 0 0-.57-.01c-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.625.712.227 1.36.195 1.871.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347m-5.421 7.403h-.004a9.87 9.87 0 0 1-5.031-1.378l-.361-.214-3.741.982.998-3.648-.235-.374a9.86 9.86 0 0 1-1.51-5.26c.001-5.45 4.436-9.884 9.888-9.884 2.64 0 5.122 1.03 6.988 2.898a9.825 9.825 0 0 1 2.893 6.994c-.003 5.45-4.437 9.884-9.885 9.884m8.413-18.297A11.815 11.815 0 0 0 12.05 0C5.495 0 .16 5.335.157 11.892c0 2.096.547 4.142 1.588 5.945L.057 24l6.305-1.654a11.882 11.882 0 0 0 5.683 1.448h.005c6.554 0 11.89-5.335 11.893-11.893a11.821 11.821 0 0 0-3.48-8.413Z"/>
              </svg>
              {tf('continue_phone')}
            </button>

            <button
              onClick={() => setAuthMethod('email')}
              style={{
                background: 'var(--bg-inset)',
                color: 'var(--tx-hi)',
                border: '1px solid var(--line)',
                padding: '12px 16px',
                borderRadius: 6,
                fontFamily: 'var(--sans)',
                fontSize: 13,
                fontWeight: 600,
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                gap: 10,
                transition: 'transform 0.1s'
              }}
              onMouseDown={(e) => e.currentTarget.style.transform = 'scale(0.98)'}
              onMouseUp={(e) => e.currentTarget.style.transform = 'scale(1)'}
            >
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <rect width="20" height="16" x="2" y="4" rx="2"/>
                <path d="m22 7-8.97 5.7a1.94 1.94 0 0 1-2.06 0L2 7"/>
              </svg>
              {tf('continue_email')}
            </button>
          </div>
        ) : authMethod === 'phone' ? (
          <form
            onSubmit={otpSent ? handlePhoneVerify : handlePhoneStart}
            style={{ display: 'flex', flexDirection: 'column', gap: 16 }}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 8 }}>
              <button
                type="button"
                onClick={() => { setAuthMethod('options'); setOtpSent(false); setOtp(''); setError(''); }}
                style={{ background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', padding: 0, display: 'flex', alignItems: 'center' }}
              >
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="m15 18-6-6 6-6"/>
                </svg>
              </button>
              <span style={{ fontSize: 13, color: 'var(--tx-hi)', fontWeight: 500 }}>
                {otpSent ? tf('enter_code') : tf('phone_login')}
              </span>
            </div>

            {!otpSent ? (
              <div>
                <label style={{ display: 'block', fontSize: 11, color: 'var(--tx-dim)', marginBottom: 6, fontWeight: 500 }}>
                  {tf('phone_number')}
                </label>
                <input
                  type="tel"
                  value={phone}
                  onChange={(e) => setPhone(e.target.value.replace(/[^\d+]/g, ''))}
                  placeholder="+593999123456"
                  autoFocus
                  style={{ width: '100%', background: 'var(--bg-void)', border: '1px solid var(--line)', borderRadius: 8, padding: '10px 12px', color: 'var(--tx-hi)', fontFamily: 'var(--mono)', fontSize: 13, outline: 'none' }}
                />
                <p style={{ fontSize: 10, color: 'var(--tx-faint)', margin: '6px 0 0' }}>
                  {tf('phone_note')}
                </p>
              </div>
            ) : (
              <div>
                <label style={{ display: 'block', fontSize: 11, color: 'var(--tx-dim)', marginBottom: 6, fontWeight: 500 }}>
                  {tf('code_sent_by', { channel: otpChannel === 'whatsapp' ? 'WhatsApp' : 'SMS', phone })}
                </label>
                <input
                  type="text"
                  inputMode="numeric"
                  value={otp}
                  onChange={(e) => setOtp(e.target.value.replace(/\D/g, '').slice(0, 6))}
                  placeholder="••••••"
                  autoFocus
                  style={{ width: '100%', background: 'var(--bg-void)', border: '1px solid var(--line)', borderRadius: 8, padding: '10px 12px', color: 'var(--tx-hi)', fontFamily: 'var(--mono)', fontSize: 18, letterSpacing: '0.3em', textAlign: 'center', outline: 'none' }}
                />
                <button
                  type="button"
                  onClick={() => { setOtpSent(false); setOtp(''); setError(''); }}
                  style={{ background: 'none', border: 'none', color: 'var(--brand)', cursor: 'pointer', fontSize: 11, padding: 0, marginTop: 8, textDecoration: 'underline' }}
                >
                  {tf('change_number')}
                </button>
              </div>
            )}

            {error && (
              <div style={{ padding: '10px', background: 'rgba(240, 86, 79, 0.1)', border: '1px solid rgba(240, 86, 79, 0.3)', borderRadius: 8, color: 'var(--real)', fontSize: 12, display: 'flex', alignItems: 'center', gap: 8 }}>
                <span style={{ fontSize: 14 }}>⚠</span>
                {error}
              </div>
            )}

            <button
              type="submit"
              disabled={loading}
              style={{ background: 'var(--brand)', color: '#000', border: 'none', padding: '12px 24px', borderRadius: 30, fontFamily: 'var(--sans)', fontSize: 13, fontWeight: 600, cursor: loading ? 'wait' : 'pointer', display: 'flex', justifyContent: 'center', alignItems: 'center', margin: '8px auto 0', width: 'fit-content', minWidth: 160 }}
            >
              {loading ? <Spinner size={16} /> : otpSent ? tf('verify_enter') : tf('send_code')}
            </button>
          </form>
        ) : (
          <form onSubmit={emailSession ? handleEmailVerify : handleEmailStart} style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 8 }}>
              <button
                type="button"
                onClick={() => { resetEmailFlow(); setAuthMethod('options'); }}
                style={{ background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', padding: 0, display: 'flex', alignItems: 'center' }}
              >
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="m15 18-6-6 6-6"/>
                </svg>
              </button>
              <span style={{ fontSize: 13, color: 'var(--tx-hi)', fontWeight: 500 }}>
                {emailSession ? tf('enter_code') : tf('email_login')}
              </span>
            </div>

            {!emailSession ? (
              <div>
                <label style={{ display: 'block', fontSize: 11, color: 'var(--tx-dim)', marginBottom: 6, fontWeight: 500 }}>
                  Email
                </label>
                <input
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="operador@anka.io"
                  autoComplete="email"
                  autoFocus
                  style={{ width: '100%', background: 'var(--bg-void)', border: '1px solid var(--line)', borderRadius: 8, padding: '10px 12px', color: 'var(--tx-hi)', fontFamily: 'var(--mono)', fontSize: 13, outline: 'none' }}
                />
                <p style={{ fontSize: 10, color: 'var(--tx-faint)', margin: '6px 0 0' }}>
                  {tf('email_note')}
                </p>
              </div>
            ) : (
              <div>
                <label style={{ display: 'block', fontSize: 11, color: 'var(--tx-dim)', marginBottom: 6, fontWeight: 500 }}>
                  {tf('code_sent_to', { email: email.trim() })}
                </label>
                <input
                  type="text"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  value={emailCode}
                  onChange={(e) => setEmailCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
                  placeholder="••••••"
                  autoFocus
                  style={{ width: '100%', background: 'var(--bg-void)', border: '1px solid var(--line)', borderRadius: 8, padding: '10px 12px', color: 'var(--tx-hi)', fontFamily: 'var(--mono)', fontSize: 18, letterSpacing: '0.3em', textAlign: 'center', outline: 'none' }}
                />
                <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 8 }}>
                  <button
                    type="button"
                    onClick={resetEmailFlow}
                    style={{ background: 'none', border: 'none', color: 'var(--brand)', cursor: 'pointer', fontSize: 11, padding: 0, textDecoration: 'underline' }}
                  >
                    {tf('change_email')}
                  </button>
                  <button
                    type="button"
                    onClick={handleEmailResend}
                    disabled={loading}
                    style={{ background: 'none', border: 'none', color: 'var(--brand)', cursor: loading ? 'wait' : 'pointer', fontSize: 11, padding: 0, textDecoration: 'underline' }}
                  >
                    {tf('resend_code')}
                  </button>
                </div>
              </div>
            )}

            {notice && !error && (
              <div style={{ padding: '10px', background: 'rgba(80, 200, 120, 0.08)', border: '1px solid rgba(80, 200, 120, 0.3)', borderRadius: 8, color: 'var(--tx-hi)', fontSize: 12 }}>
                {notice}
              </div>
            )}

            {error && (
              <div style={{ padding: '10px', background: 'rgba(240, 86, 79, 0.1)', border: '1px solid rgba(240, 86, 79, 0.3)', borderRadius: 8, color: 'var(--real)', fontSize: 12, display: 'flex', alignItems: 'center', gap: 8 }}>
                <span style={{ fontSize: 14 }}>⚠</span>
                {error}
              </div>
            )}

            <button
              type="submit"
              disabled={loading}
              style={{ background: 'var(--brand)', color: '#000', border: 'none', padding: '12px 24px', borderRadius: 30, fontFamily: 'var(--sans)', fontSize: 13, fontWeight: 600, cursor: loading ? 'wait' : 'pointer', display: 'flex', justifyContent: 'center', alignItems: 'center', margin: '8px auto 0', width: 'fit-content', minWidth: 160 }}
            >
              {loading ? <Spinner size={16} /> : emailSession ? tf('verify_enter') : tf('send_code')}
            </button>
          </form>
        )}

        <div style={{ marginTop: 24, paddingTop: 20, borderTop: '1px solid var(--line)', textAlign: 'center' }}>
          <p className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)', margin: '0 0 12px' }}>
            {isRegister ? t('has_account') : t('no_account')}{' '}
            <button
              onClick={() => {
                setAuthMethod('options');
                setIsRegister(!isRegister);
              }}
              style={{
                background: 'none',
                border: 'none',
                color: 'var(--brand)',
                cursor: 'pointer',
                fontFamily: 'inherit',
                fontSize: 'inherit',
                padding: 0,
                textDecoration: 'underline'
              }}
            >
              {isRegister ? t('go_login') : t('go_register')}
            </button>
          </p>
          <p className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', margin: 0 }}>
            {t('restriction')}
          </p>
        </div>
      </div>
    </div>
  );
}
