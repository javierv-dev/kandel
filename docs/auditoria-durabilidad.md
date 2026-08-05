# Auditoría de durabilidad de datos

Estado: **en curso** — Fases 0, 1 y 5 cerradas; correcciones 1 y 2 aplicadas y verificadas.
Pendientes: Fases 2, 3 y 4, y las correcciones 3 a 6.
Iniciada: 2026-07-31.

Objetivo: determinar, con evidencia medida y no con supuestos, bajo qué condiciones kandel puede
perder o corromper datos del usuario, y dejar tests de regresión que impidan que esas condiciones
vuelvan sin ser detectadas.

Regla de la auditoría: **ninguna fase toca la base real en modo escritura**. Las sondas sobre
`~/.kandel/kandel.db` se abren en solo-lectura; lo que necesite escribir corre sobre bases
temporales.

## 1. Alcance

| Superficie | Dónde vive | Qué se pierde si falla |
|---|---|---|
| Base cliente | `~/.kandel/kandel.db` (`cmd/kandel/main.go:174-187`) | memorias, sesiones, prompts, vectores, relaciones |
| Base hub | `hub_changes` (`internal/adapter/sqlite/hub_store.go`) | change-feed del servidor; convergencia entre máquinas |
| Cola de sync | `sync_mutations`, `sync_state`, `sync_cursor`, `sync_conflicts` | escrituras locales aún no propagadas |
| Logs deterministas | `~/.kandel/logs/{hooks,mcp,embeddings}.log` | la capa de auditoría garantizada (ver memoria id 76) |

Fuera de alcance: rendimiento, calidad de los embeddings, política de retención.

## 2. Modelo de fallos considerado

1. Corte de energía o `kill -9` a mitad de una transacción.
2. Acceso concurrente multi-proceso (hooks, servidor MCP, CLI, TUI, `serve`) sobre el mismo archivo.
3. Migración de esquema que falla a mitad.
4. Convergencia de sync que sobrescribe una edición buena (LWW).
5. Borrado lógico que degenera en pérdida real (huérfanos, purga).
6. Disco lleno o archivo corrupto, sin copia de seguridad.

## 3. Hipótesis previas

Detectadas al escanear el código antes de medir. La auditoría las confirma o las descarta; ninguna
se da por cierta hasta la Fase 0.

### H1 — Los PRAGMA se aplican al pool, no a cada conexión

`internal/adapter/sqlite/db.go:49-54` aplica `foreign_keys`, `busy_timeout` y `journal_mode` con
`ExecContext` sobre un `*sql.DB`. `foreign_keys` y `busy_timeout` son **estado por conexión**;
`database/sql` abre conexiones adicionales bajo demanda y esas nacen con los valores por defecto.
No hay `SetMaxOpenConns` en el repositorio. `journal_mode = WAL` sí persiste (se guarda en el
archivo), por lo que ese sobrevive.

Consecuencia esperada: integridad referencial no aplicada y `SQLITE_BUSY` inmediato (sin espera) en
toda conexión que no sea la primera. `hub_store.go:51` reutiliza la misma variable `pragmas`, así que
hereda el mismo defecto.

Severidad estimada: **alta**.

### H2 — `synchronous` nunca se fija de forma explícita

No aparece en ninguna parte del código. Queda al valor por defecto del driver
`modernc.org/sqlite`. Con WAL y `synchronous = NORMAL`, un corte de energía puede perder
transacciones ya confirmadas (el commit no obliga un `fsync`).

Severidad estimada: **media** (depende del valor real que mida la Fase 0).

### H3 — Sin copia de seguridad ni verificación de integridad

No existe `backup`, `PRAGMA integrity_check`, `VACUUM` ni `wal_checkpoint` en el código. No hay
copia, no hay detección de corrupción, y el archivo `-wal` crece sin techo mientras el proceso MCP
siga vivo.

Severidad estimada: **alta**.

### H4 — `doctor` no observa la base de datos

