# Exportación del producto

Herramientas de `guarded-mcp-product` (grupo 10). Detalle y uso en `docs/product/guide/install.md`.

| Archivo | Para qué |
|---|---|
| `manifest.txt` | Lista de inclusión (solo archivos versionados). Se regenera con `gen_manifest.sh`. |
| `export.sh <dest>` | Copia la lista a un directorio vacío, reescribe `go.mod`, ejecuta la barrera y deja un resumen (digest) repetible. |
| `secrets_gate.sh <dir>` | Barrera: credenciales, `.env` reales, binarios, bases de datos, artefactos. Nunca imprime el valor. |
| `secrets_gate_test.sh` | Autoprueba de la barrera con fixtures generados al vuelo. |
| `verify_export.sh` | Compila, analiza y prueba la exportación. |
| `install_test.sh` | Pruebas de instalación y reinstalación (la parte Docker con `INSTALL_TEST_DOCKER=1`). |
| `templates/ci.yml` | Flujo de CI del repositorio exportado. |
