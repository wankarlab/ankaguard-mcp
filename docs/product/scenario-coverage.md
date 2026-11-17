# Cobertura de escenarios de `guarded-mcp-product` (tarea 11.1)

Recorrido de los 146 escenarios (`#### Scenario:`) de las nueve especificaciones de
la especificación del producto (en el repositorio de origen, `openspec/specs/`). Fecha: 2026-11-16, versión 1.0.0.

Convenciones:

- Las rutas de prueba son relativas a la raíz del repositorio; `Test*` es la función. Las pruebas Go sin etiqueta corren en las tres combinaciones (sin etiqueta, `product`, `product,pro`); las que dependen de la edición lo indican.
- **cubierto**: existe una prueba automática (o una verificación manual registrada) que comprueba el `THEN` del escenario.
- **parcial**: se comprueba una parte, o el cableado final solo se ha verificado leyendo el código, o la verificación completa depende de una tarea abierta.
- **sin cubrir**: no hay prueba ni verificación; ver "Brechas de comportamiento".
- **operador**: requiere una acción del operador (generar la clave de firma, publicar, orden real); se indica la tarea de `tasks.md`.
- "(nuevo)" marca las pruebas añadidas en esta tarea.
- Estado de la verificación de Docker: `scripts/product/install_test.sh` Parte B ya se ejecutó y pasó (instalación limpia, sano en menos de 5 min, contraseña de operador, login, token `paper`, `tools/list` con 78 tools, orden de papel ejecutada, reinstalación conserva `.env` y datos, dos instalaciones con secretos distintos, puertos ligados a 127.0.0.1). En esta tarea no se ejecutó Docker.
- La comprobación en navegador de la consola (tarea 9.8, 390 px y escritorio) sigue abierta: los escenarios de consola se cubren con pruebas de componentes y de funciones puras de `anka-arena` (vitest).

## Resumen

| Especificación | Escenarios | cubierto | parcial | sin cubrir | operador |
|---|---:|---:|---:|---:|---:|
| mcp-tool-honesty | 9 | 9 | 0 | 0 | 0 |
| mcp-tool-permissions | 17 | 17 | 0 | 0 | 0 |
| order-notional-cap | 15 | 15 | 0 | 0 | 0 |
| paper-trading-broker | 18 | 18 | 0 | 0 | 0 |
| product-console | 15 | 12 | 3 | 0 | 0 |
| product-distribution | 18 | 17 | 0 | 0 | 1 |
| product-licensing | 23 | 22 | 1 | 0 | 0 |
| secure-defaults | 14 | 14 | 0 | 0 | 0 |
| standalone-auth | 17 | 17 | 0 | 0 | 0 |
| **Total** | **146** | **141** | **4** | **0** | **1** |

## mcp-tool-honesty

| Escenario | Cobertura | Estado |
|---|---|---|
| Fuente no disponible | `internal/mcp-trading/tools/backtest/backtest_tools_test.go::TestBacktestDataUnavailable`; `internal/mcp-trading/tools/futures/calculate_funding_costs_test.go::TestFundingCostsDataUnavailable`; `internal/mcp-trading/tools/technical/candle_indicators_test.go::TestIndicatorDataUnavailable`; `internal/mcp-trading/tools/scalping/analyze_opportunity_test.go::TestAnalyzeOpportunityDataUnavailable` | cubierto |
| Historial insuficiente para una métrica | `internal/mcp-trading/tools/portfolio/advanced_analysis_tools_test.go::TestAdvancedMetricsDataUnavailable`, `::TestRiskMetricsDataUnavailableWithShortHistory` | cubierto |
| Tool retirada | `internal/mcp-trading/toolinventory/honesty_test.go::TestRetiredToolsAreNotRegistered` (no está en el registro y `tools/call` responde "Tool not found") | cubierto |
| Tool implementada con datos reales | `backtest_tools_test.go::TestBacktestResultChangesWithCandles`; `advanced_analysis_tools_test.go::TestAdvancedMetricsFollowsEquityHistory`, `::TestRiskMetricsFollowHistoryAndHoldings`; `calculate_funding_costs_test.go::TestFundingCostsFollowRealRate`; `internal/mcp-trading/tools/orders/trailing_stop_tools_test.go::TestTrailingStopPriceFollowsLastPrice`; `internal/mcp-trading/tools/llm/analyze_market_test.go::TestAnalyzeMarketReturnsProviderAnswer`; `honesty_test.go::TestAuditedToolsThatStayAreRegistered` | cubierto |
| Simulación de una orden | `internal/mcp-trading/tools/orders/simulate_order_test.go::TestSimulateOrderResultIsLabeledEstimate` (`estimate: true`, comisión y deslizamiento, la respuesta sigue al precio) | cubierto |
| Resultado de backtest | `backtest_tools_test.go::TestBacktestDeclaresEstimateAndAssumptions` (`period_start/end`, `candle_source`, comisión, deslizamiento, `funding_modeled=false`) | cubierto |
| Proveedor caído | `analyze_market_test.go::TestAnalyzeMarketProviderFailureReturnsErrorNotFiller`, `::TestAnalyzeMarketWithoutProviderReturnsError` | cubierto |
| Catálogo desactualizado | `internal/mcp-trading/toolinventory/catalog_test.go::TestToolCatalog`; `inventory_test.go::TestToolInventory`; `classification_test.go::TestClassificationDoc` (fallan si el documento difiere del registro) | cubierto |
| Origen de datos declarado | `catalog_test.go::TestEveryRegisteredToolHasAnOrigin`; `internal/mcp-trading/toolpolicy/policy_test.go::TestOriginTableMatchesClassTable` | cubierto |