Los chequeos existentes (`internal/diagnostic/checks.go`) son `embedder`, `installed_assets`,
`latest_release` y `pending_embeddings`. Ninguno mira integridad, tamaño del WAL ni retraso de la
cola de sync. Un daño en la base es invisible hasta que alguien lo nota por casualidad.

Severidad estimada: **media**.

### H5 — Concurrencia multi-proceso real, sin serialización

Los hooks son procesos cortos, el servidor MCP es un proceso largo, y CLI, TUI y `serve` son
procesos aparte. Todos abren la misma `kandel.db`. Combinado con H1, produce escrituras que fallan
en silencio.

Severidad estimada: **alta**.

### H6 — LWW decidido por reloj de pared

El hub resuelve el último-que-escribe por `updated_at` en nanosegundos Unix. Un desfase de reloj
entre máquinas hace que la edición más nueva pierda. Además `sync_repo.go:34` usa el
`datetime('now')` de SQLite mientras el resto del código usa el `Clock` inyectado: hay dos fuentes
de tiempo conviviendo.

Severidad estimada: **media**.

## 4. Fases

### Fase 0 — Instrumentar

Medir el estado real antes de proponer nada. Dos artefactos:

- Un test determinista que fuerza varias conexiones del pool y comprueba el valor de los PRAGMA en
  cada una. Es la evidencia que confirma o tumba H1 y H2 sin depender del entorno.
- Una sonda de solo-lectura sobre la instalación viva que reporte: `journal_mode`, `synchronous`,
  `foreign_keys`, `busy_timeout`, tamaños de `.db`/`-wal`/`-shm`, `integrity_check`,
  `foreign_key_check`, `user_version` y el retraso de la cola de sync.

Criterio de salida: línea base documentada en la sección 5 de este documento.

### Fase 1 — Confirmar pérdida bajo concurrencia

Varios procesos escribiendo memorias en paralelo contra la misma base, más un lector. Comparar
escrituras reportadas como exitosas contra filas realmente presentes.

Criterio de salida: cero escrituras perdidas y cero `SQLITE_BUSY` no manejado. Si falla, H1 y H5
quedan confirmadas.

### Fase 2 — Simular corte de energía

Matar el proceso a mitad de un lote de confirmaciones, reabrir y contar. Repetir con `synchronous`
en `FULL` y en `NORMAL` para medir la diferencia real, no la teórica.

Criterio de salida: ninguna transacción reportada como confirmada se pierde, y la base nunca queda
corrupta.

### Fase 3 — Auditar el camino de sync

- Qué ocurre con `sync_mutations` si el envío falla a medias: ¿se reencolan, o se marcan consumidas
  antes de confirmar?
- Si `sync_cursor` avanza antes de aplicar el lote, un fallo pierde cambios en silencio.
- Prueba de desfase de reloj: dos clientes separados varios minutos editando la misma memoria.
- `sync_conflicts`: verificar que la fila remota descartada queda siempre recuperable.

### Fase 4 — Auditar borrado y migraciones

- `mem_delete` es lógico (`deleted_at`): comprobar que ningún camino haga un `DELETE` físico sin
  respaldo, y qué ocurre con vectores y relaciones huérfanas.
- Migraciones: `applyMigration` mete DDL y `user_version` en una sola transacción; verificar que
  SQLite revierte de verdad ese DDL, y ejercitar la cadena v0 a v6 sobre una base con datos.

### Fase 5 — Auditar la capa determinista

Si un hook falla al escribir (base bloqueada, disco lleno), ¿el usuario se entera, o el prompt se
pierde sin dejar rastro? Revisar `internal/port_in/hook` y `internal/adapter/auditlog`.

## 5. Resultados

_(Se completa al cerrar cada fase.)_

### Fase 0 — cerrada el 2026-07-31

Instrumentos construidos:

- `internal/adapter/sqlite/durability_probe_test.go` — mide el estado de los PRAGMA en cada
  conexión del pool. Informa siempre; solo falla con `KANDEL_DURABILITY_STRICT=1`, para no dejar la
  suite en rojo antes de decidir la corrección.
