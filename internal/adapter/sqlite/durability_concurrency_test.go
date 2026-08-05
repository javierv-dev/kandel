package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Fase 1 de la auditoría de durabilidad (docs/auditoria-durabilidad.md) ---
//
// Mide qué ocurre cuando VARIOS PROCESOS escriben a la vez en la misma base, que es la situación
// real de kandel: los hooks son procesos cortos, el servidor MCP es un proceso largo, y CLI, TUI y
// serve son procesos aparte, todos contra ~/.kandel/kandel.db.
//
// La Fase 0 confirmó que las conexiones que abre el pool después de la primera tienen
// busy_timeout = 0, es decir, se rinden ante un bloqueo sin esperar ni un milisegundo. Esta fase
// mide el efecto: cuántas escrituras fracasan, y sobre todo si alguna que se reportó como exitosa
// no está en la base (pérdida silenciosa, que sería mucho más grave que un error visible).
//
// Se usan procesos de verdad, no goroutines: dentro de un mismo proceso todas las escrituras
// comparten el pool de database/sql y el bloqueo lo arbitra Go, no SQLite. El experimento solo es
// fiel si cada escritor tiene su propio pool y su propio archivo de bloqueo.

const (
	envChildDB       = "KANDEL_PROBE_DB"
	envChildWrites   = "KANDEL_PROBE_WRITES"
	envChildID       = "KANDEL_PROBE_ID"
	envChildMode     = "KANDEL_PROBE_MODE"
	envChildStart    = "KANDEL_PROBE_START"
	envChildHold     = "KANDEL_PROBE_HOLD_MS"
	envChildParallel = "KANDEL_PROBE_PARALLEL"

	// modeOpen usa sqlite.Open, el camino real de producción. Cuando se escribió este test, Open
	// aplicaba los PRAGMA con ExecContext sobre el *sql.DB y este modo medía el defecto; desde la
	// corrección 1 los lleva en el DSN y el modo es la regresión que lo protege.
	modeOpen = "open"
	// modeDSN abre con los PRAGMA en el DSN sin pasar por Open. Se conserva como control explícito:
	// si algún día Open volviera a fallar, la comparación diría de inmediato si la culpa es de la
	// forma de aplicar los PRAGMA o de otra cosa.
	modeDSN = "dsn"
)

// writeOutcome es el recuento que cada proceso escritor devuelve a su padre.
type writeOutcome struct {
	ok       int // escrituras que devolvieron nil
	busy     int // rechazadas por bloqueo (SQLITE_BUSY / database is locked)
	other    int // cualquier otro error
	openFail int // 1 si el proceso ni siquiera pudo ABRIR la base
}

// TestDurabilityConcurrentProcessWrites lanza varios procesos escritores contra la misma base y
// compara las escrituras reportadas como exitosas con las filas que quedaron.
//
// Corre en tres escenarios y dos modos de apertura, para que la diferencia sea evidencia medida y
// no un argumento sobre el papel:
//
//   - instantanea: transacciones que no retienen el bloqueo. El caso optimista.
//   - retenida: transacciones que lo retienen unas decenas de milisegundos, como cuando una unidad
//     de trabajo abarca varias escrituras y cálculos entre medias.
//   - intraproceso: además, varias escrituras EN PARALELO dentro de cada proceso. Es el escenario
//     que de verdad expone H1: un proceso que escribe en serie reutiliza siempre la MISMA conexión
//     del pool (la primera, la única que quedó configurada), así que el defecto no se manifiesta.
//     El pool solo abre conexiones adicionales cuando hay concurrencia DENTRO del proceso, que es
//     exactamente lo que hace el servidor MCP al atender peticiones en paralelo.
func TestDurabilityConcurrentProcessWrites(t *testing.T) {
	if os.Getenv(envChildDB) != "" {
		t.Skip("proceso hijo: lo ejecuta TestDurabilityWriterChild")
	}

	const escritores = 6

	escenarios := []struct {
		nombre         string
		escriturasCada int
		holdMs         int
		paralelas      int
	}{
		{"instantanea", 40, 0, 1},
		{"retenida", 12, 25, 1},
		{"intraproceso", 12, 25, 4},
	}

	for _, esc := range escenarios {
		for _, modo := range []string{modeOpen, modeDSN} {
			t.Run(esc.nombre+"/"+modo, func(t *testing.T) {
				runConcurrencyScenario(t, modo, escritores, esc.escriturasCada, esc.holdMs, esc.paralelas)
			})
		}
	}
}

