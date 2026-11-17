# Instalación con Docker

> Estado: `deploy/product/install.sh` y `deploy/product/docker-compose.yml` existen y se verifican con `scripts/product/install_test.sh` (generación de `.env`, reinstalación, secretos distintos). La parte con Docker en marcha (arranque, salud en menos de 5 minutos, orden de papel) está escrita pero **no se ha ejecutado** en este entorno por falta de demonio Docker.

## Requisitos

- Docker instalado (con Compose).
- Una cuenta de KuCoin y sus credenciales API solo si vas a usar dinero real (Pro). Para leer mercado y operar en papel no hacen falta.

## Qué se instala

El `docker-compose` de producto levanta cuatro servicios:

1. **Servidor** del producto (MCP, API REST y salud). Se compila con CGO activado y se ejecuta con un usuario sin privilegios.
2. **GoCryptoTrader**, que conecta con el exchange por gRPC.
3. **Postgres** de configuración (necesario para la configuración en caliente).
4. **Consola web** (solo edición Pro).

Solo se publican en el anfitrión el puerto del servidor y el de la consola, y ambos en la interfaz local (`127.0.0.1`). El servidor escucha por defecto solo en local (`server.bind_address: 127.0.0.1`).

Los puertos se cambian en `.env` con `SERVER_PORT` (10600) y `CONSOLE_PORT` (3002). El servidor solo acepta peticiones de la consola desde los orígenes de `CONSOLE_ORIGINS`; por defecto son `http://localhost:<CONSOLE_PORT>` y `http://127.0.0.1:<CONSOLE_PORT>`. Si abres la consola con otro nombre de host o detrás de un proxy, añade ese origen (separado por comas). Si no, el inicio de sesión responde 403.

## Instalar

1. Desde `deploy/product/`, ejecuta el instalador: `./install.sh` (no arranca nada).
2. Arranca los servicios: `docker compose up -d --build` (con la consola Pro: `docker compose --profile pro up -d --build`).
3. Fija la contraseña del operador: `docker compose exec server trading-mcp-server -config /app/configs/product/config.yaml operator set-password`.

El servidor se compila con las etiquetas de `EDITION_TAGS` (en `.env`: `product,pro` por defecto, `product` para Community). La API REST (:8081) no se publica en el anfitrión; la consola la alcanza por la red interna.

GoCryptoTrader se compila desde el proyecto original en el commit `123d39f` (la instantánea con la que se desarrolló el servidor), así que la primera construcción necesita red hacia GitHub. Las claves de exchange no van en su configuración: el servidor las guarda cifradas (`CREDENTIAL_ENCRYPTION_KEY`).

El instalador:

- Genera un archivo `.env` con **secretos aleatorios propios de esa instalación** (firma de tokens, contraseña de la base de datos, etc.). No usa contraseñas ni secretos por defecto.
- **Nunca sobrescribe** un `.env` existente: puedes volver a ejecutarlo sin perder secretos ni datos.

Criterio de la especificación: tras la instalación limpia el servidor responde sano en menos de 5 minutos. Comprueba la salud con:

```bash
curl -s http://127.0.0.1:10600/health
```

La respuesta incluye `status`, `version`, `commit` y `edition`, y nada más. La versión es la de la release que construiste. Para que `commit` muestre el commit exacto, constrúyelo pasando el commit del clon: `PRODUCT_COMMIT=$(git rev-parse --short HEAD) docker compose up -d --build`.

## Primer uso

1. Establece la contraseña del operador. Se hace con el subcomando `operator set-password` del binario del servidor (es la única vía y exige acceso a la máquina).
2. Inicia sesión como operador (`POST /api/v1/auth/local/login`, o la consola en Pro).
3. Crea un token de agente de perfil `paper` (`POST /api/v1/auth/local/tokens`). Ver [mcp-clients.md](mcp-clients.md).

## Dinero real: desactivado por defecto

Una instalación por defecto **no puede enviar órdenes reales**. Hacen falta las tres llaves a la vez (ver [protections.md](protections.md)):

1. Token de perfil `live`.
2. Licencia Pro válida (`valid` o `grace`), ver [license.md](license.md).
3. `trading.live_enabled: true`, además de credenciales de exchange.

Los topes por orden de la configuración del producto son conservadores y no incluyen credenciales.

## Reinstalar o actualizar

Volver a ejecutar la instalación no sobrescribe secretos ni datos existentes. Dos instalaciones distintas tienen secretos distintos.

## Exportar y verificar el producto

`scripts/product/export.sh <directorio-vacío>` copia solo la lista de `scripts/product/manifest.txt` (archivos versionados), reescribe `go.mod` sin `platform` ni `iatb`, ejecuta la barrera de secretos (`secrets_gate.sh`: nunca imprime valores) y es repetible (mismo resumen para la misma entrada). GoCryptoTrader no se puede usar desde el módulo original fijado (la copia local es una instantánea posterior de upstream, commit 123d39f): se exportan solo los 24 paquetes que el producto importa, sin tests, sin `config.json` ni binarios.

`scripts/product/verify_export.sh <directorio>` compila, analiza y prueba la exportación con ambas ediciones. En un contenedor limpio:

```bash
docker run --rm -v "$PWD/export:/src" -w /src golang:1.25-bookworm \
  bash -c 'apt-get update -qq && apt-get install -y -qq gcc && \
           GOFLAGS=-mod=mod GOPROXY=https://proxy.golang.org scripts/product/verify_export.sh /src'
```