- `scripts/durability-probe/` — sonda de solo-lectura sobre la instalación viva
  (`go run ./scripts/durability-probe`).

#### H1 — CONFIRMADA (severidad alta)

Cuatro conexiones simultáneas del pool, sobre una base recién abierta con `Open`:

| conn | journal_mode | synchronous | foreign_keys | busy_timeout |
|---|---|---|---|---|
| 0 | wal | 2 | 1 | 5000 |
| 1 | wal | 2 | **0** | **0** |
| 2 | wal | 2 | **0** | **0** |
| 3 | wal | 2 | **0** | **0** |

`journal_mode = WAL` sobrevive en todas, como se esperaba (se persiste en el archivo). Los otros dos
no: solo la conexión que atendió el `ExecContext` de `Open` queda configurada.

No es un detalle teórico. Con varias conexiones vivas, un `INSERT` de una memoria con un
`session_id` inexistente **entra sin error**: la clave foránea declarada en el esquema no se está
aplicando. Y `busy_timeout = 0` significa que esas conexiones devuelven `SQLITE_BUSY` de inmediato,
sin esperar, en cuanto otro proceso tiene la base tomada.

#### H2 — REFUTADA

`synchronous = 2` (FULL) es el valor por defecto que aplica `modernc.org/sqlite` v1.45. Un commit
obliga `fsync`; no hay ventana de pérdida por corte de energía en transacciones ya confirmadas. La
Fase 2 sigue siendo útil para verificar el comportamiento real, pero deja de ser urgente.

Queda pendiente fijarlo de forma explícita: hoy kandel depende de un valor por defecto del driver
que nadie eligió y que una actualización podría cambiar.

#### Estado de la instalación viva

`~/.kandel/kandel.db`, esquema v6:

```
kandel.db      5 443 584 bytes      page_size 4096, page_count 1341
kandel.db-wal    799 312 bytes      wal_autocheckpoint 1000 (≈4 MB)
integrity_check      ok
foreign_key_check    ok (sin filas huérfanas)

memorias vivas         239      sesiones     85
memorias borradas       13      prompts     370
memorias sin vector      0      relaciones    7

mutaciones pendientes  197
mutaciones confirmadas 990
conflictos de sync       0
cursores de sync       cadena1234=0, remote=282
```

Lecturas de esto:

- **Sin daño acumulado.** Pese a H1, la base no tiene corrupción ni filas huérfanas. El defecto es
  una protección ausente, no un destrozo en curso: hasta ahora nada ejercitó el camino que rompe.
- **197 mutaciones pendientes.** Datos que existen en una sola máquina. Es el número que la Fase 3
  tiene que explicar: si es atraso normal de sync o mutaciones que nunca van a drenar.
- **`sync_mutations` no se purga nunca.** `Ack` marca `acked_at`, no borra. 990 filas confirmadas
  siguen ahí. No es pérdida de datos, pero es crecimiento sin techo del archivo.
- **`cadena1234=0`** parece un destino de prueba abandonado en la tabla `sync_cursor`. Verificar en
  la Fase 3 si es basura o algo vivo.
- **WAL en 799 KB** contra un umbral de checkpoint de ~4 MB: por debajo del disparo automático. No
  es problema hoy, pero confirma que nadie hace checkpoint explícito (H3).

#### Ajuste al plan

- H2 baja de prioridad; la Fase 2 pasa de "urgente" a "verificación".
- H1 sube a lo más alto: es un defecto confirmado, con reproducción determinista y arreglo conocido.
- La Fase 1 (concurrencia) ya no tiene que demostrar que el problema existe, sino medir su impacto
  real: con `busy_timeout = 0`, cuántas escrituras se pierden entre procesos.

### Fase 1 — cerrada el 2026-07-31

