# Propuestas de Auditoría para Kandel

Dado que se acaban de completar la **Fase 0 y Fase 1** del plan de mejoras de kandel (donde se implementaron telemetría, el concepto de `supersedes`, la reducción del bloque de arranque y los filtros de sesión), el sistema tiene ahora una base medible.

Se proponen **5 tipos de auditorías** que se pueden aplicar a la herramienta en su estado actual, ordenadas por el valor inmediato que aportan al proyecto:

### 1. Auditoría de Utilidad y Relevancia (Telemetría y Ruido)
Dado que la Fase 1 introdujo métricas de uso (cuántas veces se inyecta una memoria vs. cuántas veces es mencionada por el agente), esta auditoría evaluaría la calidad del contexto.
*   **Qué revisar:** Ejecutar y analizar `kandel usage` y `kandel doctor`.
*   **Objetivo:** Identificar si el umbral actual (`0.45`) y el límite (`3`) están funcionando, o si se están inyectando demasiadas "memorias durmientes" que consumen tokens sin aportar valor. También evaluar si el filtro que ignora la sesión en curso está funcionando correctamente tras las compactaciones de contexto.

### 2. Auditoría Arquitectónica (Regla de Dependencia Hexagonal)
El `README.md` es muy estricto sobre la arquitectura: `cmd/kandel` (wiring) -> `port_in` / `adapter` -> `service` -> `domain` (entidades puras).
*   **Qué revisar:** Analizar los imports en el código fuente de Go.
*   **Objetivo:** Garantizar que no haya "fugas" de responsabilidades. Por ejemplo, que el dominio no conozca a SQLite ni a Ollama, o que la lógica de negocio no esté acoplada a las herramientas MCP o la capa HTTP.

### 3. Auditoría de Seguridad y Sincronización (Riesgo Transversal)
En el plan se menciona que descartaron guardar comandos ejecutables en las memorias por seguridad, ya que el *hub* sincroniza los datos (por LWW - Last Write Wins) y podría propagar código malicioso entre máquinas.
*   **Qué revisar:** El mecanismo de sincronización HTTP (`internal/service/sync.go` y el hub), el manejo del `sync_token` y cómo el sistema parsea los campos al enviar y recibir.
*   **Objetivo:** Verificar que un *payload* malicioso en el hub no pueda provocar inyección de comandos o SQL en la máquina local. También verificar el riesgo documentado de que máquinas con versiones viejas borren campos nuevos al sincronizar.

### 4. Auditoría de Durabilidad e Integridad de Datos
Las memorias no se pueden "tirar" (hay borrado lógico, y reemplazos con `supersedes`).
*   **Qué revisar:** Las migraciones de SQLite, la correcta aplicación del borrado reversible (`mem_delete`/`mem_restore`) y las transacciones al usar el nuevo parámetro `supersedes`.
*   **Objetivo:** Asegurar que cuando se actualiza o reemplaza una memoria, no se rompan las cadenas de la bitácora (`mem_link`, `mem_thread`) y que las relaciones de "reemplazo" sean consistentes en la base de datos.

### 5. Auditoría de Rendimiento y Latencia (El Camino Crítico)
Kandel se ejecuta en *cada* mensaje del usuario (a través del hook). Cualquier retraso aquí empeora la experiencia de uso del agente.
*   **Qué revisar:** El tiempo que tarda `EmbedQuery` (con qwen3) y la posterior búsqueda vectorial en SQLite.
*   **Objetivo:** Asegurar que la inyección de contexto se mantenga en el orden de los milisegundos y que el guardado proactivo no bloquee la interacción principal.
