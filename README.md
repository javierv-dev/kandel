# kandel

**Memoria persistente para agentes de código.** Un binario, una base local, cero servicios de por medio.

Los agentes de código empiezan cada sesión en blanco. Vuelves a explicar por qué esa librería y no
otra, por qué esa columna es `NOT NULL`, qué se intentó y falló hace tres semanas. kandel guarda esas
decisiones a medida que ocurren y se las devuelve al agente cuando vuelven a ser relevantes —sin que
tengas que pedirlo.

```
Sesión de hace un mes:  "usamos LWW para resolver conflictos de sync porque..."
                         └── el agente lo guarda solo
Sesión de hoy:          "¿cómo resolvemos este conflicto?"
                         └── kandel le inyecta la decisión antes de que responda
```

---

## Cómo funciona

kandel se instala **dentro** de tu agente: le añade un protocolo (una skill), un servidor MCP con las
herramientas de memoria, y hooks en los momentos clave de la sesión.

1. **Guardar.** El protocolo obliga al agente a llamar a `mem_save` en cuanto se toma una decisión, se
   arregla un bug, se fija una convención o se descubre algo no obvio. No espera a que se lo pidan.
2. **Vectorizar.** Cada memoria se embebe con [Ollama](https://ollama.com) en local
   (`qwen3-embedding:0.6b` por defecto). Nada sale de tu máquina.
3. **Recordar.** En cada mensaje tuyo, un hook busca por significado en la memoria e inyecta lo
   relevante en el contexto del agente. El agente también puede buscar a demanda con `mem_search`.
4. **Sobrevivir.** Todo vive en SQLite, en tu carpeta de usuario. Sobrevive a cierres de sesión,
   compactaciones de contexto y reinicios.

Todo el almacenamiento y la búsqueda son locales. La sincronización entre máquinas es opcional y va
contra un hub que despliegas tú.

---

## Instalación rápida

```bash
# 1. Modelo de embeddings
ollama pull qwen3-embedding:0.6b

# 2. Binario: descárgalo de las releases y ponlo en el PATH
#    (o compílalo: CGO_ENABLED=0 go build -o kandel ./cmd/kandel)

# 3. Cablearlo en tu agente
kandel install claude-code

# 4. Reiniciar el agente y comprobar
kandel doctor
```

Agentes soportados: **claude-code** y **antigravity**. Guía completa en
[docs/instalacion.md](docs/instalacion.md).

¿Repo que ya lleva meses en marcha? `kandel seed` recorre el historial y el código, propone las
decisiones de alto valor que encuentra y las guarda tras tu revisión.

---

## Qué se guarda

Cada memoria tiene un **tipo**, que decide cómo se busca y cuánto pesa:

| Tipo | Qué recoge |
|---|---|
| `decision` | Una elección que restringe el trabajo futuro (arquitectura, herramienta, convención) |
| `preference` | Cómo quieres que se trabaje. Suele ser global a todos tus proyectos |
| `architecture` | Patrones y estructura del código: nombres, capas, idioms |
| `bugfix` | Un bug arreglado **con su causa raíz** |
| `discovery` | Hechos, configuraciones y aprendizajes sueltos |
| `journal` | Bitácora del proyecto: hito a hito, qué se hizo y por qué |

Y un **alcance**: `project` (lo normal) o `global` (aplica a todo lo que haces).

La bitácora encadena fases con `mem_link`, y `mem_thread` reconstruye el hilo completo — el "cómo
llegamos hasta aquí" de una línea de trabajo, en orden.

---

## Herramientas MCP

El agente dispone de:

| Tool | Para qué |
|---|---|
| `mem_save`, `mem_update`, `mem_get` | Escribir, corregir y leer memorias |
| `mem_search`, `mem_context` | Recuperar por significado o por recencia |
| `mem_save_prompt` | Guardar un prompt reutilizable |
| `mem_thread`, `mem_link` | Encadenar y recorrer hilos de la bitácora |
| `mem_delete`, `mem_restore` | Borrado **lógico** y reversible: nada se pierde de verdad |
| `mem_compare`, `mem_judge`, `mem_pending` | Detectar contradicciones, arbitrarlas y listar pendientes |
| `mem_doctor`, `mem_sync`, `mem_current_project` | Diagnóstico, sincronización y proyecto activo |

---

## Comandos

```bash
kandel status     # daemon, puerto, rutas de base de datos y logs
kandel doctor     # diagnóstico: Ollama, esquema, assets desalineados
kandel tui        # explorar la memoria desde la terminal
kandel seed       # poblar la memoria de un repo existente
kandel sync       # sincronizar con tu hub (push + pull)
kandel config     # leer y escribir ~/.kandel/config
kandel stop       # parar el daemon local (necesario antes de actualizar el binario)
```

`kandel help` lista todos; `kandel <comando> --help`, las opciones de cada uno.

---

## Sincronizar varias máquinas

Opcional. El hub es un proceso aparte que solo guarda el change-feed y arbitra conflictos por
*last-write-wins*: no ejecuta lógica de dominio ni ve tus memorias en claro más allá de guardarlas.
Cabe en una VM del *Always Free* de Google Cloud.

El acceso se controla con un `sync_token` de alta entropía (`kandel config gen-token`) que es a la vez
tu namespace y tu secreto. **Solo por HTTPS.** Guía paso a paso: [docs/deploy-hub.md](docs/deploy-hub.md).

---

## Desarrollo

Go 1.26+, sin CGO, tres dependencias directas (SQLite puro Go, MCP y Bubble Tea).

```bash
CGO_ENABLED=0 go build ./...
go vet ./...
gofmt -l internal cmd     # salida vacía = formato correcto
go test ./...
```

Arquitectura hexagonal, con la regla de dependencia apuntando hacia adentro:

```
cmd/kandel          wiring: el único sitio que elige implementaciones concretas
internal/domain     entidades puras
internal/ports      contratos (interfaces)
internal/service    casos de uso
internal/adapter    SQLite, Ollama, LLM, sync HTTP, git, reloj
internal/port_in    entradas: cli, mcp, http, hooks, tui
```

La memoria del usuario **no se puede tirar**: todo cambio de esquema pasa por una migración additiva.
Las reglas de versionado, migraciones y releases están en [docs/versionado.md](docs/versionado.md).

---

## Documentación

- [Instalación y actualización](docs/instalacion.md)
- [Versionado y releases](docs/versionado.md)
- [Desplegar el hub de sync](docs/deploy-hub.md)
