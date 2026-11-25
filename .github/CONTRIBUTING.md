# Contribuir a Kandel

Usa [Issues](https://github.com/javierv-dev/kandel/issues) para preguntas, errores y propuestas. Para un error, incluye la versión de Kandel (`kandel version`), el sistema operativo, el agente, los pasos para reproducirlo y el resultado esperado. No adjuntes bases de datos, conversaciones, tokens ni logs sin sanear. Si el problema es una vulnerabilidad, no lo publiques en un issue; usa el [canal privado de GitHub](https://github.com/javierv-dev/kandel/security/advisories/new) cuando esté habilitado.

## Preparar y comprobar el cambio

Necesitas Git y Go 1.26 o posterior. Los checks habituales no requieren instalar Kandel en tu agente ni arrancar Ollama o un hub. Si pruebas una integración real, usa memorias de prueba en un directorio `KANDEL_DATA_DIR` separado.

```bash
CGO_ENABLED=0 go build ./...
go vet ./...
gofmt -l internal cmd
go test ./...
```

`gofmt -l` no debe listar archivos. En PowerShell, fija `$env:CGO_ENABLED = '0'` antes del comando `go build ./...`.

## Abrir un pull request

1. Crea un fork, clónalo y trabaja en una rama nueva desde `main`.
2. Adjunta una prueba de regresión si corriges un bug de comportamiento. Si cambias el esquema SQLite, añade una migración sin perder los datos del usuario; consulta las [reglas de versionado y migración](../docs/versionado.md).
3. Sube la rama a tu fork y abre un pull request contra `main` de `javierv-dev/kandel`. Describe qué cambia, por qué y qué comprobaciones ejecutaste. Enlaza el issue si existe.