## mcp-tool-permissions

| Escenario | Cobertura | Estado |
|---|---|---|
| Tool sin clasificar | `internal/mcp-trading/server/permission_test.go::TestUnclassifiedToolIsNeverListedNorExecuted` (no se lista, `PERMISSION_DENIED`); `internal/mcp-trading/server/permission_startup_test.go::TestAuditToolClassificationLogsAnErrorNamingTheTool` (nuevo: el arranque registra un error que nombra la tool) | cubierto |
| Inventario verificable | `internal/mcp-trading/toolinventory/classification_test.go::TestEveryRegisteredToolHasAClass` (faltantes y sobrantes en ambos sentidos) | cubierto |
| Orden directa de futuros con solo reducción | `internal/mcp-trading/toolpolicy/policy_test.go::TestClassForCallArguments`; `permission_test.go::TestKillSwitchOnlyBlocksTradeIncrease` (`reduce_only`/`close_order` se ejecutan) | cubierto |
| Argumento ausente o ambiguo | `policy_test.go::TestClassForCallArguments` (ausente, `false`, texto y número dan `trade_increase`) | cubierto |
| Perfil de lectura intenta enviar una orden | `permission_test.go::TestPermissionMatrix` (`trade_increase`/`read`: `PERMISSION_DENIED` y la tool no se ejecuta) | cubierto |
| Perfil de papel envía una orden | `internal/mcp-trading/toolinventory/paper_isolation_test.go::TestPaperProfileNeverReachesTheRealAccount`; `internal/mcp-trading/server/product_gates_test.go::TestPaperEnvelope`; `toolinventory/paper_scenarios_test.go::TestPaperScenarioMarketOrderFillsAtRealPriceWithCosts` | cubierto |
| Perfil de papel intenta cambiar configuración | `permission_test.go::TestPermissionMatrix` (`control`/`paper`) | cubierto |
| Perfil real activa la parada de emergencia | `permission_test.go::TestPermissionMatrix` (`safety` con `paper` y `live`); `::TestKillSwitchOnlyBlocksTradeIncrease` | cubierto |
| Token heredado de nivel bajo | `permission_test.go::TestProfilelessTokens` (niveles 2 y 3 sin perfil: denegado); `toolpolicy/policy_test.go::TestProfileFromClaim` | cubierto |
| Token heredado de operador | `permission_test.go::TestProfilelessTokens` (nivel 4 sin perfil: se ejecuta como `live`) | cubierto |
| Perfil desconocido | `permission_test.go::TestProfilelessTokens` (`superuser` a nivel 4: denegado); `policy_test.go::TestProfileFromClaim` | cubierto |
| Listado con perfil de lectura | `permission_test.go::TestToolsListFilteredByProfile` | cubierto |
| Listado con perfil de papel | `permission_test.go::TestToolsListFilteredByProfile` (`read`+`trade_*`+`safety`, sin `control`) | cubierto |
| Contenido del rechazo | `permission_test.go::TestPermissionDeniedPayload`; `permission_startup_test.go::TestPermissionDeniedCarriesOnlyToolClassAndProfile` (nuevo: ningún otro campo ni datos de la llamada) | cubierto |
| Auditoría del rechazo | `permission_test.go::TestDenialIsAuditedWithTokenIdentifier` (hora, `jti` o `sub`, tool, clase, perfil; nunca el token) | cubierto |
| Parada global activa y orden de apertura | `permission_test.go::TestKillSwitchOnlyBlocksTradeIncrease`; `::TestPermissionCheckedBeforeKillSwitch`; `internal/killswitch/ring0_policy_test.go::TestRiskIncreasingToolsKeepsLegacyEight` | cubierto |
| Parada global activa y cierre | `permission_test.go::TestKillSwitchOnlyBlocksTradeIncrease` (`trade_reduce`, `safety` y `read` siguen) | cubierto |

## order-notional-cap