Instrumento: `internal/adapter/sqlite/durability_concurrency_test.go`. Lanza seis procesos de
verdad (el propio binario de test, reejecutado) contra la misma base, sincronizados por una barrera
para que empujen a la vez. Procesos y no goroutines a propósito: dentro de un mismo proceso el
bloqueo lo arbitra Go, no SQLite, y el experimento no sería fiel.

Se comparan dos modos de apertura: `legacy` (lo que hace hoy `sqlite.Open`) y `dsn` (los mismos
PRAGMA pasados por el DSN). Y tres escenarios, porque los dos primeros resultaron dar un falso
negativo que conviene dejar documentado.

#### Por qué hicieron falta tres escenarios

Los dos primeros escenarios midieron **cero** contención en modo legacy, lo que parecía exculpar a
H1. La explicación no era que el defecto no existiera, sino que el experimento no lo tocaba: un
proceso que escribe **en serie** reutiliza siempre la misma conexión del pool, y esa es justamente
la primera, la única que quedó bien configurada. `database/sql` solo abre conexiones adicionales
cuando hay concurrencia **dentro** del proceso.

Es decir: H1 no afecta a los hooks (procesos cortos y secuenciales), pero sí de lleno al **servidor
MCP**, que atiende peticiones en paralelo. El tercer escenario (`intraproceso`, cuatro escrituras
concurrentes por proceso) es el que reproduce esa situación.

#### Mediciones (3 corridas completas)

| escenario | modo | procesos que no abrieron | rechazos por bloqueo | tasa de éxito |
|---|---|---|---|---|
| instantanea | legacy | 2, 0, 1 de 6 | 0 | 66.7% / 100% / 83.3% |
| instantanea | dsn | 0 de 6 | 0 | 100% |
| retenida | legacy | 1, 1, **5** de 6 | 0 | 83.3% / 83.3% / **16.7%** |
| retenida | dsn | 0 de 6 | 0 | 100% |
| intraproceso | legacy | 1, 1, 0 de 6 | 45, 45, 54 de 72 | **20.8% / 20.8% / 25.0%** |
| intraproceso | dsn | 0 de 6 | 0 | 100% |

En modo DSN: 9 subcorridas, 0 fallos de apertura, 0 rechazos, 100% de éxito en todas.

#### Hallazgo 1 — la apertura de la base falla bajo concurrencia (severidad alta)

El fallo no aparece al escribir, sino al **abrir**:

```
sqlite: conectar "…/kandel.db": database is locked (261)
```

261 es `SQLITE_BUSY_RECOVERY`. Con seis procesos abriendo a la vez, en modo legacy fallan entre 0 y
5 de 6, de forma no determinista. En modo DSN no falla ninguno, nunca.

La causa está en el orden de `Open`: el `Ping` y las migraciones ocurren **antes** de que el
`busy_timeout` llegue a aplicarse, así que durante la ventana de arranque la conexión se rinde sin
esperar. Pasar el PRAGMA por el DSN lo aplica desde la primera conexión, ventana de arranque
incluida.

Esto es más grave que un error de escritura: un proceso que no abre la base pierde **todo** su
trabajo, no una fila. En un hook, que es fuego y olvido, significa un prompt o una sesión que nunca
se registró y de la que nadie se entera. Es exactamente el hueco de la capa determinista que la
memoria id 76 daba por garantizada.

#### Hallazgo 2 — el servidor MCP pierde 3 de cada 4 escrituras bajo carga (severidad alta)

En el escenario `intraproceso`, modo legacy: **45-54 de 72 escrituras rechazadas**, tasa de éxito
entre 20.8% y 25.0%, consistente en las tres corridas. En modo DSN, 72 de 72 en las tres.

Es la consecuencia directa de H1 medida en su peor caso: las conexiones 2..N tienen
`busy_timeout = 0` y abandonan ante el primer bloqueo en vez de esperar su turno.

#### Hallazgo 3 — no hay pérdida silenciosa

En las 18 subcorridas, el número de filas en la base coincidió **siempre** con el número de
escrituras reportadas como exitosas. SQLite mantiene su atomicidad: lo que se confirma, se guarda;
lo que falla, falla de forma visible y devuelve error.

