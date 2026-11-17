# Licencias del producto

Este directorio reúne los textos de licencia del producto (tarea 10.10 del cambio `guarded-mcp-product`).

| Archivo | Contenido |
|---|---|
| `LICENSE-COMMUNITY` | Texto estándar de Apache License 2.0, para la edición Community. |
| `LICENSE-PRO` | Licencia comercial propietaria de AnkaGuard MCP Pro (titular Wankarlab, ley de Ecuador). Se recomienda que la revise un abogado antes de la primera venta. |
| `THIRD_PARTY_NOTICES.md` (raíz de la exportación) | Lo genera `scripts/product/gen_notices.sh` en cada exportación: todos los módulos Go enlazados en el servidor y en el laboratorio XSEC (directos e indirectos) con el texto completo de su licencia, y los paquetes npm de la consola con la licencia declarada en `package-lock.json`. La exportación falla si a algún módulo Go le falta el archivo de licencia. GoCryptoTrader es MIT. |

## Qué es Community y qué es Pro

Según `design.md` (decisión D8), el producto vive en un único repositorio y las ediciones se separan por etiqueta de compilación y por directorio:

- **Community (Apache-2.0)**: todo el repositorio exportado salvo los directorios Pro. Incluye las tools de lectura, el broker de papel (`internal/paper`), el servidor MCP con todas las protecciones de riesgo, la autenticación local y la verificación de licencias.
- **Pro (licencia comercial, `LICENSE-PRO`)**: los componentes que la especificación `product-licensing` asigna a la edición Pro, es decir, la ruta de órdenes reales (etiqueta `pro`), la consola web (anka-arena en modo producto) y el laboratorio de backtest de XSEC. Cada directorio Pro debe llevar su propia copia de `LICENSE-PRO`.

Directorios Pro de la exportación, cada uno con su copia de `LICENSE-PRO` como `LICENSE` (la copia `scripts/product/export.sh`): `anka-arena/` (consola) y `cmd/xsec-sim/` (laboratorio de backtest). La ruta de órdenes reales no es un directorio: se activa con la etiqueta de compilación `pro` (`internal/edition/build_product_pro.go`) y sin ella la compilación Community no permite dinero real con ninguna licencia. La raíz lleva `LICENSE-COMMUNITY` como `LICENSE`.

Una licencia comercial en el código no impide técnicamente copiarlo: es una protección legal. La separación técnica real es que la compilación Community no incluye la ruta de órdenes reales.