| Escenario | Cobertura | Estado |
|---|---|---|
| Orden a mercado por encima del tope | `internal/risk/sentinel_notional_cap_test.go::TestCheckOrderCap` ("market above cap": `ORDER_CAP_EXCEEDED`), `::TestCheckOrderCap_MessageCarriesNotionalAndCap`; `internal/gct/order_notional_test.go::TestNotional_FuturesMinimumContractExceedsTarget` (6,58 frente a 2,00) | cubierto |
| Orden a mercado dentro del tope | `sentinel_notional_cap_test.go::TestCheckOrderCap` ("market within cap") | cubierto |
| Tope relativo menor que el absoluto | `sentinel_notional_cap_test.go::TestEffectiveCap_Scenarios`; `internal/risk/sentinel_order_cap_test.go::TestEffectiveOrderCap` | cubierto |
| Sin tope configurado | `sentinel_notional_cap_test.go::TestCheckOrderCap` ("no cap configured"), `::TestEffectiveCap_Scenarios` | cubierto |
| Orden spot en unidades base | `order_notional_test.go::TestNotional_SpotBaseUnits` (60 USD) | cubierto |
| Orden de futuros en contratos | `order_notional_test.go::TestNotional_FuturesContracts` (3 USD) | cubierto |
| Orden límite con precio propio | `order_notional_test.go::TestNotional_LimitUsesOwnPriceWithoutLookup`, `::TestEvaluateOrderRisk_LimitCarriesOwnPrice` | cubierto |
| Sin precio de referencia | `order_notional_test.go::TestNotional_NoReferencePriceIsUnknown`, `::TestNotional_StaleReferencePriceIsUnknown`; `sentinel_notional_cap_test.go::TestCheckOrderCap` ("unknown notional, enforced": `ORDER_NOTIONAL_UNKNOWN`) | cubierto |
| Especificación del contrato desconocida | `order_notional_test.go::TestNotional_UnknownContractSpecIsUnknown` + el mismo caso del centinela | cubierto |
| Cierre de una posición grande | `sentinel_notional_cap_test.go::TestCheckOrderCap` ("big close, enforced"); `order_notional_test.go::TestEvaluateOrderRisk_CloseOrdersSkipLookups`; `internal/gct/order_gate_test.go::TestEvaluateOrderRisk_SpotReduceOnlyBuyIsAnOpen` | cubierto |
| Cierre sin precio de referencia | `sentinel_notional_cap_test.go::TestCheckOrderCap` ("close without reference price, enforced") | cubierto |
| Tope menor que el tamaño del bot | `internal/services/ai_trading_bot_ordercap_status_test.go::TestSizingStatus_ExposesOrderCapState` (2,00 frente a 7,00 en el estado); `internal/services/ai_trading_bot_sizing_test.go::TestCheckConsistency_WarnsWhenSentinelCapBelowBotMax` (aviso de arranque/recarga con ambos valores) | cubierto |
| Valores alineados | `ai_trading_bot_ordercap_status_test.go::TestSizingStatus_ExposesOrderCapState` (tope 10, sin advertencias) | cubierto |
| Despliegue existente tras actualizar | `internal/config/dynamic/manifest/sizing_test.go::TestEnforceCapOnMarketOrdersDefaultFollowsEdition` (nuevo: `false` fuera de `product`); `TestSizingStatus_ExposesOrderCapState` (el estado advierte que el tope no se aplica); `TestCheckOrderCap` ("market without price, enforcement off") | cubierto |
| Instalación nueva del producto | `sizing_test.go::TestEnforceCapOnMarketOrdersDefaultFollowsEdition` (nuevo: `true` con `product`); `internal/risk/sentinel_product_test.go::TestProductSentinelWithoutStoreUsesDefaults` (etiqueta `product`); `cmd/trading-mcp-server/boot_product_test.go::TestProductBinaryBoot` (paso 7: `ORDER_CAP_EXCEEDED` en el binario) | cubierto |

## paper-trading-broker

