'use client';
 
export default function GlobalError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  return (
    <html lang="es">
      <body style={{ 
        margin: 0, 
        backgroundColor: '#06080c', 
        color: '#b9c0cd',
        fontFamily: 'system-ui, sans-serif'
      }}>
        <div style={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          justifyContent: 'center',
          height: '100vh',
          padding: '24px',
        }}>
          <div style={{
            background: '#10141c',
            border: '1px solid #f0564f',
            borderRadius: '11px',
            padding: '32px',
            maxWidth: '500px',
            width: '100%',
            textAlign: 'center',
          }}>
            <h2 style={{ color: '#eef1f6', marginBottom: '16px' }}>Error Crítico del Sistema</h2>
            <p style={{ color: '#7e8799', marginBottom: '24px' }}>
              La aplicación ha encontrado un error irrecuperable en el layout principal.
              {error.message && <div style={{ 
                fontFamily: 'monospace', 
                fontSize: '11px', 
                background: '#0c0f15', 
                padding: '10px', 
                marginTop: '16px',
                borderRadius: '6px',
                color: '#f0564f',
                textAlign: 'left'
              }}>{error.message}</div>}
            </p>
            <button
              onClick={() => reset()}
              style={{
                background: '#4f9bf5',
                color: '#fff',
                border: 'none',
                padding: '10px 20px',
                borderRadius: '7px',
                cursor: 'pointer',
                fontWeight: 600
              }}
            >
              Reiniciar Consola
            </button>
          </div>
        </div>
      </body>
    </html>
  );
}
