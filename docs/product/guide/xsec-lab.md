# Laboratorio de backtest XSEC (solo Pro)

La imagen Pro del servidor incluye `xsec-sim`, un simulador de la cesta de momentum transversal (largos en los pares que más suben, cortos en los que más bajan, con capital fijo). Sirve para **explorar** configuraciones, no para prometer resultados.

> **Aviso.** Un backtest no garantiza ganancias. Lo que ves aquí es una estimación hecha con un modelo simplificado; el mercado real será peor en casi todos los casos. Ver [limits.md](limits.md).

## Disponibilidad

- Solo en la imagen compilada con las etiquetas `product,pro` (`EDITION_TAGS=product,pro`, valor por defecto de `install.sh`).
- La imagen Community no lo incluye.
- El binario está en `/usr/local/bin/xsec-sim` dentro del contenedor del servidor.

## Datos que necesita

Lee velas diarias de una base SQLite (tabla `candle_archive`), por defecto `data/trading.db` relativo al directorio de trabajo (`/app/data/trading.db` en la imagen). El producto no trae esa base: debes cargarla tú con tus propias velas. Sin ella el simulador no tiene qué calcular. Con la opción `-min-turnover-usd` consulta además la API pública de contratos de KuCoin (necesita red).

## Uso

```bash
docker compose exec server xsec-sim -db /app/data/trading.db -tf 1d -lb 14 -k 3 -rebal 7 -cost 0.0012 -funding 0.0001
docker compose exec server xsec-sim -help      # lista completa de opciones
```

Opciones principales: `-lb` (ventana de momentum en velas), `-k` (pares por lado), `-rebal` (periodo de rebalanceo), `-band` (banda sin operar), `-cost` (comisión por rotación), `-funding` (arrastre de funding), `-vol-weights` (pesos por volatilidad inversa), `-windows` (sub-periodos secuenciales), `-print-equity`, `-csv-equity`, `-csv-trades`.

## Límites del modelo

Léelos antes de fiarte de cualquier número:

1. **Sin deslizamiento (slippage).** Se cobra una comisión fija por rotación, pero no el impacto de tu orden en el precio ni el spread. En pares poco líquidos el coste real es mayor.
2. **Entrada al mismo cierre.** La señal se calcula con el cierre de una vela y la operación se ejecuta a ese mismo cierre. En la práctica la señal llega después de que la vela cierra y el precio ya se movió: el modelo es optimista.
3. **Sesgo de supervivencia parcial.** Solo existen en la base los pares que sigues teniendo; los que se deslistaron o murieron no aparecen. La opción `-min-turnover-usd` reconstruye un universo por liquidez histórica en cada rebalanceo, lo que reduce el sesgo pero no lo elimina.
4. **Sin profundidad de libro ni rechazos.** Se asume que toda orden se ejecuta completa.
5. **Funding aproximado.** Salvo que cargues `funding_archive` (`-funding-archive`), se usa un valor escalar.
6. **El pasado no predice el futuro.** Un resultado bueno en un periodo no implica que lo sea en el siguiente; usa `-windows` y desconfía de una sola ventana positiva.

## Relación con el dinero real

`xsec-sim` solo simula: no se conecta a ningún exchange para operar. Operar la cesta con dinero real no forma parte del producto.
