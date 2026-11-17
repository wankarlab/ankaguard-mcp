# AnkaArena

**AnkaArena** es la consola web frontend para la Inteligencia Colectiva de Trading que se Auto-Mejora (AnkaAI). Construido para orquestar la creación, validación y escalado de estrategias de trading impulsadas por IA y colaboración humana.

Este proyecto materializa el concepto de "el color es el riesgo" y proporciona transparencia total sobre las decisiones de trading mediante la Explainability API.

## 🛠️ Stack Tecnológico

- **Framework**: Next.js 15 (App Router)
- **UI/Estilos**: React 19, Tailwind CSS 4, CSS variables custom
- **Estado Global**: Zustand
- **Data Fetching**: SWR (polling REST) + WebSockets (tiempo real)
- **Autenticación**: JWT
- **Despliegue**: Standalone build mode

## 🏛️ Arquitectura y Órganos Principales

El proyecto se estructura alrededor de 4 "órganos" fundamentales:

1. **El Flywheel Vivo (`/flywheel`)**: La home. Un dashboard dinámico que muestra el ciclo de vida completo: Educación → Generación → Gate → Shadow → Real.
2. **Visor de Explicabilidad (`ExplainDrawer`)**: Un árbol de decisión desplegable que desglosa el "por qué" detrás de cada trade (regime gate, ML gate, trend filter, voto LLM).
3. **Teatro del Gate (`/gate`)**: Interfaz de validación walk-forward OOS (Out-Of-Sample) donde las estrategias demuestran su *edge* real.
4. **Consola Kill-Switch (`KillSwitchModal`)**: Control de seguridad en 3 anillos (Spec, Tenant, Global) con fricción proporcional al riesgo (biometría, confirmación escrita).

## 🎨 Lenguaje Visual

- **Estética**: Terminal oscura, alta densidad de datos, diseñada como un instrumento profesional.
- **Color = Riesgo**:
  - 🟦 **Azul (Paper / Shadow)**: Sin dinero real; decisión sin envío de orden.
  - 🟨 **Ámbar (Virtual / Micro)**: Torneo / micro-capital real con cap duro.
  - 🟥 **Rojo (Real Escalado)**: Dinero de verdad a escala; requiere confirmaciones extras.

## 🚀 Instalación y Desarrollo

### Requisitos

- Node.js >= 20
- NPM o Yarn
- Backend Go (trading-mcp) corriendo en los puertos `:8081` (REST) y `:10600` (WS/MCP)

### Variables de Entorno

Crear un archivo `.env.local` en la raíz del proyecto (basado en `.env.local.example` si existe) con:

```env
# Ejemplo de variables requeridas
NEXT_PUBLIC_API_URL=http://localhost:8081
NEXT_PUBLIC_WS_URL=ws://localhost:10600/ws
```

### Comandos Disponibles

```bash
# Instalar dependencias
npm install

# Iniciar servidor de desarrollo
npm run dev

# Linting
npm run lint

# Type checking
npx tsc --noEmit

# Build para producción (genera output standalone)
npm run build
```

## 🛡️ Estructura de Seguridad y RBAC

El acceso a la plataforma está gobernado por un sistema RBAC centralizado con 5 roles principales:

- `viewer` (Alumno / Observador)
- `author` (Creador de Specs)
- `reviewer` (Validador en cola adversarial)
- `operator` (Aprobador de promociones a real)
- `admin` / `arbiter` (Gobernanza de políticas)

Ninguna vista muestra "qué" sin acceso al "por qué". Las secciones restringidas muestran claramente los requisitos de KYC y rol necesarios para desbloquearlas.

## 📄 Licencia

Propiedad de RantiPay / AnkaAI. Todos los derechos reservados.
