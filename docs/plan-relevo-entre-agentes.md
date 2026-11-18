# Plan: relevo entre agentes con trazabilidad

## Objetivo

Permitir que el usuario interrumpa el trabajo en Claude Code y lo continúe o revise en Antigravity —y viceversa— conservando el estado de la tarea, su procedencia y la evidencia disponible. El relevo debe funcionar aunque el agente anterior haya agotado su cuota o contexto y ya no pueda generar un resumen final.

Este documento distingue el alcance previsto de la implementación disponible. No se consultó el README.

### Estado de implementación

- Primer entregable local: esquema SQLite v9, identidad de aplicación y conversación desde los hooks, procedencia declarada en lecturas MCP y checkpoints inmutables por copia de trabajo. `kandel handoff list|show|resume` y `mem_handoff_get|save|resume` no requieren Ollama.
- Instalación: con el binario nuevo en `PATH`, actualizar los assets con `kandel install claude-code` y `kandel install antigravity`; reiniciar el daemon si estaba levantado y abrir conversaciones nuevas. Las sesiones históricas no se reasignan. Hacer backup de la base local antes de actualizar.
- Prueba manual: guardar un checkpoint en una conversación, interrumpirla sin cierre, abrir el mismo worktree con el otro agente y pedirle que liste, seleccione y verifique el relevo. Si no hay checkpoint, inspeccionar Git y repetir las pruebas pertinentes; no hay reconstrucción automática. No guardar secretos en checkpoints.
- Pendiente: validación en clientes reales (E0), vinculación de conversación por canal confiable cuando el cliente la permita, procedencia estructurada completa, avisos automáticos de relevos y cobertura, historial general y evidencias, y sincronización remota. La asociación MCP a la conversación sigue siendo declarada; no prueba quién ejecutó un cambio. Los checkpoints no se sincronizan.

## 1. Punto de partida y límites

Capacidades existentes que se reutilizan:

- Memoria compartida por proyecto, con SQLite local y sincronización opcional.
- Sesiones y asociación opcional de memorias a `SessionID`.
- Bitácoras `journal`, estado abierto/resuelto y enlaces `continues`.
- Hooks de Claude Code y Antigravity, herramientas MCP y contexto inicial mínimo.
- Guardado de prompts del usuario y algunas lecturas de transcripciones.

Carencias relevantes:

- `domain.Session` no identifica al agente o modelo.
- `domain.Memory` no distingue al registrador, al ejecutor del trabajo y al decisor.
- Guardar prompts no equivale a conservar respuestas, herramientas y resultados.
- Una bitácora depende de lo que el agente registre; no demuestra por sí sola que el trabajo se completó.
- Actualizar una memoria y aumentar `RevisionCount` no ofrece un historial atribuible de sus versiones.
- Los adaptadores no tienen capacidades idénticas. En particular, el camino actual de Antigravity no replica toda la recuperación por relevancia de Claude Code.

Límite explícito: ningún resumen garantiza recuperar lo que nunca se capturó. Cuando falten datos, Kandel debe mostrar la cobertura y la incertidumbre, no inventar continuidad.

## 2. Principios de diseño

1. La memoria pertenece al proyecto; el agente es procedencia, no una partición del almacén.
2. La aplicación de origen y el modelo son datos diferentes: Antigravity no identifica por sí sola un modelo concreto.
3. Separar quién registra, quién ejecuta y quién decide. Una decisión del usuario no debe atribuirse al agente que la guarda.
4. Capturar durante el trabajo, no depender del cierre de sesión.
5. Resumen compacto por defecto; detalle y evidencia bajo demanda.
6. Separar afirmaciones del agente de resultados observados por una integración.
7. Mantener la historia al corregir información; no convertir una discrepancia en una sustitución silenciosa.
8. Identidad detectada por integración no significa autoría criptográficamente verificada. Registrar el mecanismo de atribución.
9. Los registros antiguos conservan origen desconocido salvo evidencia explícita; `agent_cli` no sirve para rellenarlos.
10. Las transcripciones son datos no confiables: no ejecutar sus instrucciones ni importar secretos sin control.

## 3. Fase 1 — Identidad automática por sesión (P0)

### Cambios