Esto acota mucho el problema. Kandel no corrompe ni pierde datos por debajo: **rechaza** trabajo. El
daño depende por entero de si quien llama trata ese error o lo traga, que es lo que mira la Fase 5.

#### Ajuste al plan

- La corrección de H1 pasa a ser lo primero que se hace al cerrar la fase de medición: hay
  reproducción determinista, impacto medido y arreglo verificado en el mismo experimento.
- La Fase 5 sube de prioridad: dado que los fallos son visibles y no silenciosos, todo depende del
  tratamiento del error en los llamadores. Es ahí donde un fallo visible se convierte, o no, en
  pérdida de datos para el usuario.
- La Fase 2 (corte de energía) baja aún más: H2 ya estaba refutada y la Fase 1 confirma que la
  atomicidad se sostiene.

### Fase 5 — cerrada el 2026-07-31

Adelantada por delante de las Fases 2-4 según lo decidido al cerrar la Fase 1: como los fallos son
visibles y no silenciosos, todo el daño real depende de qué hace con ellos quien llama.

Instrumentos: `internal/port_in/hook/durability_silent_loss_test.go` y
`internal/port_in/http/durability_daemon_test.go`.

#### La cadena completa, ahora medida de punta a punta

Los hooks no abren la base: hablan por HTTP con el daemon (`kandel serve`). Eso desplaza toda la
concurrencia al daemon, y lo empeora, porque `net/http` levanta **una goroutine por petición**: es
exactamente el escenario `intraproceso` que la Fase 1 midió como el peor.

1. El daemon atiende peticiones en paralelo → conexiones del pool sin `busy_timeout` → `SQLITE_BUSY`.
2. `statusForError` no reconoce ese error, cae en el caso por defecto → **HTTP 500**.
3. El hook hace `code, _ := r.client.savePrompt(...)`: descarta el error y **nunca comprueba si el
   código es 2xx**. Solo lo escribe en el log.
4. El hook devuelve `nil` y termina con éxito.

#### Hallazgo 4 — el daemon pierde casi todo lo que se le solape (severidad alta)

Daemon real, servicio real, SQLite real, 3 corridas por nivel:

| peticiones simultáneas | prompts guardados | perdidos |
|---|---|---|
| 2 | 1, 1, 1 | **50%** |
| 4 | 1, 1, 1 | 75% |
| 8 | 1, 1, 1 | 88% |
| 16 | 1, 2, 1 | 88-94% |
| 60 | 2, 3, 2 | 95-97% |

No es una degradación progresiva bajo carga: **sobrevive prácticamente una sola escritura,
independientemente del nivel de concurrencia**. Con dos peticiones solapadas —la situación más
corriente imaginable— ya se pierde la mitad.

Conviene ser precisos sobre la exposición real: sesenta guardados a la vez no es la carga de un
usuario, y los prompts llegan de uno en uno. Pero el daemon atiende simultáneamente los hooks, el
servidor MCP, el backfill de embeddings y el sync, así que dos o tres escrituras solapadas no son
un caso extremo sino el funcionamiento normal. Y ahí la pérdida ya es del 50%.

La coherencia se mantuvo en todas las corridas: cada 201 se correspondió con una fila. Sigue sin
haber pérdida silenciosa **a nivel de base de datos**. El problema está una capa más arriba.

#### Hallazgo 5 — el hook convierte un fallo visible en silencio (severidad alta)

Con un daemon que rechaza el guardado con HTTP 500, medido:

- `userPromptSubmit` devuelve **`nil`**.
- La salida hacia el agente es **byte a byte indistinguible** de la de un guardado correcto (el
  bloque `hookSpecificOutput` habitual).
- `sessionStart` igual: devuelve `nil` y entrega al agente sus 4905 bytes de protocolo como si el
  arranque hubiera ido bien.