// runConcurrencyScenario ejecuta una corrida del experimento y reporta sus cifras.
func runConcurrencyScenario(t *testing.T, modo string, escritores, escriturasCada, holdMs, paralelas int) {
	t.Helper()
	ctx := context.Background()
	esperadas := escritores * escriturasCada
	path := filepath.Join(t.TempDir(), "concurrencia.db")

	// El padre crea y migra la base, y siembra la sesión a la que apuntan las memorias.
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := db.sql.ExecContext(ctx,
		`INSERT INTO sessions (id, project, directory, started_at) VALUES (?, ?, ?, ?)`,
		"sesion-concurrencia", "kandel", "/tmp", formatTime(baseTime),
	); err != nil {
		t.Fatalf("sembrar sesión: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Barrera: todos los hijos esperan al mismo instante para arrancar. Sin esto, el arranque
	// escalonado de los procesos reduce el solapamiento y el experimento mide menos contención de
	// la real.
	arranque := time.Now().Add(1500 * time.Millisecond)

	var wg sync.WaitGroup
	resultados := make([]writeOutcome, escritores)
	fallos := make([]error, escritores)
	for i := 0; i < escritores; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resultados[i], fallos[i] = runWriterChild(t, path, modo, i, escriturasCada, holdMs, paralelas, arranque)
		}(i)
	}
	wg.Wait()

	var total writeOutcome
	for i, err := range fallos {
		if err != nil {
			t.Fatalf("escritor %d: %v", i, err)
		}
		total.ok += resultados[i].ok
		total.busy += resultados[i].busy
		total.other += resultados[i].other
		total.openFail += resultados[i].openFail
	}

	// Contar lo que realmente quedó en la base.
	verif, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reabrir: %v", err)
	}
	defer verif.Close()
	var filas int
	if err := verif.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memories WHERE session_id = ?`, "sesion-concurrencia",
	).Scan(&filas); err != nil {
		t.Fatalf("contar memorias: %v", err)
	}

	t.Logf("modo=%s escritores=%d escrituras/escritor=%d hold=%dms paralelas=%d (intentos=%d)",
		modo, escritores, escriturasCada, holdMs, paralelas, esperadas)
	t.Logf("  procesos que no abrieron  %d de %d", total.openFail, escritores)
	t.Logf("  reportadas ok             %d", total.ok)
	t.Logf("  rechazadas (busy)         %d", total.busy)
	t.Logf("  otros errores             %d", total.other)
	t.Logf("  filas en la base          %d", filas)
	if esperadas > 0 {
		t.Logf("  tasa de éxito             %.1f%%", 100*float64(total.ok)/float64(esperadas))
	}

	// Un proceso que no consigue ABRIR la base pierde todo su trabajo, no una escritura suelta. En
	// un hook —que es fuego y olvido— eso significa un prompt o una sesión que nunca se registró.
	if total.openFail > 0 {
		t.Errorf("%d de %d procesos no pudieron abrir la base (modo %s); la apertura concurrente "+
			"no debe fallar", total.openFail, escritores, modo)
	}

	// Lo verdaderamente grave sería que una escritura confirmada no esté. Eso sí es pérdida de
	// datos, y se afirma SIEMPRE (no detrás de la variable estricta): si SQLite dejara de ser
	// atómico, esta suite tiene que ponerse en rojo sin excusas.
	if filas != total.ok {
		t.Errorf("PÉRDIDA SILENCIOSA: %d escrituras reportadas como exitosas pero %d filas en la base",
			total.ok, filas)
	}

	// Que haya rechazos por bloqueo no es pérdida de datos, pero sí es la consecuencia visible de
	// H1: con busy_timeout = 0 el escritor se rinde de inmediato en vez de esperar su turno.
	if total.busy > 0 {
		t.Errorf("%d de %d escrituras rechazadas por bloqueo (modo %s, hold %dms); con busy_timeout "+
			"y _txlock=immediate ninguna debería rechazarse", total.busy, esperadas, modo, holdMs)
	}
}

// runWriterChild lanza un proceso escritor (el propio binario de test, reejecutado sobre
// TestDurabilityWriterChild) y devuelve su recuento.
func runWriterChild(t *testing.T, path, modo string, id, escrituras, holdMs, paralelas int, arranque time.Time) (writeOutcome, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestDurabilityWriterChild$", "-test.v=false")
	cmd.Env = append(os.Environ(),
		envChildDB+"="+path,
		envChildWrites+"="+strconv.Itoa(escrituras),
		envChildID+"="+strconv.Itoa(id),
		envChildMode+"="+modo,
		envChildHold+"="+strconv.Itoa(holdMs),
		envChildParallel+"="+strconv.Itoa(paralelas),
		envChildStart+"="+strconv.FormatInt(arranque.UnixNano(), 10),
	)
	salida, err := cmd.CombinedOutput()
	if err != nil {
		return writeOutcome{}, fmt.Errorf("ejecutar hijo: %w\nsalida:\n%s", err, salida)
	}
	return parseOutcome(string(salida))
}

// parseOutcome extrae la línea RESULT de la salida del hijo.
func parseOutcome(salida string) (writeOutcome, error) {
	for _, linea := range strings.Split(salida, "\n") {
		linea = strings.TrimSpace(linea)
		if !strings.HasPrefix(linea, "RESULT ") {
			continue
		}
		var out writeOutcome
		if _, err := fmt.Sscanf(linea, "RESULT ok=%d busy=%d other=%d openfail=%d",
			&out.ok, &out.busy, &out.other, &out.openFail); err != nil {
			return writeOutcome{}, fmt.Errorf("interpretar %q: %w", linea, err)
		}
		return out, nil
	}
	return writeOutcome{}, fmt.Errorf("el hijo no emitió una línea RESULT; salida:\n%s", salida)
}

// TestDurabilityWriterChild es el cuerpo del proceso escritor. Solo hace algo cuando el padre lo
// invoca con las variables de entorno puestas; en una corrida normal de la suite se salta.
func TestDurabilityWriterChild(t *testing.T) {
	path := os.Getenv(envChildDB)
	if path == "" {
		t.Skip("no es un proceso hijo")
	}
	ctx := context.Background()

	escrituras, err := strconv.Atoi(os.Getenv(envChildWrites))
	if err != nil {
		t.Fatalf("%s inválido: %v", envChildWrites, err)
	}
	id := os.Getenv(envChildID)

	// hold retiene el bloqueo de escritura dentro de la transacción. Con transacciones que duran
	// microsegundos, seis procesos casi nunca coinciden y el experimento no probaría nada.
	hold := time.Duration(0)
	if ms, err := strconv.Atoi(os.Getenv(envChildHold)); err == nil && ms > 0 {
		hold = time.Duration(ms) * time.Millisecond
	}
	paralelas := 1
	if p, err := strconv.Atoi(os.Getenv(envChildParallel)); err == nil && p > 1 {
		paralelas = p
	}

	// Esperar a la barrera para que todos los escritores empujen a la vez. Se espera ANTES de
	// abrir: la apertura es parte de lo que se mide, porque es donde compiten los hooks al
	// arrancar todos a la vez.
	if nanos, err := strconv.ParseInt(os.Getenv(envChildStart), 10, 64); err == nil {
		if espera := time.Until(time.Unix(0, nanos)); espera > 0 {
			time.Sleep(espera)
		}
	}

	db, err := openForMode(ctx, path, os.Getenv(envChildMode))
	if err != nil {
		// No abrir la base es un resultado del experimento, no un fallo del experimento: se
		// reporta al padre y se sale limpio.
		fmt.Printf("RESULT ok=0 busy=0 other=0 openfail=1\n")
		fmt.Printf("MOTIVO %v\n", err)
		return
	}
	defer db.Close()

	// Se escribe a través de UnitOfWork porque es el camino real de kandel: cada memoria se guarda
	// dentro de una transacción, que retiene el bloqueo de escritura más tiempo que un INSERT
	// suelto. Medir con INSERT pelado subestimaría la contención.
	uow := NewUnitOfWork(db)
	var mu sync.Mutex
	var out writeOutcome

	// Repartir las escrituras entre goroutines. Con paralelas > 1, el pool de database/sql se ve
	// obligado a abrir conexiones adicionales, que son justamente las que no llevan los PRAGMA.
	var wg sync.WaitGroup
	for g := 0; g < paralelas; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := g; i < escrituras; i += paralelas {
				err := uow.Do(ctx, func(ctx context.Context) error {
					_, err := execFromCtx(ctx, db.sql).ExecContext(ctx,
						`INSERT INTO memories (session_id, type, title, content, created_at, updated_at)
						 VALUES (?, ?, ?, ?, ?, ?)`,
						"sesion-concurrencia", "discovery",
						fmt.Sprintf("escritor %s memoria %d", id, i), "cuerpo de prueba",
						formatTime(baseTime), formatTime(baseTime),
					)
					if err != nil {
						return err
					}
					if hold > 0 {
						time.Sleep(hold)
					}
					return nil
				})
				mu.Lock()
				switch {
				case err == nil:
					out.ok++
				case esBloqueo(err):
					out.busy++
				default:
					out.other++
				}
				mu.Unlock()
			}
		}(g)
	}
	wg.Wait()

	fmt.Printf("RESULT ok=%d busy=%d other=%d openfail=0\n", out.ok, out.busy, out.other)
}

// esBloqueo distingue el rechazo por contención de cualquier otro fallo. modernc.org/sqlite
// devuelve textos del tipo "database is locked (5) (SQLITE_BUSY)".
func esBloqueo(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked")
}

// openForMode abre la base como lo hace kandel hoy (modeLegacy) o pasando los PRAGMA por el DSN
// (modeDSN). Deliberadamente NO toca el código de producción: la auditoría mide, y la corrección
// va por su propio cambio si las mediciones la justifican.
func openForMode(ctx context.Context, path, modo string) (*DB, error) {
	if modo != modeDSN {
		return Open(ctx, path)
	}
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("abrir %q por DSN: %w", path, err)
	}
	if err := sqldb.PingContext(ctx); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("conectar %q por DSN: %w", path, err)
	}
	// El esquema ya lo creó el padre; aquí solo se escribe.
	return &DB{sql: sqldb}, nil
}