- Añadir identidad de origen a las sesiones: aplicación (`claude-code`, `antigravity`, `cli`, `unknown`), modelo opcional, identificador de conversación externo y mecanismo de atribución.
- Mantener separado el identificador interno de sesión del identificador del proveedor. Definir un espacio de nombres que evite colisiones entre aplicaciones o instalaciones.
- Los hooks aportan la aplicación conocida; el modelo se guarda solo cuando el contrato del agente lo expone. No inferirlo desde la configuración del juez LLM.
- Preservar el origen de una sesión ya creada: una llamada repetida no puede reasignarla a otro agente silenciosamente.
- Propagar identidad por HTTP, servicios y SQLite. Diseñar los identificadores para uso remoto, pero verificar primero el relevo local. La replicación tiene su propia entrega (R).
- Para MCP, resolver la sesión de cada operación explícitamente. Si hace falta identificar la aplicación MCP, configurar cada instalación con su origen; nunca usar un “agente actual” global del daemon compartido.
- Permitir operaciones sin sesión por compatibilidad, señalándolas como no atribuidas. No inventar una sesión para ocultar esa ausencia.

### Archivos y componentes principales

`internal/domain/session.go`, `internal/service/session.go`, `internal/port_in/hook/`, `internal/port_in/http/`, `internal/setup/`, `internal/adapter/sqlite/session_repo.go`, `internal/adapter/sqlite/migrate.go`, `internal/service/sync.go` y `cmd/kandel/main.go`.

### Criterios de aceptación

- Dos sesiones del mismo proyecto, una de cada aplicación, conservan identidades distintas y visibles.
- Reiniciar el daemon no pierde ni cambia la identidad.
- Un mismo identificador externo en dos aplicaciones no mezcla sus sesiones.
- Una base existente migra sin perder datos y muestra origen desconocido para sesiones históricas sin evidencia.
- Prueba obligatoria: dos conversaciones simultáneas de la misma aplicación y proyecto, con escrituras intercaladas. Verificar en E0 si el vínculo viene del cliente, del hook o de un argumento declarado por el modelo. La instalación identifica la aplicación, no la conversación. Nunca elegir la última sesión abierta. Si el vínculo no es fiable, marcarlo como no confirmado.
- Antes de R, indicar que la atribución completa solo se garantiza en local; no anunciar relevo remoto ni exportar por accidente el nuevo material privado.

## 4. Fase 2 — Procedencia visible y consultable (P0)

### Cambios

- Registrar el evento de creación de cada memoria con sesión, actor registrador y fecha.
- Representar por separado, cuando se conozcan, el ejecutor/proponente y el decisor (`user`, agente identificado o desconocido). Estas atribuciones pueden ser declaradas y deben indicarlo.
- Exponer procedencia resumida en `mem_get`, `mem_search`, `mem_context`, `mem_thread`, CLI y TUI, sin sobrecargar cada resultado.
- Añadir filtros por aplicación de origen y sesión. Diferenciar “registrado por” de “trabajo atribuido a”.
- Mantener las búsquedas sin filtro compartidas entre agentes.
- Definir tratamiento de duplicados: conservar el autor original y registrar la nueva observación atribuida; no asignar toda la memoria al último agente que la repitió.
- No incorporar etiquetas de autoría al texto semántico por defecto: son metadatos y filtros, no contenido que deba alterar los embeddings.

### Ejemplo de presentación

> Registrado por Claude Code · sesión X · modelo no informado.
> Trabajo atribuido a Claude Code; decisión atribuida al usuario. Atribución declarada.

### Criterios de aceptación

- Antigravity puede consultar lo registrado por Claude Code y viceversa.
- Una decisión del usuario no aparece como una decisión del agente registrador.
- La procedencia sobrevive a actualizaciones, deduplicación, borrado lógico y restauración. Verificar por separado su conservación entre máquinas en R.
- Un origen ausente se muestra como desconocido, no se omite de forma ambigua.

## 5. Fase 3 — Estado de relevo incremental (P0)

### Modelo propuesto

Reutilizar `journal` como narrativa y soporte de continuidad. Añadir metadatos de tarea/relevo donde hagan falta, evitando crear otra memoria paralela sin vínculo con las bitácoras existentes.

Cada tarea o hilo activo necesita un identificador estable y un estado de relevo con:

- Objetivo y referencia a la petición del usuario.
- Sesión de origen y sesiones que han continuado el trabajo.
- Trabajo completado, trabajo en curso y cambios aún sin verificar.
- Decisiones, motivos y alternativas descartadas.
- Archivos afectados y referencia Git, cuando exista.
- Pruebas ejecutadas, resultado y evidencia disponible.
- Bloqueos y siguiente paso concreto.
- Fecha de actualización y cobertura de captura.

