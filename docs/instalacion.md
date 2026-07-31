# Instalar kandel

kandel es un binario único (Go, sin CGO) que da **memoria persistente** a tu agente de código:
guarda decisiones, bugs y convenciones en una base local y las devuelve en sesiones futuras.

Esta guía cubre la instalación normal. Si vas a publicar versiones, ver
[versionado.md](versionado.md). Para sincronizar entre máquinas, ver [deploy-hub.md](deploy-hub.md).

---

## 1. Requisitos

| Requisito | Para qué | Obligatorio |
|---|---|---|
| [Ollama](https://ollama.com) con el modelo de embeddings | Vectorizar memorias (buscar y guardar) | Sí |
| Un agente soportado (`claude-code`, `antigravity`) | La integración que consume la memoria | Sí |
| Go 1.26+ | Solo si compilas desde el código fuente | No |

Descarga el modelo de embeddings por defecto:

```bash
ollama pull qwen3-embedding:0.6b
```

> El modelo es **contrato de datos**: cambiarlo obliga a re-vectorizar toda la memoria.
> Se puede cambiar con `kandel config set embed_model <modelo>`, pero no lo hagas sin motivo.

---

## 2. Obtener el binario

### Opción A — descargar la release (recomendado)

Baja el binario de tu plataforma desde la
[página de releases](https://github.com/javierv-dev/kandel/releases) y ponlo en una carpeta del `PATH`:

| Plataforma | Archivo |
|---|---|
| Linux x86-64 | `kandel-linux-amd64` |
| macOS Intel | `kandel-darwin-amd64` |
| macOS Apple Silicon | `kandel-darwin-arm64` |
| Windows x86-64 | `kandel-windows-amd64.exe` (renómbralo a `kandel.exe`) |

En Linux/macOS hay que darle permiso de ejecución: `chmod +x kandel`.

### Opción B — compilar desde el código

```bash
git clone https://github.com/javierv-dev/kandel
cd kandel
CGO_ENABLED=0 go build -o kandel ./cmd/kandel
```

En Windows/PowerShell:

```powershell
$env:CGO_ENABLED = "0"
go build -o kandel.exe .\cmd\kandel
```

Antes de instalar un binario compilado por ti, comprueba que el repo está sano:

```bash
go build ./...     # compila
go vet ./...       # sin avisos
gofmt -l internal cmd   # salida vacía = formato correcto
go test ./...      # todo verde
```

---

## 3. Instalar la integración en el agente

```bash
kandel install claude-code
```

Agentes soportados: `claude-code`, `antigravity`. La operación es **idempotente**: reinstalar no
duplica nada. Puedes instalar varios agentes; el primero queda como agente activo (el que juzga y
siembra memorias) y se cambia con `kandel agent use <nombre>`.

Qué escribe en disco (ejemplo con `claude-code`, `~` = tu carpeta de usuario):

| Artefacto | Ruta |
|---|---|
| Skill principal | `~/.claude/skills/kandel/SKILL.md` |
| Skill de seed (`/kandel-seed`) | `~/.claude/skills/kandel-seed/SKILL.md` |
| Scripts de hook | `~/.claude/kandel/scripts/` |
| Servidor MCP | `~/.claude.json` → `mcpServers.kandel` |
| Hooks de ciclo de vida | `~/.claude/settings.json` |
| Marca de versión de los assets | `~/.claude/kandel/VERSION` |
| Config de kandel | `~/.kandel/config` |

> **Reinicia el agente después de instalar.** Mientras una sesión siga abierta, usa la versión
> vieja del binario y de los hooks que ya tenía cargados en memoria.

---

## 4. Verificar

```bash
kandel version   # versión, commit, fecha de build y versión de esquema de la base
kandel status    # daemon, puerto, rutas de base de datos y logs
kandel doctor    # diagnóstico completo (incluye si los assets instalados están al día)
```

Desde el agente, la tool MCP `mem_doctor` hace el mismo diagnóstico.

Prueba opcional del seed (pobla la memoria de un repo ya existente). En seco, sin guardar nada:

```bash
cd /ruta/a/tu/repo
kandel seed --dry-run
```

Debe listar candidatos con el formato `(tipo/alcance) Título` y terminar en
`(dry-run) No se guardó nada.` Requiere la CLI del agente en el `PATH` y tiene coste de API.

---

## 5. Actualizar

1. **Para el daemon primero:** `kandel stop`. Los hooks levantan un daemon HTTP (`kandel serve`) que
   mantiene el binario **en uso**; en Windows, sobrescribirlo sin pararlo falla con "archivo en uso".
2. Reemplaza el binario por el nuevo.
3. Si la release cambió assets (skill, hooks, registro MCP), **reinstala**: `kandel install claude-code`.
4. Reinicia el agente y comprueba con `kandel doctor`.

El siguiente hook vuelve a levantar el daemon con la versión nueva; no hace falta arrancarlo a mano.

### Si el binario sigue bloqueado en Windows

Cuando `kandel stop` no está disponible (binario viejo), mata el proceso a mano —verifica antes con
`Get-CimInstance Win32_Process` que el `CommandLine` termine en `serve`:

```powershell
taskkill /IM kandel.exe /F
```

Alternativa sin matar nada: Windows permite **renombrar** un `.exe` en uso aunque no permita
sobrescribirlo. Compila a un temporal, aparta el viejo y pon el nuevo en su sitio:

```powershell
$bin = "C:\ruta\en\tu\PATH\kandel.exe"
$env:CGO_ENABLED = "0"
go build -o "$env:TEMP\kandel-new.exe" .\cmd\kandel
Rename-Item $bin "$bin.$(Get-Date -Format yyyyMMdd-HHmmss).old" -Force
Move-Item "$env:TEMP\kandel-new.exe" $bin -Force
```

Los archivos `.old` quedan bloqueados por los procesos vivos; se pueden borrar tras reiniciar el
agente (`Remove-Item "$bin.*.old" -Force`).

---

## 6. Desinstalar

```bash
kandel uninstall claude-code
```

Retira la integración sin tocar el resto de la configuración de tu agente. **No** borra
`~/.kandel/config` ni la base de datos: esos son tus datos, y se eliminan a mano si quieres.

---

## 7. Comandos disponibles

| Comando | Para qué |
|---|---|
| `install` / `uninstall` | Cablear o retirar la integración en un agente |
| `agent` | Ver o cambiar el agente activo |
| `config` | Leer y escribir la configuración (`~/.kandel/config`) |
| `status` / `doctor` / `version` | Estado, diagnóstico y metadatos de build |
| `serve` / `stop` | Daemon HTTP local (lo gestionan los hooks) |
| `sync` | Sincronizar con el hub remoto |
| `seed` | Poblar la memoria de un repo existente |
| `embeddings` | Mantenimiento de vectores (backfill / re-embedding) |
| `session` / `hook` / `mcp` | Puntos de entrada que usa el agente |
| `tui` | Interfaz de terminal para explorar la memoria |

Usa `kandel help` para la lista viva y `kandel <comando> --help` para las opciones de cada uno.