| Escenario | Cobertura | Estado |
|---|---|---|
| Orden de papel | `internal/paper/paper_test.go::TestNoOrderOrAccountMethodReachesTheRealClient`; `internal/mcp-trading/toolinventory/paper_isolation_test.go::TestPaperProfileNeverReachesTheRealAccount`; `internal/mcp-trading/gctclient/paper_route_test.go::TestForUserMethodsRefuseAPaperProfile` | cubierto |
| Fallo del broker de papel | `paper_test.go::TestNoHolderIsAnErrorNeverASharedAccount`; `internal/mcp-trading/gctclient/paper_route_test.go::TestRouteFailsClosedWithoutPaperClient`; `internal/mcp-trading/server/permission_test.go::TestPaperTradeNeverExecutesWithoutBroker`; `product_gates_test.go::TestPaperEnvelope` ("broker not ready") | cubierto |
| Orden a mercado de compra | `paper_test.go::TestMarketBuyAtRealPricePlusSlippageAndFee`; `toolinventory/paper_scenarios_test.go::TestPaperScenarioMarketOrderFillsAtRealPriceWithCosts` | cubierto |
| Orden límite que no cruza | `paper_test.go::TestLimitBuyThatDoesNotCrossStaysOpenThenFillsAtLimit`; `paper_scenarios_test.go::TestPaperScenarioLimitOrderRestsUntilPriceCrosses` | cubierto |
| Orden límite que cruza después | mismas pruebas (ejecución al precio límite y posición actualizada); `paper_test.go::TestStopOrderActivatesOnTrigger` para stops | cubierto |
| Precio no disponible | `paper_test.go::TestPriceUnavailableRejectsWithDataUnavailable`, `::TestStalePriceIsDataUnavailable` | cubierto |
| Reinicio del servidor | `paper_test.go::TestStateSurvivesRestart`; `paper_scenarios_test.go::TestPaperScenarioPendingOrderSurvivesRestart` | cubierto |
| Dos titulares | `paper_test.go::TestTwoHoldersAreIsolated`; `paper_scenarios_test.go::TestPaperScenarioHoldersAreIsolated` | cubierto |
| Saldo insuficiente | `paper_test.go::TestInsufficientBalanceReportsAvailableAndRequired`; `paper_scenarios_test.go::TestPaperScenarioInsufficientBalanceReportsAvailableAndRequired` | cubierto |
| Consulta de posiciones en papel | `paper_scenarios_test.go::TestPaperScenarioQueriesAreMarkedAndLiveProfileIsNotServedByPaper` | cubierto |
| Cuenta real invisible | `paper_isolation_test.go::TestPaperProfileNeverReachesTheRealAccount` (subpruebas `read/...`, cliente real que falla ante cualquier llamada de cuenta u órdenes) | cubierto |
| Respuesta de una orden de papel | `product_gates_test.go::TestPaperEnvelope` ("served call is marked paper with the assumptions"); `paper_scenarios_test.go::TestPaperScenarioQueriesAreMarkedAndLiveProfileIsNotServedByPaper` | cubierto |
| Tope por orden en papel | `paper_test.go::TestSentinelAppliesCapAndKillSwitchInPaper`; `paper_scenarios_test.go::TestPaperScenarioRiskCapAndKillSwitchApplyToPaperOrders` | cubierto |
| Parada global activa en papel | las mismas dos pruebas | cubierto |
| Tipo de orden no soportado | `paper_test.go::TestUnsupportedOrdersNeverReachTheRealClient`; `product_gates_test.go::TestPaperEnvelope` (`PAPER_UNSUPPORTED`) | cubierto |
| Pérdida que consume el margen | `paper_test.go::TestFuturesMarginAndLiquidation` (fill con `Liquidation`, PnL = -margen); `paper_scenarios_test.go::TestPaperScenarioLeveragedPositionIsLiquidated` | cubierto |
| Reinicio por el operador | `paper_test.go::TestResetClosesPositionsCancelsOrdersKeepsHistory`; `paper_scenarios_test.go::TestPaperScenarioResetReturnsTheAccountToItsInitialBalance` | cubierto |
| Reinicio por un agente | `internal/productapi/productapi_test.go::TestPaperResetIsOperatorOnly` | cubierto |

## product-console

Las pruebas de la consola son vitest en `anka-arena/src` (entorno node, sin DOM: componentes con `renderToStaticMarkup` y funciones puras). La comprobación en navegador (9.8) sigue abierta.

| Escenario | Cobertura | Estado |
|---|---|---|
| Navegación en modo producto | `anka-arena/src/lib/productMode.test.ts` ("the nav lists exactly the allowed screens") | cubierto |
| Ruta ajena al producto | `anka-arena/src/proxy.test.ts` (nuevo: con `NEXT_PUBLIC_EDITION=product` el proxy reescribe a `/_edition-not-found` y las pantallas del producto no); `productMode.test.ts` (lista de rutas permitidas) | cubierto |
| Contraseña sin establecer | `anka-arena/src/components/product/product-screens.test.tsx` ("password not set shows the server command and no input") | cubierto |
| Bloqueo por intentos | `product-screens.test.tsx` ("lock message shows the wait from Retry-After"); `anka-arena/src/lib/product-api.test.ts` ("429 carries the Retry-After seconds", "parses Retry-After and formats the wait") | cubierto |
| Creación | `product-screens.test.tsx` ("created notice shows the value once with copy and the warning") | cubierto |
| Revocación | `product-api.test.ts` ("active means neither revoked nor expired", "revoke flow"); `product-screens.test.tsx` ("lists tokens without values and revoked ones as inactive") | cubierto |
| Revocación cancelada | `product-api.test.ts` ("revoke flow": cancelar cierra la confirmación); `product-screens.test.tsx` ("shows the inline confirmation only for the pending token"). El `onCancel` de `AgentTokensScreen.tsx` solo despacha `cancel` y `revokeAgentToken` solo se llama desde `confirmRevoke`, pero no hay una prueba con DOM que pulse "cancelar" y compruebe que no sale ninguna petición | parcial |
| Perfil real sin licencia | `product-api.test.ts` ("a live token is blocked only when the license is known and not Pro"); `internal/localauth/localauth_test.go::TestLiveTokenRequiresLicense` (el servidor responde 402); `boot_product_test.go::TestProductBinaryBoot` (paso 3) | cubierto |
| Aviso de caducidad | `product-screens.test.tsx` ("warns at 14 days or fewer with a link on home, silent above"); `product-api.test.ts` ("warns at 14 days or fewer and not before"); `LicenseScreen.tsx:51` y `LicenseParts.tsx:70` montan el aviso en licencia e inicio | cubierto |
| Licencia rechazada | `product-api.test.ts` ("reads the rejection reason from a 400 body"); servidor: `internal/license/license_test.go::TestInstallKeepsPreviousOnInvalid` (conserva la anterior) | cubierto |
| Pantalla bloqueada | `product-screens.test.tsx` ("blocks with the reason and a link to the license without Pro", "stays blocked when the license cannot be checked"); `productMode.test.ts` ("only login, health and license are exempt") | cubierto |
| Parada de emergencia sin licencia | `product-screens.test.tsx` ("LicenseBlocked mentions the emergency stop"); `app/[locale]/layout.tsx:93` monta `KillSwitchModal` fuera de `ProductGuard`; servidor: `product_gates_test.go::TestLicenseGate` ("live safety, no license"). Falta la comprobación en navegador (9.8) de que el botón actúa sin licencia | parcial |
| Dinero real habilitado | `product-screens.test.tsx` (`LiveModeIndicator`: `live`, `off`, `unknown`); `app/[locale]/layout.tsx:78` monta `LiveModeBadge` en la cabecera común. No hay prueba que recorra todas las pantallas; verificación por lectura del layout, pendiente 9.8 | parcial |
| Estado desconocido | `product-screens.test.tsx` (`LiveModeIndicator` `unknown` muestra `mode_unknown`); `product-api.test.ts` ("only a boolean live_enabled yields a known mode") | cubierto |
| Paridad de claves | `anka-arena/src/lib/product-i18n.test.ts` ("es and en have identical key sets over the whole files", "every literal and templated key the product components use exists") | cubierto |