- El **único** rastro es una línea en `~/.kandel/logs/hooks.log`:
  `POST /prompts session_id=… -> HTTP 500`, entre cientos de líneas `HTTP 200` que nadie lee.

La raíz está en `client.postJSON`: devuelve error **solo si la petición no llegó**. Un rechazo del
servidor vuelve como `(código, nil)` —verificado con 500, 400 y 404—, así que el código HTTP es la
única señal existente, y los cinco llamadores la descartan con `code, _ :=`.

El fallo de sesión es el más grave de los dos: si la sesión no se crea, los prompts posteriores
cuelgan de un `session_id` inexistente y, como la clave foránea tampoco se aplica (H1), entran
igualmente y quedan huérfanos.

#### Conclusión de la fase

La promesa de la arquitectura era que la capa determinista "graba sesiones y prompts SIEMPRE, sin
depender del agente". Lo medido es que puede no grabar nada y que nadie se entera. La distinción
entre capa determinista y capa curada sigue siendo correcta en el diseño; lo que falla es la
implementación de la garantía.

Nada de esto requiere un fallo exótico: basta con que dos escrituras se solapen.

#### Ajuste al plan

Las correcciones 1 (PRAGMA por DSN) y una nueva —propagar el fallo del hook— dejan de ser
independientes: la primera elimina la causa, pero la segunda es la que garantiza que la próxima
causa no vuelva a ser invisible. Ambas deben ir juntas.

## 5.bis Correcciones 1 y 2 — aplicadas el 2026-07-31

### Qué se cambió

**Corrección 1 — PRAGMA por DSN** (`internal/adapter/sqlite/db.go`, `hub_store.go`). Los PRAGMA
pasan a viajar en la cadena de conexión, donde el driver los aplica a cada conexión nueva, en vez
de por un `ExecContext` posterior que solo alcanzaba a la primera. `hub_store.go` comparte el mismo
`buildDSN`, así que el servidor de sync queda cubierto por el mismo cambio.

**Corrección 2 — el fallo del hook deja de ser invisible** (`internal/port_in/hook/`). `postJSON`
devuelve error ante cualquier código fuera de 2xx; los diez llamadores dejan de escribir
`code, _ :=` y registran el desenlace con `klogResultado`, que antepone `ERROR` a los fallos para
que se distingan con un vistazo o un grep. Las dos escrituras que no se pueden perder —creación de
sesión y guardado de prompt— se reintentan hasta dos veces (50 ms y 150 ms) ante fallos
transitorios. La ruta de Antigravity (`antigravity.go`) tenía el mismo defecto y se corrigió igual.

Lo que **no** se cambió, a propósito: el hook sigue devolviendo `nil` siempre. Un `exit≠0`
bloquearía el mensaje del usuario en el agente, así que el registro diferenciado es la señal máxima
compatible con esa restricción. Tampoco se inyectan avisos en la salida hacia el agente:
contaminaría el contexto del modelo en cada fallo pasajero.

### El descubrimiento a mitad de camino: `busy_timeout` no bastaba

Con la corrección 1 aplicada, los fallos de apertura desaparecieron y la concurrencia de dos
peticiones pasó a 0% de pérdida, pero por encima de eso el daemon **seguía perdiendo entre el 50% y
el 90%** de los prompts, y el rechazo seguía siendo `database is locked (5) (SQLITE_BUSY)` pese a
tener `busy_timeout = 5000` correctamente aplicado en todas las conexiones.

La causa es el ascenso de una transacción diferida. `database/sql` abre las transacciones con
`BEGIN` (diferido): la transacción toma primero un snapshot de lectura —`AddIfMissing` empieza por
un `SELECT`— y solo pide el bloqueo de escritura al llegar al `INSERT`. Ante ese ascenso SQLite no
puede hacer esperar a quien llama, porque esperar con un snapshot ya tomado puede acabar en
interbloqueo, así que devuelve `SQLITE_BUSY` de inmediato e **ignora el `busy_timeout`**.

La cura es `_txlock=immediate`: la transacción pide el bloqueo de escritura desde el principio y ahí
sí se aplica la espera.