Distinguir estado de tarea (en curso, bloqueada, terminada) de sesión abierta/cerrada: una sesión sin cierre no prueba que siga trabajando.

### Flujo

1. Abrir o seleccionar una tarea/hilo al empezar trabajo relevante.
2. Actualizar el relevo después de hitos: cambios significativos, pruebas, decisiones y bloqueos.
3. Registrar el avance desde el agente y adjuntar observaciones deterministas disponibles, sin afirmar que esas observaciones reconstruyen toda su intención.
4. Mantener el último checkpoint utilizable si la sesión se interrumpe. Mostrar su antigüedad y las lagunas conocidas.
5. En el agente nuevo, ofrecer relevos pendientes del proyecto; si hay varios, pedir o permitir seleccionar el adecuado.
6. Recuperar un paquete compacto: objetivo, estado, siguiente paso, procedencia y referencias a detalles.
7. Continuar vinculando una sesión nueva al mismo hilo, sin cambiar el autor histórico.

### Interfaz propuesta

Validar nombres con la superficie MCP existente antes de implementarlos:

- `mem_handoff_save`: añadir una versión inmutable con ID estable, tarea, sesión registradora y versión padre; el puntero a la versión actual sí se actualiza. Escritura y movimiento del puntero atómicos, con clave de idempotencia para reintentos. No esperar a E4 para preservar los checkpoints.
- `mem_handoff_get`: obtener un relevo concreto o listar candidatos del proyecto.
- `mem_handoff_resume`: vincular la nueva sesión con la versión exacta que recibió, no solo con el último estado del hilo.
- Equivalente CLI: `kandel handoff list|show|resume`.

La reanudación debe ser idempotente y no cerrar ni apropiarse silenciosamente de una tarea que otro agente sigue usando. Las actualizaciones concurrentes deben detectarse mediante versión esperada o el mecanismo de conflictos correspondiente.

### Recuperación y presupuesto

- Inyectar al inicio solo un índice pequeño de relevos disponibles.
- Cargar el paquete completo al seleccionar o solicitar continuar una tarea.
- Aplicar límites configurables de contenido y advertir si hubo truncamiento.
- Validar proyecto y copia de trabajo antes de mostrar contenido sensible o reanudar. `internal/project/detect.go` reduce el remoto a su último componente: probar `equipo-a/api` frente a `equipo-b/api` y dos worktrees del mismo repo. Usar una identidad persistente o selección explícita cuando el nombre sea ambiguo; distinguir repo de worktree. No inferir quién modificó un archivo por su diff.
- Validar rama, commit y cambios locales al retomar: pueden haber variado desde el checkpoint.
- No depender de Ollama ni de un LLM para leer un relevo conocido por identificador.

### Criterios de aceptación

- Tras matar el proceso sin ejecutar el hook de cierre, el otro agente recupera el último checkpoint guardado.
- El paquete identifica objetivo, avance, bloqueo y siguiente paso sin leer toda la conversación.
- Varias tareas simultáneas no se mezclan ni se selecciona una arbitrariamente.
- Ambos agentes pueden guardar y recuperar el mismo esquema de relevo aunque sus hooks difieran.
- Si no hay checkpoint o está desactualizado, se informa explícitamente y se ofrecen los registros disponibles.

## 6. Fase 4 — Historial y revisión atribuible (P1)

### Cambios

- Generalizar el versionado inmutable de checkpoints ya disponible en E3 al resto de memorias: identificador estable, versión padre, contenido, actor, sesión, fecha y motivo.
- Registrar una versión inicial y cada cambio sustantivo. La memoria actual sigue siendo la proyección que usan las búsquedas existentes.
- Escribir revisión, proyección actual y mutación de sincronización en una sola transacción.
- Registrar borrado lógico/restauración como eventos atribuibles, manteniendo las versiones previas.
- Añadir una revisión crítica vinculada a la versión evaluada: autor, conclusión, argumentos y referencias a evidencia.
- Separar revisión de modificación: “Antigravity discrepa” no cambia automáticamente la decisión vigente ni significa que su conclusión sea correcta.
- Distinguir decisiones explícitas del usuario de hipótesis o propuestas del agente. Revisar una implementación no autoriza ignorar una restricción del usuario.
- Ante edición concurrente, conservar ambas propuestas y su procedencia, sin sobrescribirlas por orden de llegada.
- Añadir consulta de historial y revisiones, por ejemplo `mem_history` y `mem_review`.