## product-distribution

| Escenario | Cobertura | Estado |
|---|---|---|
| Ruta no incluida | manual: `scripts/product/export.sh` ejecutado el 2026-11-16 (`SKIP_TIDY=1 ALLOW_DIRTY=1`, 1251 archivos); del directorio `cmd/` (30+ subdirectorios en el origen) solo salieron `trading-mcp-server` y `xsec-sim`, porque lo que no figura en `scripts/product/manifest.txt` no se copia | cubierto |
| Historial | manual: la exportación copia archivos con `cp` y no crea ni copia `.git`; `find <destino> -name .git` sin resultados en la exportación del 2026-11-16 | cubierto |
| Exportación repetible | manual: dos exportaciones del mismo commit dieron el mismo digest (`b4f438396567e8bf5e41b75d0807051a6dabc69068fe7f45c7ae75b66a73c62b`); `export.sh` imprime ese digest en su última línea | cubierto |
| Credencial detectada | `scripts/product/secrets_gate_test.sh` (ejecutado, pasa: clave PEM, AWS, JWT, GitHub, KuCoin, asignaciones de alta entropía; comprueba archivo, línea y regla y que el valor nunca aparece en la salida) | cubierto |
| Binario detectado | `scripts/product/secrets_gate_test.sh` (ELF, Mach-O, base de datos, `node_modules`, `bin/`) | cubierto |
| Compilación completa | `scripts/product/verify_export.sh` ejecutado el 2026-11-16 dentro de un contenedor limpio `golang:1.25-bookworm` con solo la exportación montada: compila, pasa `go vet` y no depende de `iatb`/`platform` con `product` y `product,pro` | cubierto |
| Pruebas | mismo contenedor limpio: `go test -short` con `product` y `product,pro` (incluye `boot_product_test.go` con GoCryptoTrader simulado); sin exchange ni credenciales | cubierto |
| Integración continua | `scripts/product/templates/ci.yml` (compila, `go vet` y pruebas con `product` y `product,pro`) se instala como `.github/workflows/ci.yml` en la exportación; falta el repositorio publicado donde dispararla (tareas 10.11 y 11.6) | operador |
| Instalación limpia | `scripts/product/install_test.sh` Parte B (ejecutada y pasó: sano en menos de 5 min con servidor, GoCryptoTrader, Postgres y consola) | cubierto |
| Secretos por instalación | `scripts/product/install_test.sh` Parte A y Parte B (dos instalaciones con todos los secretos distintos) | cubierto |
| Reinstalación | `scripts/product/install_test.sh` Parte A (el `.env` no cambia byte a byte) y Parte B (la sesión del operador y los datos siguen tras reinstalar) | cubierto |
| Primer uso | `install_test.sh` Parte B (token `paper`, orden de papel ejecutada sin credenciales de exchange); `cmd/trading-mcp-server/boot_product_test.go::TestProductBinaryBoot` (etiqueta `product`) | cubierto |
| Conexión de un cliente MCP | `install_test.sh` Parte B (`tools/list` con el token recién creado devuelve las 78 tools del perfil); la guía `docs/product/guide/mcp-clients.md` es la que sigue la persona (verificación manual de su contenido pendiente de 11.7) | cubierto |
| Número de tools | `internal/mcp-trading/toolinventory/catalog_test.go::TestToolCatalog`, `inventory_test.go::TestToolInventory`, `classification_test.go::TestClassificationDoc` (el número de `tools.md`, `tool-inventory.md` y `tool-classification.md` es el del registro: 105); la guía exportada no repite ninguna cifra | cubierto |
| Límites declarados | manual: `docs/product/guide/limits.md` (KuCoin único, mercados, límites del simulador de papel y backtest) y la advertencia de que no se garantizan ganancias en `guide/README.md`, `limits.md` y `paper.md` | cubierto |
| Componentes Pro | `scripts/product/export.sh` copia `LICENSE-PRO` como `LICENSE` en `anka-arena/` y `cmd/xsec-sim/`; verificado en la exportación del 2026-11-16 | cubierto |
| Dependencias redistribuidas | `scripts/product/gen_notices.sh` (lo ejecuta `export.sh`): `THIRD_PARTY_NOTICES.md` con todos los módulos Go enlazados (584 entradas con los paquetes npm), texto completo de cada licencia, y falla si falta alguna | cubierto |
| Consulta de versión | `cmd/trading-mcp-server/product_wiring_test.go::TestVersionLineCarriesVersionCommitAndEdition` (nuevo: `-version` imprime versión, commit y edición); `internal/mcp-trading/server/routes_audit_test.go::TestPublicHealthExposesOnlyBuildFields`; `boot_product_test.go::TestProductBinaryBoot` (`/health` con `version`, `commit`, `edition`) | cubierto |

