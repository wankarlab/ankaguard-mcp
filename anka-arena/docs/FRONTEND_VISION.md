---
title: AnkaAI — Visión de Frontend (web + móvil)
doc_id: FRONTEND-VISION
tier: 1
status: review
owner: TBD
created: 2026-06-04
updated: 2026-06-04
supersedes: []
depends_on: [VISION, API-CONTRACT, SEC-PERMISSIONS, SPEC-STRATEGY]
---

# AnkaAI — Visión de Frontend

> Documento de imaginación de producto. Materializa la [VISION v3.0](../00-vision/VISION.md)
> en dos superficies de UI: la app **ankamobil** (Flutter, ya existe) y la **Consola AnkaAI**
> (web, net-new). No es un spec de implementación: es el norte de experiencia que las
> pantallas concretas deberán respetar. Cada claim se ancla a una sección de la VISION.

---

## Tabla de contenidos

1. [El principio rector del diseño](#1-el-principio-rector-del-diseño)
2. [Dos superficies, dos trabajos](#2-dos-superficies-dos-trabajos)
3. [El corazón compartido: 4 órganos](#3-el-corazón-compartido-4-órganos)
4. [La Consola Web: rol por rol](#4-la-consola-web-rol-por-rol)
5. [La app móvil (ankamobil)](#5-la-app-móvil-ankamobil)
6. [Sistemas transversales](#6-sistemas-transversales)
7. [Lenguaje visual](#7-lenguaje-visual)
8. [Lo que la UI NO promete (honestidad)](#8-lo-que-la-ui-no-promete-honestidad)
9. [Mapa pantalla → endpoint](#9-mapa-pantalla--endpoint)

---

## 1. El principio rector del diseño

La VISION dice algo que define TODA la UI: *"la transparencia no es un feature, es el
combustible"* y *"una sola fuente de verdad — la Explainability API — con cuatro consumidores"*
(VISION §2.2, §11). De ahí salen las dos reglas no negociables del frontend:

> **Regla 1 — El "por qué" siempre a un toque.** Ninguna pantalla muestra un *qué* (un PnL, un
> ranking, una promoción, un rechazo) sin dar acceso de un toque al *por qué*. Cada número es un
> link a su árbol de explicación.

> **Regla 2 — El color es el riesgo** (VISION §12.1, §12.6). Tres estados de capital con código
> de color consistente en todo el producto:
> - 🟦 **PAPER / SHADOW** (azul frío) — no hay dinero; decide pero no envía orden.
> - 🟨 **VIRTUAL / MICRO** (ámbar) — torneo / micro-capital real con cap duro.
> - 🟥 **REAL ESCALADO** (rojo) — dinero de verdad a escala; siempre con fricción extra
>   (biometría, confirmación escrita).

---

## 2. Dos superficies, dos trabajos

No es la misma app en dos tamaños. El móvil es **reactivo** (te llega algo, decides); la web es
**generativa** (vas a producir algo).

| | **ankamobil** (móvil, ya existe) | **Consola AnkaAI** (web, net-new) |
|---|---|---|
| Metáfora | El **copiloto de bolsillo** | El **taller / sala de control** |
| Trabajo | Monitorear, aprobar, recibir alertas, frenar, aprender en micro-dosis | Crear, validar, gobernar, enseñar, asignar capital |
| Densidad | *Glanceable*, una decisión por pantalla | Multi-panel, data-densa, atajos de teclado |
| Driver | Push (FCM/APNs) + biometría | Sesión larga, monitores grandes |
| Persona | Trader, copiador, alumno | Autor, revisor, operador, árbitro, educador, admin |

---

## 3. El corazón compartido: 4 órganos

Estos cuatro componentes son el ADN del producto y viven en ambas superficies. Todo lo demás se
construye alrededor.

### 3.1 🌀 El Flywheel Vivo (la home)

No un dashboard de KPIs sueltos: el diagrama de VISION §5, pero **animado y real**. Las cinco
vueltas giran a velocidad proporcional a su métrica real.

```
        📚 EDUCACIÓN ──────► 🧠 PERSONAS+IAs
           ▲                      │
           │                      ▼
    🏦 CAPITAL          ⚙️ GENERACIÓN de Specs
       ▲    ▲                     │
       │    │                     ▼
       │  💰 REAL ◄── 👻 SHADOW ◄── 🛡️ GATE
       │    │            ▲          │ (mata 90%)
       └────┴──────► 📈 feature_store ◄┘

   Specs certificadas hoy: 1     AUM-Certificado: $XXX
   Edges vivos: 3/47 pares       Tasa de paso del gate: 8%
```

El gate **pulsa** cuando rechaza una Spec (es el único punto de frenado del flywheel — VISION
§5). Tocas cualquier nodo → entras al subsistema. La **North Star** (§15) — AUM-Certificado —
vive arriba, descompuesta en su cadena de adquisición: `educadores × alumnos/cohorte × tasa de
conversión a Spec certificada × AUM medio por Spec`.

### 3.2 🔍 El Visor de Explicabilidad (la joya)

La *Explainability API* con sus cuatro consumidores se materializa en **un solo componente
reutilizable** que aparece en todos lados (un trade del historial, un replay educativo, una
decisión rechazada). Es un árbol de decisión desplegable:

```
TRADE #4471 · TON-USDT · LONG · cerrado +$0.83
└─ ¿Por qué entró?
   ├─ ✅ Regime gate: TRENDING (conf 0.78)        [ver datos]
   ├─ ✅ ML gate (GBM 60m): prob 0.64 → coincide LONG
   ├─ ✅ Trend filter EMA50 1h: alcista
   ├─ 🟡 Voto LLM: "pullback sano sobre soporte"  [ver prompt]
   └─ Tamaño: $2.00 (cap de testing) · SL -1.2% · TP +0.9%
└─ ¿Qué pasó?
   ├─ MFE +1.1% · MAE -0.3%
   ├─ Salida: trailing stop re-colocado en exchange
   └─ Tracking error vs shadow: 0.04% ✅
└─ ¿Qué aprendió el sistema? → feature_store fila #2,391,556
```

El mismo componente, con otro *skin*, es el material didáctico (§11.1: cada trade cerrado es un
replay narrado automáticamente). Un botón "¿el sistema se equivocó aquí?" alimenta el feedback
loop — la UI hace literal el principio de §2.2: *nadie más usa su propia explicabilidad como
dato de entrenamiento de su propio modelo*.

### 3.3 🛡️ El Teatro del Gate (validación walk-forward visible)

El validation gate es soberano (principio #1). La UI lo dramatiza para que sea **legible e
incorruptible a la vista**:

```
VALIDACIÓN OOS · Spec "mean-revert-ranging-v3"
Ventanas walk-forward:  ▮▮▮▯▮▮▯▮▮▮   7/10 verdes (umbral ≥60%)
                        └──── train ────┘└─ OOS ─┘
PF OOS: 1.42  (umbral ≥1.30) ✅
Consistencia: 70%            ✅
Robustness score: 0.68       [pesos v1 gobernados ▸]
Cost model: fees KuCoin reales aplicados ✅

VEREDICTO: CERTIFIED · TTL 14 días  →  [Promover a Shadow]
```

Muestra explícitamente que **penaliza overfitting** (§7.1: prefiere "PF 1.4 estable en 9/10" a
"PF 3.0 en 5/10"). Los pesos del robustness score son consultables (ADR-0008, gobernados en DB)
pero **no editables desde aquí** — eso vive en gobernanza, con sign-off.

### 3.4 🚨 La Consola Kill-Switch (3 anillos, siempre a un toque)

De §12.2. En móvil es un widget de pantalla de inicio y un botón rojo persistente.

```
        ┌─────────────────────────┐
        │   ⭕ ANILLO 0 · GLOBAL    │  ← cierra TODO, todos los tenants
        │  ┌───────────────────┐  │
        │  │ ⭕ ANILLO 2·TENANT  │  │  ← aísla un usuario
        │  │ ┌───────────────┐ │  │
        │  │ │⭕ ANILLO 1·SPEC│ │  │  ← congela una estrategia
        │  │ └───────────────┘ │  │
        │  └───────────────────┘  │
        └─────────────────────────┘
   Global requiere: biometría + confirmación escrita
```

El kill-switch global **vive fuera del proceso del bot** (§12.2): la UI puede mostrarlo *caído* y
aun así ofrecer el botón nuclear (🌙 watchdog revoca-keys cuando exista). Cada accionamiento queda
firmado en el audit trail.

---

## 4. La Consola Web: rol por rol

La VISION es *role-based* (viewer/author/reviewer/operator/arbiter/admin + educador + copiador +
LP — SEC-PERMISSIONS). La web es **un workspace que se reconfigura según rol y nivel KYC**. Lo que
no puedes hacer no aparece gris: aparece **con su puerta** ("Promover a real requiere KYC nivel 2
+ 30 días skin-in-the-game").

### 4.1 👤 Viewer / Alumno → La Academia

Motor de adquisición (P5). No cursos genéricos: **replays explicados generados desde
`trade_history` + Explainability API**.

- **Feed de replays** estilo carrete: cada tarjeta es un trade real narrado. Filtrable por
  régimen, par, win/loss.
- **Anti-survivorship obligatorio** (§11.1, §12.3): el feed mezcla losers y estrategias
  retiradas; "ver solo ganadores" muestra disclaimer permanente.
- **Cursos por nivel** que culminan en "genera tu primera Strategy Spec candidata". El graduado
  pasa viewer → author.
- Banner permanente y por defecto (§12.6): **"Rendimiento pasado ≠ futuro · Esto NO es consejo
  financiero"**.

### 4.2 ✍️ Author → El Spec Studio

El contrato de entrada (§7.1). Aquí se crea la Strategy Spec declarativa — **nadie escribe Go**
(elimina ~90% del riesgo).

```
┌─ SPEC STUDIO ───────────────────────────────────────────┐
│ Editor visual (bloques)      │  Vista YAML (read-only)   │
│ ┌─────────────────────────┐  │  name: mean-revert-v3     │
│ │ ENTRADA                  │  │  regime: [RANGING]        │
│ │  cuando RSI < 30         │  │  entry:                   │
│ │  y régimen = RANGING     │  │    rsi: {lt: 30}          │
│ │ SALIDA                   │  │  exit:                    │
│ │  TP +0.9% · SL -1.2%     │  │    tp_pct: 0.9 ...        │
│ └─────────────────────────┘  │  content_hash: a3f9... 🔒 │
│                                                           │
│ [ Backtest rápido ]  [ Enviar al Gate ]                  │
│ ⚠️ Skin in the game: deberás correrla 30 días en TU      │
│    cuenta antes de que sea copiable.                     │
└───────────────────────────────────────────────────────────┘
```

- El **content_hash** (`spec.go: ContentHash()`) se muestra como sello — la Spec es inmutable y
  content-addressed.
- Dos modos: editor de bloques (alumnos graduados) y YAML directo (power users e IAs vía API).
- Panel **IA copiloto**: el LLM sugiere mutaciones *dirigidas por datos* (§9.2). La IA propone, tú
  envías al gate. La IA jamás califica su propia Spec (principio #1).

### 4.3 🔬 Reviewer → La Cola Adversarial

El diferenciador de §10.5: la comunidad es **fuzzer del gate**.

- Cola de candidatas con incentivo invertido: ganas reputación prediciendo qué Specs **fallarán**
  y destapando **bugs del propio gate**.
- Botones "Esto huele a overfitting" / "Creo que el gate la rechazó mal" (apelación que, si
  destapa un bug real como el de orden de velas newest-first, sube tu reputación de revisor).
- **Ledger de Reputación** público y auditable — no es un like, es función de aciertos medidos.

### 4.4 🎚️ Operator → Aprobaciones de Promoción

Quien firma el salto a dinero real (peldaño 3 de §12.1).

- Cola de Specs que sobrevivieron shadow y piden micro-capital real.
- **Conflicto de interés codificado** (§10.5): si figuras como autor, "Aprobar" está **bloqueado
  por código**. Requiere quórum de N operadores independientes sobre cierto AUM.
- Cada promoción muestra el **tracking error shadow→real** y arma el demote automático.

### 4.5 ⚖️ Arbiter → El Tribunal de Apelaciones

Revisa rechazos del gate y disputas. Vista de expediente: la Spec, su veredicto, el árbol de
explicabilidad, el historial de apelación. Resoluciones firmadas → audit trail.

### 4.6 🛠️ Admin / Gobernanza → Sala de Políticas

Principio: **se gobiernan políticas, NUNCA resultados** (§10.5).

- Sliders de umbrales del gate (PF mínimo, % verde, drift máx) y pesos del robustness score
  (ADR-0008) — con **sign-off de risk-owner** (EV-13) y todo cambio al audit trail firmado.
- **Capa 0 inmutable visible**: lista de paths (`.ai-immutable-paths`, ADR-0005) mostrada como
  "zona prohibida para la IA" — risk/, validator.go, risk_limits. Ni el admin los toca sin PR
  humano + 2 aprobaciones.
- Panel de tiers de suscripción (Free/Basic/Pro/Enterprise — precios reales de §8.1) y sus límites.

### 4.7 💸 Copiador / Inversor → Marketplace + Fee Ledger

Flujo F2 (§8.3) — con frontera legal en grande.

- **Gremio de Estrategias**: marketplace sobre `/pairs/ranked` pero rankeando *contribuciones*.
  Cada Spec muestra PF/Sharpe OOS, % ventanas verdes, **drawdowns y si fue retirada**
  (anti-survivorship).
- **Copy setup non-custodial**: conectas TUS keys (solo-trade, jamás withdraw —
  `verifyNoWithdraw` modo block). La UI lo deja claro: *"Operamos TU cuenta con TUS llaves. Nunca
  tocamos tus fondos."*
- **Fee Ledger con HWM** (`fee_ledger_repository.go`): el fee solo se cobra sobre PnL *nuevo*
  sobre el máximo histórico.

```
TU FEE LEDGER · período abierto (junio 2026)
HWM anterior: $142.30
PnL del período: $158.90
Sujeto a fee:  $16.60  ×20%  =  $3.32
                              └ 70% autor · 30% Treasury
[Histórico de períodos ▸]
```

- **Gate anti-abuso visible**: una Spec solo aparece copiable con verdict `certified` vigente +
  ≥30 días skin-in-the-game del autor.
- **Disclaimer regulatorio bloqueante** (§8.3): antes del primer copy real, consentimiento
  informado de riesgo auditado (código, no PDF).

### 4.8 🎓 Educador → Dashboard de Cohorte

El educador como motor de capital (§8.6). Su métrica de éxito **NO es horas vistas** sino **Specs
certificadas por cohorte** (§15).

```
COHORTE "Scalping desde cero · ES · junio"
Alumnos: 84 → autores: 31 → Specs certificadas: 4
Conversión a edge: 4.8%   ·   AUM generado: $2,140
Tu bounty: $X + 5% de los fees de esas 4 Specs
```

### 4.9 🌙 LP → NAV del Pool (moonshot, etiqueta roja)

Flujo F4. La pantalla existe pero **gated**: *"Disponible solo en jurisdicciones licenciadas.
Estado: legal-gated."* NAV diario desde `portfolio_snapshots` agregados, fee 2/20. No se enciende
sin licencia (principio #5: default OFF).

---

## 5. La app móvil (ankamobil)

El móvil **no intenta ser el taller**. Hace 5 cosas excepcionalmente bien:

1. **Home glanceable** — "¿cómo va mi dinero ahora?". PnL del día reconciliado contra exchange
   (no SQLite — §8.8), posiciones abiertas con badge de régimen/volTier. Cada posición → árbol de
   explicabilidad.
2. **Aprobaciones por push** (caso estrella). Promoción a real, quórum de operador, alerta de
   drift llegan como push accionable (FCM/APNs + Centrifugo `personal:{user_id}`). Apruebas con
   **biometría** — fricción proporcional al riesgo.
3. **Kill-switch widget** — botón rojo de los 3 anillos sin abrir la app.
4. **Replays-reel** — el feed de §4.1 en formato vertical estilo carrete: un trade real narrado
   por pantalla, swipe al siguiente. Mezcla ganadores y perdedores. Alimenta el embudo de autores.
5. **Alertas y bitácora** — stream de eventos (señal, validación, demote, payout, kill-switch) vía
   `notificationpush`. Como ese servicio no persiste historial (§12.2), la UI lee el **outbox**
   para auditoría de payouts.

---

## 6. Sistemas transversales

- **Badges de régimen y volTier** en cada par (TRENDING/RANGING/BREAKOUT × low/normal/volatile/
  extreme) — del clasificador dinámico ya desplegado.
- **Escalera de capital de 5 peldaños** (§12.1) como componente de progreso: PAPER → SHADOW →
  VIRTUAL → MICRO → ESCALADO, **con flechas de demote visibles** (demote es de primera clase: baja
  sola antes de que un humano reaccione).
- **Audit trail explorer**: feed append-only, firmado, público, de toda promoción/veto/cambio de
  ranking (§10.5). "Por qué subió esta Spec" deja de ser caja negra.
- **Puertas KYC/capital** dibujadas, no escondidas: federadas al Auth de RantiPay (niveles KYC vía
  REST). Lo bloqueado muestra *cómo* desbloquearlo.
- **Multi-exchange honesto**: KuCoin 🟢 vivo, Binance ⚪ `enabled:false`. La UI no finge que el SOR
  existe (es moonshot H3).

---

## 7. Lenguaje visual

- **Web**: estética de **sala de control / terminal** — oscura, densa, monoespaciada para datos,
  atajos de teclado. Instrumento profesional.
- **Móvil**: cálida y respirada para educación; severa y de alto contraste para acciones con
  dinero real.
- **Color = riesgo** (Regla 2): azul=paper/shadow, ámbar=virtual/micro, rojo=real escalado.
- **Disclaimers como ciudadanos de primera clase**, no letra chica (§12.6): "pasado ≠ futuro",
  "no es consejo financiero", consentimiento de riesgo.

---

## 8. Lo que la UI NO promete (honestidad)

Reflejo del realismo de la VISION: el frontend **no dibuja** lo que es moonshot como si existiera.
El Bucle B (IA editando código), el SOR cross-exchange, el pool tokenizado y la custodia de
terceros aparecen **etiquetados, gated y apagados por defecto**. La UI es tan honesta como la
VISION: muestra el cerebro que ya gira y marca con asterisco lo que falta.

---

## 9. Mapa pantalla → endpoint

> Anclaje a [API-CONTRACT](../05-api/API_CONTRACT.md). Endpoints existentes vs. net-new se marcan.

| Pantalla | Endpoint(s) | Estado |
|---|---|---|
| Spec Studio · listar/crear/promover | `/api/v1/specs/*` (incl. `validate`, `promote`) | ✅ vivo (:8081) |
| Fee Ledger (copiador) | `/api/v1/fees/current`, `/fees/history`, `/fees/accrue` | ✅ vivo (:8081) |
| Marketplace / Gremio | `/pairs/ranked` | ✅ existe (rankea pares; extender a contribuciones) |
| Visor de explicabilidad | trade-history reader + Explainability API | 🟡 trade-history ✅; Explainability API net-new |
| Replays-reel (Academia) | `trade_history` + Explainability API | 🟡 net-new sobre datos existentes |
| Alertas / push | `notificationpush` (FCM/APNs) + Centrifugo `personal:{user}` | ✅ reuse |
| Stream en vivo (flywheel, ligas) | WebSocket `/ws` + `internal/iatbws/hub.go` | ✅ existe |
| Audit trail explorer | `internal/mcp-trading/server/audit.go` (extender a lectura pública) | 🟡 base ✅ |
| Kill-switch | `bot.mode=off` / control de engine | 🟡 endpoint de control a exponer |
| Sala de políticas / gate | umbrales en DB dinámica + robustness weights (ADR-0008) | 🟡 lectura ✅, escritura con sign-off |
| KYC / puertas de capital | Auth `GET /api/v1/internal/permissions/:userID` | ✅ reuse |

---

> **Siguiente paso natural**: cuando se priorice una superficie, derivar de aquí los wireframes
> detallados y el contrato de datos por pantalla. La implementación respeta el default OFF
> (principio #5) y el gating por rol/KYC (SEC-PERMISSIONS).