Esto merece quedar escrito porque es contraintuitivo: fijar `busy_timeout` parece resolver la
contención y no lo hace. Sin la medición intermedia, la corrección 1 se habría dado por buena
dejando la mayor parte del problema en pie.

### Verificación

| medición | antes | después |
|---|---|---|
| PRAGMA en las conexiones 1..N del pool | `foreign_keys=0`, `busy_timeout=0` | `1` y `5000` en todas |
| Clave foránea | una memoria huérfana entraba sin error | `FOREIGN KEY constraint failed (787)` |
| Apertura con 6 procesos simultáneos | 0 a 5 de 6 fallaban | 0 de 6, en todas las corridas |
| Escrituras concurrentes intra-proceso | 20.8-25.0% de éxito | 100% |
| Daemon, 2 peticiones simultáneas | 50% de prompts perdidos | 0% |
| Daemon, 60 peticiones simultáneas | 95-97% perdidos | 0% (15 subcorridas) |
| Fallo de guardado en el hook | `nil`, indistinguible del éxito | `nil` + línea `ERROR` + 2 reintentos |

Instalación real tras el cambio: `integrity_check ok`, `foreign_key_check` sin huérfanas, 247
memorias vivas, esquema v6. Sin migración ni conversión: el cambio solo afecta a cómo se abre la
base.

Los tests que en las Fases 0, 1 y 5 solo informaban pasan a **afirmar**: un rechazo por contención o
un fallo de apertura ponen la suite en rojo. La variable `KANDEL_DURABILITY_STRICT` deja de hacer
falta para esos casos y se conserva solo donde el criterio aún depende de una decisión de diseño.

### Efecto secundario que conviene tener presente

Al aplicarse ya las claves foráneas, un prompt cuya sesión no exista se **rechaza** en vez de
entrar huérfano. Es el comportamiento correcto, pero convierte un fallo parcial en uno total para
esa sesión. Se comprobaron los dos caminos que podrían dispararlo:

- **Importación por sync**: `applyMutation` ya vaciaba `SessionID` a propósito —el comentario
  nombra la FK— y los repositorios guardan la cadena vacía como `NULL`, que satisface la
  restricción. Sin riesgo.
- **Sesión que no se creó**: ahora se reintenta (corrección 2), lo que reduce mucho la ventana. La
  base real no tiene ni una fila huérfana, así que el caso no se ha dado en la práctica.

### El prompt huérfano: política decidida y aplicada

Al revisar el efecto secundario apareció un matiz que corrige lo dicho más arriba: la clave foránea
**sí actuaba** en la conexión 0. Un daemon poco cargado reutiliza siempre esa conexión, de modo que
en la práctica la restricción se aplicaba la mayor parte del tiempo y los huérfanos eran la
excepción. La bitácora del proyecto ya recogía el síntoma el 2026-06-27: *"un `POST /prompts` con
`session_id` de una sesión inexistente da HTTP 500 por violación de FK"*, con esta misma pregunta
abierta. El cambio no introdujo el rechazo; lo volvió **consistente**.

Cómo puede llegar un prompt sin sesión, si el hook SessionStart la crea antes que nada:

`userPromptSubmit` **no llamaba a `ensureDaemon`**, y `prepareStartup` sí sale sin crear la sesión
cuando este falla. El arranque en frío es justo el caso: SessionStart lanza `kandel serve` y lo
sondea 3 s; si no responde a tiempo, no hay sesión. El usuario tarda unos segundos en escribir y
para entonces el daemon ya está arriba, así que el prompt viaja con un `session_id` que nunca se
creó, contra un daemon perfectamente sano. Otros caminos: SessionStart que no llega a dispararse
(reanudar conversación, kandel instalado a mitad de sesión) o restaurar la base con una sesión viva.