## product-licensing

| Escenario | Cobertura | Estado |
|---|---|---|
| Sin licencia | `internal/license/license_test.go::TestExpiredAfterInstallAndNoLicense`; `internal/mcp-trading/server/product_gates_test.go::TestLicenseGate` (sin licencia: `live` abre y controla con `LICENSE_REQUIRED`, `paper` y lectura siguen) | cubierto |
| Con licencia Pro válida | `license_test.go::TestStates` ("valid far"); `product_gates_test.go::TestLicenseGate` ("live increase, licensed"), `::TestLiveGate` (sujeto a la activación explícita) | cubierto |
| Verificación sin red | `internal/license/nonet_test.go::TestNoNetworkDuringVerificationCycle`, `::TestPackageImportsNoNetwork` | cubierto |
| Licencia manipulada | `license_test.go::TestParseDocuments` (carga útil sustituida: `ReasonBadSignature`); `::TestInstallKeepsPreviousOnInvalid` | cubierto |
| Licencia firmada con otra clave | `license_test.go::TestParseDocuments` ("foreign key"), `::TestInstallKeepsPreviousOnInvalid` | cubierto |
| Periodo de gracia | `license_test.go::TestStates` ("grace 5 days": Pro sigue y 9 días de gracia restantes) | cubierto |
| Caducada | `license_test.go::TestStates` ("expired 20 days": Community), `::TestExpiredAfterInstallAndNoLicense` | cubierto |
| Aviso previo | `license_test.go::TestStates` ("expiring soon", "exactly 14 days") | cubierto |
| Cierre con licencia caducada | `product_gates_test.go::TestLicenseGate` ("live reduce, no license: closing stays possible") | cubierto |
| Apertura con licencia caducada | `product_gates_test.go::TestLicenseGate` ("live increase, no license": `LICENSE_REQUIRED`) | cubierto |
| Parada de emergencia sin licencia | `product_gates_test.go::TestLicenseGate` ("live safety, no license") | cubierto |
| Reloj atrasado | `license_test.go::TestClockSkew` (3 días atrás: Community y `clock_skew`, persistido tras reinicio) | cubierto |
| Ajuste horario menor | `license_test.go::TestClockSkew` (1 hora atrás: el estado no cambia) | cubierto |
| Instalación en caliente | `license_test.go::TestHotInstallAndSources`; `internal/license/httpapi/handlers_test.go::TestLicenseEndpoints` | cubierto |
| Instalación de una licencia inválida | `license_test.go::TestInstallKeepsPreviousOnInvalid`; `httpapi/handlers_test.go::TestLicenseEndpoints` | cubierto |
| Consulta del estado | `license_test.go::TestStatusNeverContainsDocument`; `internal/productapi/productapi_test.go::TestProductStatusShapeAndLicenseHook`; `httpapi/handlers_test.go::TestLicenseEndpoints` | cubierto |
| Clave de relleno | `scripts/product/export.sh` ejecuta `TestReleaseKeyIsNotPlaceholder` y falla con la clave de relleno salvo `ALLOW_PLACEHOLDER_KEY=1` (exportación de desarrollo); la plantilla de CI también la ejecuta. Verificado: la exportación sin la variable falla. Sustituir la clave: tarea 7.13/11.5 (operador) | cubierto |
| Búsqueda de la clave privada | `scripts/product/secrets_gate_test.sh` (regla `private-key-pem`; el gate corre sobre cada exportación); no se inspeccionaron imágenes Docker en esta tarea y el par de claves aún no existe (7.13) | parcial |
| Emisión manual | `cmd/license-server/issue_test.go::TestIssueProducesAcceptedLicenseAndLedger` (la licencia emitida la acepta el verificador del producto y queda en el libro); `::TestKeygenModesAndNoOverwrite` | cubierto |
| Vigencia inválida | `cmd/license-server/issue_test.go::TestIssueRejectsBadInput`; `internal/license/license_test.go::TestSignRejectsBadInput` | cubierto |
| Revocación sin credencial | `cmd/license-server/handler/admin_test.go::TestStateChangingRoutesRequireAdminToken` | cubierto |
| Arranque sin credencial configurada | `cmd/license-server/issue_test.go::TestRequireAdminToken`; `cmd/license-server/handler/admin_test.go::TestAdminAuthAcceptsRightTokenAndFailsClosedWhenEmpty` | cubierto |
| Tráfico de licencia | `internal/license/nonet_test.go::TestNoNetworkDuringVerificationCycle` | cubierto |

