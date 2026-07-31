# Versionado y releases

Cómo cortar una versión nueva de kandel **sin romper la memoria del usuario ni el contrato con el
agente**. Documento para quien mantiene el proyecto; para instalar, ver [instalacion.md](instalacion.md).

---

## 1. Principios

kandel usa **SemVer** (`MAJOR.MINOR.PATCH`):

- **MAJOR** — cambio incompatible: rompe el esquema de forma no additiva o rompe el contrato con el
  agente (tools MCP, hooks, comandos, variables de entorno).
- **MINOR** — funcionalidad nueva compatible hacia atrás (comando, tool, migración additiva).
- **PATCH** — arreglo de bug sin tocar contratos ni esquema.

La **fuente de verdad de la versión es el git tag** `vX.Y.Z`. El binario no la lleva escrita: se
inyecta al compilar con `-ldflags`. En construcciones locales vale `dev`.

> **Regla de oro:** kandel guarda memoria del usuario que **no se puede tirar**. Ninguna versión
> puede exigir borrar `~/.kandel/kandel.db`. Todo cambio de esquema pasa por una migración additiva.

### Los tres frentes de todo cambio

| Frente | Vive en | Riesgo si se ignora |
|---|---|---|
| **1. Migraciones de esquema** | `internal/adapter/sqlite/migrate.go` | Pérdida de datos del usuario |
| **2. Assets instalados** | `internal/setup/assets/**` | Protocolo del agente desalineado con el binario |
| **3. Cambios de lógica** | Resto del código Go | Regresiones e incompatibilidad entre instancias |

---

## 2. Migraciones de esquema

El esquema SQLite se versiona con `PRAGMA user_version` y una lista **ordenada y append-only** de
migraciones. Al arrancar se aplican, en orden y cada una en su transacción, solo las de `version`
mayor que la actual.

### Reglas innegociables

1. **Additivas, nunca destructivas.** `ADD COLUMN`, `CREATE TABLE`, `CREATE INDEX` sí; `DROP` o
   rename directo sobre datos vivos no.
2. **Append-only.** Solo se añaden entradas al final, con `version` = anterior + 1. **Jamás se edita
   una migración ya publicada**: romperías las bases que ya la aplicaron.
3. **La v1 es idempotente** (`IF NOT EXISTS` en todo). Las posteriores no lo necesitan porque
   `user_version` garantiza que cada una corre una sola vez.

### Cómo añadir una

```go
var migrations = []migration{
	{version: 1, sql: schemaV1},
	{version: 2, sql: `ALTER TABLE memories ADD COLUMN priority INTEGER NOT NULL DEFAULT 0`},
	// la siguiente sería {version: 3, sql: ...}
}
```

Una migración puede llevar varias sentencias separadas por `;` en el mismo string. El bump de
`user_version` lo hace el runner: **no** escribas `PRAGMA user_version` en el SQL.

### Backfill de datos

Rellenar valores derivados (recomputar vectores, normalizar campos) **no** va en el SQL de la
migración: va como paso de servicio aparte (un comando o un barrido del daemon), para no atar el
arranque a operaciones caras o a dependencias externas como Ollama. El SQL solo crea la estructura.

### Cambios destructivos

SQLite tiene `ALTER` limitado. Para cambiar el tipo de una columna, renombrar con migración de datos
o dividir columnas, usa **crear-copiar-swap** dentro de una migración:

```sql
CREATE TABLE memories_new (...nuevo esquema...);
INSERT INTO memories_new (...) SELECT ... FROM memories;
DROP TABLE memories;
ALTER TABLE memories_new RENAME TO memories;
-- recrear índices
```

Sigue siendo additivo desde fuera (no pierde datos) y corre atómico. Casi siempre implica **MAJOR**
si además cambia contratos hacia afuera.

### Pruebas obligatorias

Usa `internal/adapter/sqlite/migrate_test.go` como plantilla. Toda migración nueva exige comprobar:

- Una base nueva termina en la última versión declarada.
- Reabrir una base ya migrada es idempotente y **no pierde datos**.
- El cambio queda visible (por ejemplo, un `INSERT` que usa la columna nueva no falla).
- Ante SQL inválido hay rollback atómico y `user_version` no avanza.

---

## 3. Assets instalados (skill, hooks, MCP)

Los assets van embebidos en el binario (`//go:embed` sobre `internal/setup/assets/**`) y se
despliegan con `kandel install <agente>`: skills, scripts de hook, registro MCP, hooks del agente y
una **marca de versión** en `~/.claude/kandel/VERSION`.

El chequeo `installed_assets` compara esa marca con la versión del binario en ejecución. Si no
coinciden, `kandel doctor` y la tool `mem_doctor` avisan de que hay que reinstalar.

**Consecuencia para releases:** cualquier versión que cambie un asset obliga al usuario a
**reinstalar**. El binario nuevo por sí solo no reescribe los archivos ya puestos en su config.

### Checklist al tocar un asset