**Política elegida: degradar a NULL, no rechazar.** El contenido del prompt es el dato durable —lo
que el usuario pidió, que la capa determinista promete registrar siempre—; la sesión es el
contenedor que lo liga a su conversación, la columna ya es anulable y el camino de importación por
sync ya la descarta a propósito. Rechazar sacrificaría lo valioso por preservar lo accesorio.

No se crea la sesión al vuelo: `POST /prompts` no conoce su directorio ni su instante de arranque
reales, y fabricar una fila con datos inventados es peor que no tenerla.

Se aplicaron los dos cambios, uno defensivo y otro de causa raíz:

- **Defensivo** — el adaptador traduce la violación de clave foránea a `domain.ErrUnknownSession`;
  `PromptService.Save` reintenta sin sesión y marca `SessionDropped`; el handler lo registra en
  `serve.log` (`prompt guardado SIN sesión: la sesión "…" no existe`) y lo devuelve en la respuesta.
  La degradación es específica: cualquier otro error de escritura sigue propagándose, porque
  tragárselo reintroduciría la pérdida silenciosa que esta auditoría corrigió.
- **Causa raíz** — `userPromptSubmit` asegura daemon y sesión en el **primer mensaje** de la sesión.
  Solo en el primero: `postSession` es idempotente, pero llamarlo en cada prompt añadiría una
  escritura por mensaje sin ganar nada. `firstMessage` tiene efecto colateral (crea el marcador de
  la sesión), así que se consulta una sola vez y el valor se reutiliza para el recordatorio de tools.

Verificado de punta a punta: un `POST /prompts` con sesión inexistente devuelve `201` con
`"session_dropped":true`, el prompt queda en la base con `session_id` NULL, y el daemon lo registra.
En el hook, el orden observado en el primer mensaje es `[sessions, prompts, backfill]`, y el segundo
prompt de la misma sesión ya no repite la creación.

## 6. Correcciones propuestas

_(Se ordenan por impacto cuando haya mediciones que las respalden.)_

Ordenadas por impacto medido. Las dos primeras están **aplicadas** (ver §5.bis); las demás siguen
pendientes.

1. ~~**PRAGMA por DSN en lugar de `ExecContext`**~~ — APLICADA. (arregla H1, hallazgos 1 y 2 de la Fase 1). El
   driver `modernc.org/sqlite` v1.45 acepta `_pragma=foreign_keys(1)` y los atajos
   `_busy_timeout`, `_synchronous`, `_journal`, y los aplica a **cada** conexión que abre el pool,
   incluida la ventana de arranque donde hoy falla la apertura. Afecta a `db.go` y a
   `hub_store.go`, que comparte la misma variable `pragmas`. El experimento de la Fase 1 ya mide la
   corrección: modo DSN, 100% de éxito en las 9 subcorridas. Al aplicarla, activar el portón con
   `KANDEL_DURABILITY_STRICT=1` y dejarlo en CI.
2. ~~**Que un fallo de escritura del hook deje de ser invisible**~~ — APLICADA. (arregla el hallazgo 5). Va junto
   con la corrección 1, no después: la primera elimina la causa actual, la segunda garantiza que la
   próxima causa no vuelva a ser silenciosa. Mínimo: que `postJSON` devuelva error ante un código
   no-2xx, y que los llamadores dejen de escribir `code, _ :=`. Qué hacer con ese error es decisión
   aparte —un hook no puede terminar con exit≠0 sin bloquear al usuario—, pero como mínimo debe
   quedar registrado como fallo distinguible y, preferiblemente, reintentado.
3. **Fijar `synchronous` de forma explícita** y documentar el valor elegido. Hoy funciona por un
   valor por defecto del driver que nadie eligió (FULL) y que una actualización podría cambiar.
4. **Un chequeo de base de datos en `doctor`**: integridad, tamaño del WAL y retraso de la cola de
   sync (hoy 197 mutaciones pendientes y nadie lo mira).
5. **Un comando `kandel backup`** y un `PRAGMA wal_checkpoint(TRUNCATE)` periódico.
6. **Purga de `sync_mutations` ya confirmadas**, que hoy se acumulan sin techo.