## secure-defaults

| Escenario | Cobertura | Estado |
|---|---|---|
| Endpoint de depuración | `internal/mcp-trading/server/routes_audit_test.go::TestDebugRoutesAreGone` (`/debug`, `/test`, `/simple-test`: 404 y sin eco de la cabecera) | cubierto |
| Métricas sin token | `routes_audit_test.go::TestMuxRoutesRequireAuthUnlessListed` (todas las rutas montadas, incluida `/metrics`, responden 401 sin token salvo las de `routepolicy.Open`); `internal/api/v1/routes_audit_test.go::TestRESTRoutesRequireAuthUnlessListed`; `internal/gateway/routes_audit_test.go::TestGatewayRoutesRequireAuthUnlessListed` | cubierto |
| Canal en tiempo real sin token | `TestMuxRoutesRequireAuthUnlessListed` (`/ws` con el hub montado); `internal/api/v1/routes_audit_test.go`; `internal/gateway/routes_audit_test.go` | cubierto |
| Salud sin token | `routes_audit_test.go::TestPublicHealthExposesOnlyBuildFields` (exactamente `status`, `version`, `commit`, `edition`); `boot_product_test.go::TestProductBinaryBoot` | cubierto |
| Cabecera de tenant ajena | `internal/trading/middleware/tenant_headers_authority_test.go::TestTenantHeadersIgnoredWithoutGatewayToken` ("no gateway token", "wrong gateway token", "no gateway token configured") | cubierto |
| Petición a través del gateway | `tenant_headers_authority_test.go::TestTenantHeadersIgnoredWithoutGatewayToken` ("valid gateway token: headers honoured"); `internal/trading/middleware/kong_jwt_middleware_test.go::TestKongJWTMiddleware_GatewayToken` | cubierto |
| Configuración por defecto | `internal/mcp-trading/config/product_config_test.go::TestShippedProductConfigIsLocalAndLiveDisabled` (nuevo: `configs/product/config.yaml` escucha en local), `::TestBindAddressOverridesHost`, `::TestIsLocalBind`; `scripts/product/install_test.sh` Partes A y B (puertos ligados a 127.0.0.1) | cubierto |
| Exposición explícita | `cmd/trading-mcp-server/product_wiring_test.go::TestWarnIfExposed` (nuevo: un aviso con el puerto para `0.0.0.0`, vacío y LAN; ninguno para loopback); el listener MCP avisa en `internal/mcp-trading/server/server.go:386` con la misma lógica `IsLocalBind` | cubierto |
| Autenticación desactivada con dinero real | `cmd/trading-mcp-server/product_wiring_test.go::TestJWTAuthGuard` ("jwt off, real money, live": fatal con motivo) | cubierto |
| Autenticación desactivada en un entorno de pruebas | `TestJWTAuthGuard` ("jwt off, sandbox": arranca con aviso) | cubierto |
| Instalación por defecto y token real | `internal/mcp-trading/server/product_gates_test.go::TestLiveGate` ("live increase, disabled": `LIVE_TRADING_DISABLED`); `cmd/trading-mcp-server/live_gate_wiring_test.go::TestLiveOrderGate`; `internal/gct/order_gate_test.go::TestEvaluateOrderRisk_LiveGate`; `product_config_test.go::TestShippedProductConfigIsLocalAndLiveDisabled` (nuevo: `trading.live_enabled: false`) | cubierto |
| Activación por el operador | `product_gates_test.go::TestLiveGate` ("live increase, enabled"), `::TestLicenseGate` ("live increase, licensed"). La orden real mínima con licencia es la tarea 11.7 (operador) | cubierto |
| Origen no configurado | `cmd/trading-mcp-server/cors_wiring_test.go::TestCORSOriginsFor`; `internal/gateway/cors_test.go::TestGatewayCORSUsesConfiguredList` ("unlisted origin gets no CORS headers", "empty list never sends a wildcard") | cubierto |
| Registro de una llamada con credenciales | `product_gates_test.go::TestSecretsAreRedactedInLogs`, `::TestAuditLogRedactsSecrets`; `internal/redact/redact_test.go::TestMapRedactsSensitiveKeysAtAnyDepth`; `internal/middleware/log_scrubber_test.go` | cubierto |

