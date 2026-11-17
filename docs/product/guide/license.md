# Licencia y ediciones

Detalle técnico en [../licensing.md](../licensing.md).

## Ediciones

| | Community | Pro |
|---|---|---|
| Tools de lectura y datos de mercado | sí | sí |
| Perfil `paper` (broker simulado) | sí | sí |
| Protecciones de riesgo | sí | sí |
| Perfil `live` con dinero real | no | sí |
| Consola web | no | sí |
| Laboratorio de backtest | no | sí |

Precio de Pro: **19 USD al mes o 190 USD al año** (dos meses gratis). Cada pago emite una licencia con la vigencia pagada (30 o 365 días). La primera versión emite las licencias a mano tras el pago. Community es gratuita.

Sin licencia válida el producto se comporta como Community. El código Community se distribuye bajo Apache-2.0 y los componentes Pro bajo licencia comercial (ver `deploy/product/licenses/`).

## La licencia

Es un documento firmado (`base64url(payload).base64url(firma)`) con campos `id`, `holder`, `edition`, `issued_at` y `expires_at`. Se verifica **sin red** con una clave pública Ed25519 incluida en el binario. El producto no contacta con ningún servidor del proveedor por motivos de licencia.

| Estado | Cuándo | Efecto |
|---|---|---|
| `valid` | antes de caducar | Pro disponible; aviso `expiring_soon` a 14 días o menos |
| `grace` | hasta 14 días tras caducar | Pro disponible, con aviso `in_grace` |
| `expired` | más de 14 días tras caducar, o reloj atrasado | Comportamiento Community |
| `invalid` | sin licencia, manipulada o ilegible | Comportamiento Community |

Si la hora del sistema retrocede más de 24 horas respecto a la última verificación, el estado pasa a `expired` con aviso `clock_skew`. **Una licencia caducada nunca impide reducir o cerrar posiciones.** No hay revocación anterior a la caducidad.

## Instalar o renovar

Tres vías equivalentes (renovar es instalar el documento nuevo):

1. Variable de entorno `LICENSE_KEY` (el documento) o `LICENSE_FILE` (ruta a un archivo).
2. Archivo `license.key` en el directorio de datos del servidor.
3. `PUT /api/v1/license` con `{"license": "<documento>"}` y sesión de operador (o la pantalla de licencia de la consola). No requiere reiniciar.

Un documento inválido se rechaza (`400 LICENSE_INVALID` con `reason`: `malformed`, `bad_signature`, `wrong_key`, `expired`) y se conserva la licencia anterior. `GET /api/v1/license` muestra el estado sin devolver nunca el documento. El servidor reverifica cada hora.

Conseguir una licencia Pro: abre un issue en el repositorio de GitHub de AnkaGuard MCP pidiendo una licencia e indica a nombre de quién debe ir (`holder`). En esta primera versión el proveedor acuerda el pago contigo y emite la licencia a mano.
