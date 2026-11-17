# AnkaGuard MCP

Servidor MCP de trading con protecciones por defecto: cada agente conectado recibe un token con un perfil (`read`, `paper` o `live`) y el servidor decide en cada llamada qué puede hacer.

> **Aviso importante.** Este software no garantiza ganancias de ningún tipo y no es asesoramiento financiero. Operar con criptomonedas, y más con apalancamiento, puede hacerte perder todo el dinero implicado. El simulador de papel y los backtests son estimaciones y no predicen resultados reales. Usa el dinero real bajo tu propia responsabilidad. Ver [limits.md](docs/product/guide/limits.md).

## Índice

| Documento | Contenido |
|---|---|
| [install.md](docs/product/guide/install.md) | Instalación con Docker en un comando |
| [mcp-clients.md](docs/product/guide/mcp-clients.md) | Conectar un cliente MCP (`/mcp`, token Bearer, ejemplos con curl) |
| [protections.md](docs/product/guide/protections.md) | Perfiles, clases de tools, orden de comprobaciones, tope por orden, parada de emergencia |
| [paper.md](docs/product/guide/paper.md) | Operar en papel sin dinero real |
| [license.md](docs/product/guide/license.md) | Ediciones Community y Pro, licencia firmada, instalación |
| [xsec-lab.md](docs/product/guide/xsec-lab.md) | Laboratorio de backtest XSEC (Pro) y límites del modelo |
| [limits.md](docs/product/guide/limits.md) | Límites conocidos y descargo de responsabilidad |
| [../tools.md](docs/product/tools.md) | Catálogo de tools generado desde el registro real (fuente de verdad del número de tools) |
| [../tool-classification.md](docs/product/tool-classification.md) | Clase de cada tool y matriz de permisos |
| [../api-contract.md](docs/product/api-contract.md) | Contrato de la API REST del producto |

## Resumen

- **Edición Community**: lectura de mercado, operar en papel y todas las protecciones de riesgo.
- **Edición Pro**: añade el perfil `live` con dinero real, la consola web y el laboratorio de backtest. Requiere una licencia firmada.
- Tras una instalación por defecto no se puede enviar ninguna orden real: hace falta añadir credenciales de exchange, instalar una licencia Pro y habilitar el dinero real (`trading.live_enabled`).
- Exchange soportado: KuCoin, a través de GoCryptoTrader.

La documentación describe la versión 1.0.0: el instalador, el compose, la consola y las licencias están incluidos y probados. Los límites conocidos están en [limits.md](docs/product/guide/limits.md).