## standalone-auth

| Escenario | Cobertura | Estado |
|---|---|---|
| Consulta del modo | `internal/localauth/localauth_test.go::TestModeEndpoint` (solo `mode` y `password_set`, `len(m)==2`) | cubierto |
| Modo externo sin cambios | `localauth_test.go::TestExternalModeUnavailable` (endpoints locales "no disponible"); `internal/localauth/authority_test.go::TestExternalModeIgnoresLocalTokens`; `internal/trading/middleware/tenant_headers_authority_test.go::TestLocalAuthorityReplacesTheSecretPerRequest` ("external mode") | cubierto |
| Instalación recién hecha | `localauth_test.go::TestLoginBeforePasswordSet` | cubierto |
| Contraseña corta | `localauth_test.go::TestShortPasswordRejectedAndNothingSaved`, `::TestPasswordStoredOnlyAsHash` | cubierto |
| Recuperación de acceso | `localauth_test.go::TestRecoveryInvalidatesSessions`, `::TestRunOperatorCommand`, `::TestLoginRightAfterCLISetPassword` (solo por el subcomando del servidor, sin endpoint) | cubierto |
| Contraseña correcta | `localauth_test.go::TestLoginSuccessSession` (caduca a las 12 h como máximo) | cubierto |
| Contraseña incorrecta | `localauth_test.go::TestWrongPasswordGenericAndAudited` | cubierto |
| Bloqueo por intentos | `localauth_test.go::TestLockoutAfterFiveFailures` (429 incluso con la contraseña correcta, libera a los 15 min), `::TestFailuresOutsideWindowDoNotLock`, `::TestGlobalFailuresSlowDownButNeverLockOut` | cubierto |
| Creación de un token | `localauth_test.go::TestCreateListRevokeAndExpiry` (valor una vez, 90 días por defecto) | cubierto |
| Caducidad fuera de rango | `localauth_test.go::TestCreateListRevokeAndExpiry` (366 días: 400; 365: 201) | cubierto |
| Token caducado | `localauth_test.go::TestCreateListRevokeAndExpiry` (a los 91 días `CheckToken` falla); `authority_test.go::TestAuthorityDrivesTheSharedValidators` | cubierto |
| Listado | `localauth_test.go::TestCreateListRevokeAndExpiry` (nombre, perfil, fechas, último uso y nunca el valor), `::TestLastUsedCoalesced` | cubierto |
| Revocación | `localauth_test.go::TestRevocationImmediateLocalAndUnder5sCrossProcess` (inmediata local; otro proceso en menos de 5 s), `::TestRevokeUnknownIs404` | cubierto |
| Agente intenta crear un token | `localauth_test.go::TestAgentTokenCannotManageTokens` (perfiles `read`, `paper` y `live`: 403); `internal/mcp-trading/server/agent_scope_test.go::TestAgentTokenOnlyReachesMCP` | cubierto |
| Primer arranque | `localauth_test.go::TestSecretFirstStartPermsAndNoLeak`, `::TestLooseSecretPermsAreTightened` | cubierto |
| Rotación del secreto | `localauth_test.go::TestRestartKeepsTokensAndRotationInvalidates` | cubierto |
| Reinicio | `localauth_test.go::TestRestartKeepsTokensAndRotationInvalidates` | cubierto |

## Pruebas añadidas en esta tarea

| Prueba | Escenario que cubre |
|---|---|
| `cmd/trading-mcp-server/product_wiring_test.go::TestWarnIfExposed` | secure-defaults / Exposición explícita |
| `cmd/trading-mcp-server/product_wiring_test.go::TestVersionLineCarriesVersionCommitAndEdition` | product-distribution / Consulta de versión |
| `internal/mcp-trading/server/permission_startup_test.go::TestAuditToolClassificationLogsAnErrorNamingTheTool` | mcp-tool-permissions / Tool sin clasificar |
| `internal/mcp-trading/server/permission_startup_test.go::TestPermissionDeniedCarriesOnlyToolClassAndProfile` | mcp-tool-permissions / Contenido del rechazo |
| `internal/config/dynamic/manifest/sizing_test.go::TestEnforceCapOnMarketOrdersDefaultFollowsEdition` | order-notional-cap / Despliegue existente e Instalación nueva |
| `internal/mcp-trading/config/product_config_test.go::TestShippedProductConfigIsLocalAndLiveDisabled` | secure-defaults / Configuración por defecto e Instalación por defecto y token real |
| `anka-arena/src/proxy.test.ts` (3 pruebas) | product-console / Ruta ajena al producto |

## Brechas de comportamiento

Las tres brechas detectadas al hacer este mapa (licencia Pro en los directorios Pro, la clave de relleno que no impedía exportar y los avisos de terceros incompletos) quedaron corregidas el 2026-11-16; ver las filas correspondientes.
