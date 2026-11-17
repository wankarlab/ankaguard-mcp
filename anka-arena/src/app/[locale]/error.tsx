'use client';
 
import { useEffect } from 'react';
 
export default function Error({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    // Log the error to an error reporting service
    console.error('App Error Boundary caught an error:', error);
  }, [error]);
 
  return (
    <div style={{
      display: 'flex',
      flexDirection: 'column',
      alignItems: 'center',
      justifyContent: 'center',
      height: '100vh',
      padding: '24px',
      background: 'var(--bg-void)',
      color: 'var(--tx)',
      fontFamily: 'var(--sans)'
    }}>
      <div style={{
        background: 'var(--bg-panel)',
        border: '1px solid var(--line-2)',
        borderRadius: 'var(--r-lg)',
        padding: '32px',
        maxWidth: '500px',
        width: '100%',
        textAlign: 'center',
        boxShadow: 'var(--sh-2)'
      }}>
        <div style={{
          width: '56px',
          height: '56px',
          borderRadius: '50%',
          background: 'var(--real-soft)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          margin: '0 auto 20px',
          color: 'var(--real)',
        }}>
          <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"></path>
            <line x1="12" y1="9" x2="12" y2="13"></line>
            <line x1="12" y1="17" x2="12.01" y2="17"></line>
          </svg>
        </div>
        <h2 style={{ fontSize: '18px', fontWeight: 600, color: 'var(--tx-hi)', marginBottom: '12px' }}>
          Error en la consola
        </h2>
        <p style={{ color: 'var(--tx-dim)', fontSize: '13.5px', marginBottom: '24px', lineHeight: 1.5 }}>
          Se ha producido un error inesperado al renderizar esta vista. 
          {error.digest && <span className="mono" style={{ display: 'block', marginTop: '8px', fontSize: '11px' }}>Digest: {error.digest}</span>}
        </p>
        <button
          className="btn primary"
          onClick={() => reset()}
          style={{ width: '100%' }}
        >
          Intentar recuperar
        </button>
      </div>
    </div>
  );
}