### Criterios de aceptación

- Se reconstruye qué registró Claude, qué cuestionó Antigravity y qué corrección se aplicó.
- Una revisión se mantiene vinculada a la versión evaluada aunque la memoria cambie después.
- No se pierden autoría ni contenido anterior al resolver conflictos de sincronización.
- Fallos de transacción no dejan una actualización sin su historial correspondiente.

## 7. Fase 5 — Evidencia y transcripciones bajo demanda (P2)

### Cambios

- Definir un registro de evidencias con identificador, tipo, origen, fecha, sesión, localizador y huella del contenido cuando se conserve.
- Admitir referencias a commits, diffs, ejecuciones de pruebas y fragmentos de conversación. Los registros deben distinguir captura realizada por integración de contenido declarado por un agente.
- Una prueba debe incluir comando, resultado, momento y estado del código sobre el que se ejecutó cuando pueda conocerse. “Pasó” no prueba que el código actual siga pasando.
- Añadir importadores de transcripción separados por aplicación y versión de formato, con detección de capacidades reales.
- Importar de forma incremental e idempotente; tolerar archivos parcialmente escritos, truncados o rotados.
- Si un formato no está soportado, advertir y conservar la opción de importación manual; no aparentar captura completa.
- Conservar mensajes y resultados visibles relevantes, no exigir razonamiento interno privado del modelo.
- Enlazar fragmentos desde el relevo y las revisiones; no volcar la conversación completa en cada arranque.

### Privacidad y conservación

- Importación de transcripciones mediante consentimiento explícito, local por defecto.
- Exclusiones y límites de tamaño, acceso restringido al directorio de datos y sanitización de salida.
- Redactar secretos antes de persistir o sincronizar, advirtiendo que la detección automática no ofrece garantía absoluta.
- Configurar retención y eliminación de evidencias. La privacidad puede exigir borrado físico del material sensible, separado del borrado lógico de memorias.
- No sincronizar archivos locales ni transcripciones completas automáticamente. Definir qué referencias o extractos se exportan y qué ocurre si la evidencia solo existe en la máquina de origen.
- Marcar evidencia modificada, eliminada o no disponible; una ruta o hash por sí solos no conservan el archivo.

### Criterios de aceptación

- El agente revisor puede inspeccionar la evidencia de una afirmación sin recibir toda la transcripción.
- Importar dos veces no duplica eventos.
- Una transcripción incompleta no se presenta como completa.
- Las pruebas verifican exclusiones, redacción, consentimiento, retención y ausencia de sincronización accidental.

## 8. Migración, compatibilidad y sincronización

Aplicable a todas las fases:

- Añadir migraciones versionadas; nunca modificar migraciones ya desplegadas.
- Probar migración desde bases existentes y creación de una base nueva.
- Hacer copia de seguridad antes de las migraciones y documentar restauración; no prometer downgrade del esquema.
- Entrega remota R: hoy `SyncService.pushBatch` filtra todo salvo memorias pero confirma el lote, y al importar vacía `SessionID`. No reutilizar esa ruta sin preservar el vínculo ni confirmar entidades no entregadas. Probar en dos máquinas sesión, versión recibida, reintento y cliente antiguo; aplazar R no debe degradar el sync actual de memorias.
- Definir versiones/capacidades del protocolo de sincronización antes de introducir nuevas entidades. El código actual valida tipos de entidad, por lo que no basta con añadir campos al JSON.
- Evitar que un cliente antiguo reescriba objetos borrando procedencia o historial: negociar capacidades, rechazar escrituras incompatibles o exigir actualización según la entidad.
- Sincronizar identidades, revisiones y referencias con identificadores estables, no con IDs locales de SQLite.
- Resolver dependencias y llegada fuera de orden de sesiones, memorias, revisiones y evidencias.
- La importación de un estado antiguo crea una versión base marcada como histórica; no reconstruye autores ni versiones inexistentes.

## 9. Pruebas y validación

### Automatizadas

- Dominio y servicios: identidad, atribución, duplicados, selección de tareas, versiones y revisión crítica.
- SQLite: migraciones, atomicidad, concurrencia, idempotencia y persistencia tras reinicio.
- Hooks/MCP/HTTP: formatos de ambas aplicaciones, sesiones ausentes y daemon compartido entre proyectos/agentes.
- Sincronización: dos máquinas, reintentos, entidades fuera de orden, clientes incompatibles y conflictos concurrentes.
- Degradación: Ollama caído, ausencia de agente LLM, cuota agotada y transcripción inaccesible.
- Contexto: límites, truncamiento visible y aislamiento entre proyectos.
- Privacidad: entradas maliciosas, secretos, permisos y exportaciones.

