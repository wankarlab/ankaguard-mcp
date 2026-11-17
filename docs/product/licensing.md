# Licencias del producto

La licencia es un documento firmado que el servidor verifica **sin red** con una clave
pública Ed25519 incluida en el binario (`internal/license/pubkey.ed25519`, `go:embed`).
El producto no contacta nunca con ningún servidor del proveedor por motivos de licencia.

## Formato

`base64url(payload JSON) + "." + base64url(firma Ed25519 del payload)`, sin relleno.
Campos del payload: `id`, `holder`, `edition` (`pro` o `community`), `issued_at`,
`expires_at` (RFC3339 UTC).

## Estados

| Estado | Cuándo | Efecto |
|---|---|---|
| `valid` | antes de `expires_at` | Pro disponible. Con 14 días o menos para caducar, aviso `expiring_soon` |
| `grace` | hasta 14 días tras caducar | Pro disponible, aviso `in_grace` y `grace_days_remaining` |
| `expired` | más de 14 días tras caducar, o reloj atrasado | Comportamiento Community |
| `invalid` | sin licencia, manipulada o ilegible | Comportamiento Community |

Si la hora del sistema retrocede más de 24 horas respecto a la última verificación
guardada (`license_clock.json` en el directorio de datos), el estado pasa a `expired` con
aviso `clock_skew` hasta que la hora vuelva a ser coherente. Un retroceso menor no cambia
nada. La edición efectiva es `pro` solo con estado `valid`/`grace`, licencia `pro` y
compilación `product,pro`.

Aunque la licencia caduque, las llamadas `read`, `trade_reduce`, `safety` y todo el perfil
`paper` siguen funcionando: una licencia nunca atrapa posiciones.

## Emitir (operador, repositorio de origen)

La clave privada nunca va al repositorio del producto. Se usa
`ED25519_PRIVATE_KEY_FILE` (PEM) o `ED25519_PRIVATE_KEY` (semilla en base64).

```bash
# Generar el par (una sola vez; no sobrescribe archivos existentes)
license-server keygen -out-dir /ruta/segura/keys
#   private.pem (0600), public.pem, pubkey.ed25519 (clave pública para el binario)

# Emitir una licencia Pro de 365 días y registrarla
ED25519_PRIVATE_KEY_FILE=/ruta/segura/keys/private.pem \
  license-server issue -holder "ACME SL" -edition pro -days 365 \
  -out acme.lic -ledger emisiones.jsonl -issuer yo
```

- `-days` debe ser mayor que cero; si no, la emisión se rechaza.
- El registro lleva id, titular, edición, fechas y emisor (`-issuer`, por defecto `$USER`).
  Con `-ledger FILE` se añade una línea JSON al archivo; sin él se usa `DATABASE_URL`
  (tabla `licenses`, migración `002_issuance_ledger`). Sin ninguno de los dos no se emite.
  El documento solo se entrega después de registrar la emisión.

## Instalar y renovar

Tres vías equivalentes; no hace falta reiniciar con la tercera:

1. Variable `LICENSE_KEY` con el documento, o `LICENSE_FILE` con la ruta de un archivo.
2. Archivo `license.key` en el directorio de datos.
3. `PUT /api/v1/license` con `{"license": "<documento>"}` (sesión de operador).

Si hay varias fuentes al arrancar, gana la que caduca más tarde. Un documento inválido se
rechaza con `400 LICENSE_INVALID` y `reason` (`malformed`, `bad_signature`, `wrong_key`,
`expired`), y la licencia anterior se conserva. Una renovación es simplemente instalar el
documento nuevo. Estado: `GET /api/v1/license` (nunca incluye el documento). El servidor
reverifica cada hora.

## Servidor de licencias (`cmd/license-server`)

No se exporta con el producto. `LICENSE_ADMIN_TOKEN` es obligatorio: sin él no arranca.
Todas las rutas que modifican estado (`/v1/activate`, `/v1/heartbeat`, `/v1/revoke`,
`/v1/rebind`) exigen la cabecera `X-Admin-Token` (comparación en tiempo constante). Los
webhooks de Odoo (`/webhooks/odoo`) conservan su firma HMAC-SHA256 en `X-Odoo-Signature`
con `ODOO_WEBHOOK_SECRET`; sin secreto configurado se rechazan todos.

## Clave de firma en uso

El par de claves de la primera versión se generó el 2026-11-16 con `license-server keygen`.
La clave pública ya está en `internal/license/pubkey.ed25519`. La privada está fuera del
repositorio, en la máquina del operador: `~/.config/trading-mcp-product/keys/private.pem`
(permisos 0600; el directorio, 0700). Guarda una copia cifrada fuera de esa máquina: si se
pierde, no se pueden emitir licencias que acepten los binarios ya publicados y hay que
publicar una versión nueva con otra clave.

## Comprobación de publicación

Antes de publicar una versión, con la clave real ya copiada a
`internal/license/pubkey.ed25519`:

```bash
go test -tags release -run TestReleaseKeyIsNotPlaceholder ./internal/license
```

Falla mientras el archivo contenga la clave de relleno (32 bytes a cero). Sin la etiqueta
`release` la prueba no se compila. Además, la clave privada no debe aparecer en el
repositorio ni en las imágenes.

## Compilaciones

`go build` (sin etiqueta) conserva el comportamiento actual; `-tags product` es la
edición Community del producto; `-tags product,pro` añade los componentes Pro
(`internal/edition`).