1. Edita el archivo bajo `internal/setup/assets/**` (no el desplegado en el home).
2. Recompila: los assets viajan dentro del binario.
3. `go test ./internal/setup/...`.
4. Indica **explícitamente en las notas de la release** que hay que reinstalar y reiniciar el agente.
5. Si cambió el protocolo inyectado o el ruteo de tools/tipos, revisa que `SKILL.md` y
   `internal/port_in/hook/protocol.go` sigan coherentes entre sí.

---

## 4. Cambios de lógica

### Dónde toca cada cambio (arquitectura hexagonal)

La regla de dependencia apunta hacia adentro:

- `internal/domain` — entidades puras. Cambiar un campo suele arrastrar migración (§2).
- `internal/ports` — contratos. Cambiar una firma obliga a actualizar todos los adaptadores.
- `internal/service` — casos de uso; aquí vive la lógica y nace con tests.
- `internal/adapter/**` — SQLite, git, llm, embedding, sync HTTP, clock.
- `internal/port_in/**` — cli, mcp, http, tui. El wiring vive **solo** en `cmd/kandel/main.go`.

### Contratos que rompen compatibilidad (→ MAJOR)

- Renombrar o eliminar una tool MCP, o cambiar sus parámetros obligatorios.
- Cambiar los eventos o el formato de los hooks.
- Renombrar o eliminar un comando o flag de la CLI que alguien pueda estar scripteando.
- Renombrar o eliminar una variable de entorno `KANDEL_*`.
- Cambiar el protocolo HTTP del hub de sync (`internal/hub`, `internal/adapter/httpsync`) de forma
  que dos instancias con versiones distintas dejen de poder sincronizar.

Añadir una tool, comando, flag o variable **opcional** es compatible (→ MINOR).

### Pruebas

`go test ./...` en verde, con tests nuevos donde toque. Todo bug arreglado nace con un test que lo
reproduce.

---

## 5. Decidir el número

| Disparador | MAJOR | MINOR | PATCH |
|---|:---:|:---:|:---:|
| Migración additiva (`ADD COLUMN`, tabla o índice nuevo) | | ✅ | ✅¹ |
| Migración destructiva con cambio de contrato | ✅ | | |
| Ruptura del protocolo de sync | ✅ | | |
| Renombrar o quitar tool MCP, hook, comando, flag o env | ✅ | | |
| Añadir tool, comando, flag o env opcional, o asset nuevo | | ✅ | |
| Cambio compatible de texto de skill o protocolo | | ✅ | |
| Arreglo de bug sin tocar contratos ni esquema | | | ✅ |

¹ PATCH solo si la migración es soporte directo de un fix, no de una feature.

**Ante la duda, sube el nivel.** Una MAJOR de más es barata; una incompatibilidad silenciosa no.

---

## 6. Proceso de release

### 6.1 Antes de taggear

```bash
CGO_ENABLED=0 go build ./...
go vet ./...
gofmt -l internal cmd     # no debe listar archivos
go test ./...             # todo verde
```

- [ ] ¿Cambió el esquema? → migración additiva + tests (§2).
- [ ] ¿Cambió algún asset? → nota de "reinstalar" en la release (§3).
- [ ] ¿Se rompió algún contrato del agente o del sync? → confirma que es MAJOR (§4).
- [ ] Decide `X.Y.Z` con la tabla de §5.

### 6.2 Cortar la versión

```bash
git tag v1.2.0
git push origin v1.2.0
```

El push del tag dispara los jobs de `.github/workflows/ci.yml`:

1. `build-test` — compila, `vet` y tests. Si falla, no se publica nada.
2. `release` — cross-compila con `CGO_ENABLED=0` para `linux/amd64`, `darwin/amd64`, `darwin/arm64`
   y `windows/amd64`, inyectando por `-ldflags`
   `-X main.version=<tag> -X main.commit=<sha corto> -X main.buildDate=<fecha UTC>`, y sube cada
   binario como artifact.
3. `publish` — corre una sola vez tras la matriz completa y crea la GitHub Release con los cuatro
   binarios. Es el **único escritor** de la Release, para evitar carreras entre jobs.

### 6.3 Después de publicar

1. `kandel stop` si actualizas sobre una instalación viva (el daemon mantiene el binario en uso).
2. Descarga el binario de tu plataforma y ponlo en el `PATH`.
3. Verifica: `kandel version` debe mostrar el tag, el commit, la fecha y la versión de esquema.
4. Si la release cambió assets: `kandel install claude-code` y reinicia el agente.
5. Sanidad: `kandel doctor`. El chequeo `installed_assets` debe quedar en verde y el de esquema sin
   pendientes inesperados.

---

## 7. Referencias en el código

| Qué | Dónde |
|---|---|
| Runner y lista de migraciones | `internal/adapter/sqlite/migrate.go` |
| Instalador y marca de versión | `internal/setup/` |
| Chequeo de assets desalineados | `internal/diagnostic/checks.go` |
| Sync con el hub | `internal/hub/`, `internal/adapter/httpsync/` |
| Jobs de release | `.github/workflows/ci.yml` |
| Inyección de `version`/`commit`/`buildDate` | `cmd/kandel/main.go` |