### Escenario de aceptación central

1. Claude Code inicia una tarea, registra una decisión del usuario y modifica archivos.
2. Guarda un checkpoint con una prueba fallida y una alternativa descartada.
3. Se interrumpe sin cierre ni resumen final.
4. Antigravity abre el mismo proyecto, selecciona la tarea y recupera estado y procedencia.
5. Inspecciona los cambios reales, identifica un problema y registra una revisión de la versión concreta.
6. Aplica una corrección, adjunta su resultado de pruebas y guarda un checkpoint nuevo.
7. Claude Code retoma y distingue su trabajo original, la revisión y la corrección de Antigravity.
8. Repetir en sentido inverso. Para E3 bastan los checkpoints inmutables: la revisión crítica estructurada del paso 5 pertenece a E4; los adjuntos verificables, a E5.
9. Repetir el corte (a) antes del primer checkpoint y (b) después de editar archivos y fallar una prueba, pero antes de guardar el nuevo checkpoint. El receptor distingue «registrado», «observable ahora» y «desconocido»: no atribuye el diff ni asegura conocer una prueba cuya salida no se guardó. Puede proponer volver a ejecutarla. No exigir que responda el agente interrumpido.
10. Comprobar que tras la corrección aún se puede abrir la versión exacta que recibió el segundo agente. Repetir las variantes entre máquinas solo al validar R.

Las pruebas con fixtures validan nuestros contratos, pero la compatibilidad real de los hooks debe comprobarse también manualmente con versiones documentadas de ambas aplicaciones.

## 10. Orden de ejecución y entregables

| Entrega | Alcance | Resultado comprobable |
|---|---|---|
| E0 | Contratos y fixtures reales de ambas integraciones | Matriz de capacidades: identidad, modelo, conversación, transcripción y eventos disponibles |
| E1 | Fase 1 | Sesiones locales identificadas y migración segura |
| E2 | Fase 2 | Procedencia visible y consultable sin aislar memorias por agente |
| E3 | Fase 3 | Relevo funcional tras interrupción abrupta, con cobertura explícita |
| E4 | Fase 4 | Historial y revisiones críticas atribuibles |
| E5 | Fase 5 | Evidencia consultable e importación opcional con privacidad |
| R | Relevo remoto, tras E3 | Identidad y checkpoint exacto disponibles en la otra máquina; compatibilidad validada. No transfiere el árbol de trabajo. |

**MVP local:** E0–E3. Resuelve continuidad y procedencia básica con la información efectivamente capturada. No promete auditoría completa ni reconstrucción íntegra de conversaciones.

**Solución completa:** E0–E5 + R. Permite además comparar versiones y contrastar afirmaciones con evidencia.

Cada entrega incluye pruebas, migración si corresponde y actualización de los assets de instalación. Revisar las instrucciones compartidas: la skill actual contiene descripciones de contexto inicial que no coinciden por completo con el servicio actual y usa referencias específicas de Claude Code que no deben asumirse en Antigravity.

### Presupuesto de tokens y llamadas

Diseñar E3 para que el checkpoint sustituya la escritura de bitácora cuando corresponda, no para que duplique su narrativa. Resolver metadatos sin LLM, devolver confirmaciones cortas y cargar el relevo solo al retomarlo. Ver [simulación de una tarea con dos agentes](simulacion-coste-relevo.md) para el presupuesto y el protocolo de medición.

## 11. Fuera de alcance inicial

- Transferir el estado interno privado o la ventana de contexto exacta de un modelo.
- Garantizar que el segundo agente comparta la interpretación del primero.
- Ejecutar automáticamente tareas pendientes al cambiar de agente.
- Determinar cuál agente tiene razón solo por su identidad.
- Convertir commits Git en prueba inequívoca de autoría de una IA.
- Un sistema general de coordinación o bloqueo entre múltiples agentes autónomos.

## Definición de éxito

El usuario puede preguntar: “Continúa la tarea que dejó Claude Code y revisa su enfoque”, y Antigravity puede identificar la tarea correcta, recuperar el último estado capturado, distinguir quién afirmó o hizo cada cosa, comprobar el repositorio y acceder al detalle disponible. Todo ello sin necesitar que Claude Code vuelva a responder y sin presentar información faltante como si estuviera registrada.
