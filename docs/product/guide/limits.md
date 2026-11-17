# Límites conocidos

> **Descargo de responsabilidad.** Este producto no garantiza ganancias ni resultados de trading y no es asesoramiento financiero ni de inversión. Se entrega sin garantía. Operar con criptomonedas, con o sin apalancamiento, puede hacerte perder todos los fondos. Los resultados de papel y de backtest son estimaciones: el pasado no predice el futuro.

## Mercados y exchange

- Un único exchange: **KuCoin**, a través de **GoCryptoTrader** (gRPC). No hay segundo exchange.
- Necesita GoCryptoTrader en marcha y Postgres para la configuración en caliente; ambos van en el `docker-compose` de producto.
- Papel: spot en pares cotizados en USDT, USDC o USD, y futuros lineales USDT (`...USDTM`).
- No incluidos en el producto: scalping, bot de IA, torneo, copy trading, AnkaLaunch y servicios de RantiPay.
- Sin revocación de licencias antes de su caducidad.

## Simulador de papel

Sin funding; sin ejecuciones parciales; sin profundidad de libro (deslizamiento fijo, sin importar el tamaño); sin comisión de maker; precio de referencia = último precio (no bid/ask ni precio de marca); una posición neta por contrato; solo margen aislado; tipos de orden no soportados rechazados. Detalle en [../paper-trading.md](../paper-trading.md).

## Backtest

`backtest_strategy` repite una estrategia simple sobre velas reales recientes y devuelve una **estimación** con sus supuestos (sin funding; comisiones y deslizamiento supuestos).

## Laboratorio de backtest XSEC (edición Pro)

El modelo es optimista. Límites conocidos (tarea 10.12 del cambio):

- **Sin deslizamiento.**
- **Entrada al mismo cierre** que genera la señal.
- **Sesgo de supervivencia parcial** en el universo de pares.

Sus resultados no deben leerse como rendimiento esperable. El laboratorio se entrega documentando estos límites; no se corrige el optimismo del modelo.

## Honestidad de las tools

Ninguna tool debe devolver datos inventados como reales: si la fuente falla, devuelve `DATA_UNAVAILABLE`. Las estimaciones llevan `estimate: true` y sus supuestos. Ver [../tools.md](../tools.md) y [../honesty-review.md](../honesty-review.md).
