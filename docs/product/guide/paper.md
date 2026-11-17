# Operar en papel

El perfil `paper` ejecuta las mismas tools de órdenes, posiciones y saldo contra un **broker simulado** persistente (SQLite) que usa **precios reales** de mercado. Nunca llega al exchange. No necesita licencia ni credenciales de exchange y forma parte de la edición Community.

Resumen (detalle completo, parámetros y códigos de error en [../paper-trading.md](../paper-trading.md)):

- Una cuenta por titular (el sujeto del token), con saldo inicial `paper.initial_balance_usd` (1000 USD por defecto). Dos tokens distintos no ven nada del otro.
- Mercado: último precio real con deslizamiento `paper.slippage_pct` (0,05 %) y comisión `paper.taker_fee_pct` (0,06 %). Límite y stop quedan pendientes hasta que el precio real los alcanza. Sin precio reciente (máx. 10 s) la orden se rechaza con `DATA_UNAVAILABLE`.
- Las órdenes pasan por el mismo centinela de riesgo que las reales.
- El estado sobrevive a reinicios del servidor.
- Tipos no soportados (stop-limit, trailing, IOC/FOK, margin, opciones) se rechazan con `PAPER_UNSUPPORTED`.
- Todas las respuestas del broker llevan `paper: true` y sus supuestos.
- El operador puede reiniciar una cuenta con `POST /api/v1/paper/accounts/:holder/reset`; un token de agente no puede.

Límites del simulador (sin funding, sin ejecuciones parciales, sin profundidad de libro, sin comisión de maker, etc.): ver [limits.md](limits.md). Que algo funcione en papel no garantiza que funcione con dinero real.
