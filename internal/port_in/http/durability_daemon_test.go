package http

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/javierv-dev/kandel/internal/adapter/clock"
	"github.com/javierv-dev/kandel/internal/adapter/sqlite"
	"github.com/javierv-dev/kandel/internal/service"

	_ "modernc.org/sqlite"
)

// --- Fase 5 de la auditoría de durabilidad (docs/auditoria-durabilidad.md) ---
//
// Cierra el eslabón intermedio de la cadena. Los otros dos ya están medidos:
//
//   - Fase 1: con concurrencia dentro de un proceso, las conexiones que abre el pool no llevan
//     busy_timeout y rechazan entre el 75% y el 79% de las escrituras.
//   - Fase 5 (paquete hook): el hook descarta el error y no comprueba el código HTTP.
//
// Falta comprobar con el daemon REAL —net/http, una goroutine por petición, servicio real,
// SQLite real— que ese rechazo se convierte efectivamente en un HTTP 500 hacia el hook. Sin este
// test, esa parte sería lectura de código y no medición.

// TestDurabilityDaemonReturns500UnderConcurrency golpea el daemon real con peticiones simultáneas
// de guardado de prompt y cuenta cuántas devuelven un código de error.
//
// Se barre el nivel de concurrencia a propósito. Sesenta guardados a la vez no es la carga de un
// usuario —los prompts llegan de uno en uno—, así que quedarse solo con ese número exageraría el
// hallazgo. Lo que importa es dónde EMPIEZA a degradarse: el daemon atiende a la vez los hooks, el
// servidor MCP, el backfill de embeddings y el sync, así que dos o tres escrituras solapadas son
// una situación corriente, no un caso extremo.
func TestDurabilityDaemonReturns500UnderConcurrency(t *testing.T) {
	for _, n := range []int{2, 4, 8, 16, 60} {
		t.Run(fmt.Sprintf("concurrencia-%d", n), func(t *testing.T) {
			runDaemonConcurrency(t, n)
		})
	}
}

// runDaemonConcurrency ejecuta una corrida con el nivel de concurrencia dado.
func runDaemonConcurrency(t *testing.T, peticiones int) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "daemon.db")

	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Cableado real, el mismo que arma cmd/kandel/main.go para el daemon.
	uow := sqlite.NewUnitOfWork(db)
	promptSvc := service.NewPromptService(
		sqlite.NewPromptRepo(db),
		sqlite.NewSyncRepo(db),
		uow,
		clock.System{},
	)

	// La sesión tiene que existir de verdad: desde que los PRAGMA van por el DSN, la clave foránea
	// prompts.session_id → sessions.id se aplica en TODAS las conexiones del pool, y un prompt
	// colgado de una sesión inexistente se rechaza (antes entraba y quedaba huérfano).
	sesionSvc := service.NewSessionService(
		sqlite.NewSessionRepo(db), sqlite.NewSyncRepo(db), uow, clock.System{},
	)
	if _, err := sesionSvc.Start(ctx, service.StartInput{
		ID: "s-fase5", Project: "kandel", Directory: t.TempDir(),
	}); err != nil {
		t.Fatalf("crear sesión: %v", err)
	}
	srv := httptest.NewServer(NewHandler(Services{Prompt: promptSvc}))
	defer srv.Close()

	var wg sync.WaitGroup
	codigos := make([]int, peticiones)
	cuerpos := make([]string, peticiones)
	errores := make([]error, peticiones)
	// Todas las goroutines esperan la misma señal para empujar a la vez.
	arranque := make(chan struct{})
	for i := 0; i < peticiones; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-arranque
			// Cada prompt es distinto: el guardado es idempotente y contenidos repetidos no
			// insertarían nada, falseando el recuento.
			cuerpo := fmt.Sprintf(`{"session_id":"s-fase5","project":"kandel","content":"prompt numero %d"}`, i)
			resp, err := http.Post(srv.URL+"/prompts", "application/json", strings.NewReader(cuerpo))
			if err != nil {
				errores[i] = err
				return
			}
			defer resp.Body.Close()
			codigos[i] = resp.StatusCode
			if resp.StatusCode != http.StatusCreated {
				raw, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
				cuerpos[i] = string(raw)
			}
		}(i)
	}
	close(arranque)
	wg.Wait()

	porCodigo := map[int]int{}
	fallosRed := 0
	for i := range codigos {
		if errores[i] != nil {
			fallosRed++
			continue
		}
		porCodigo[codigos[i]]++
	}

	// Contar lo que de verdad quedó guardado. Se abre una conexión aparte porque el *sql.DB del
	// adaptador es privado a su paquete.
	filas := contarPrompts(t, path)

	creados := porCodigo[http.StatusCreated]
	rechazos := peticiones - creados

	t.Logf("peticiones simultáneas: %d", peticiones)
	for code, n := range porCodigo {
		t.Logf("  HTTP %d  %d", code, n)
	}
	if fallosRed > 0 {
		t.Logf("  fallos de red  %d", fallosRed)
	}
	t.Logf("  prompts guardados  %d de %d (%.0f%% perdidos)",
		filas, peticiones, 100*float64(rechazos)/float64(peticiones))
	for _, c := range cuerpos {
		if c != "" {
			t.Logf("  ejemplo de rechazo: %s", strings.TrimSpace(c))
			break
		}
	}

	// Coherencia: un 201 tiene que corresponderse con una fila. Si no, habría pérdida SILENCIOSA,
	// que es otra categoría de problema. Se afirma siempre.
	if filas != creados {
		t.Errorf("INCOHERENCIA: %d respuestas 201 pero %d prompts en la base", creados, filas)
	}

	// PORTÓN: tras las correcciones 1 y 2, ninguna escritura debe perderse por contención. Se afirma
	// siempre, no solo en modo estricto: es la regresión que protege el arreglo. Si vuelve a
	// aparecer un rechazo aquí, algo revirtió los PRAGMA del DSN o el _txlock=immediate.
	if rechazos > 0 {
		t.Errorf("%d de %d prompts perdidos por contención; ninguna escritura debería rechazarse",
			rechazos, peticiones)
	}
}

// contarPrompts abre la base por su cuenta y cuenta las filas de prompts.
func contarPrompts(t *testing.T, path string) int {
	t.Helper()
	sqldb, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatalf("abrir para contar: %v", err)
	}
	defer sqldb.Close()
	var n int
	if err := sqldb.QueryRow(`SELECT COUNT(*) FROM prompts`).Scan(&n); err != nil {
		t.Fatalf("contar prompts: %v", err)
	}
	return n
}

// TestDurabilityPromptHuerfanoSeGuardaSinSesion verifica de punta a punta la política elegida para
// un prompt cuya sesión no existe: se conserva el contenido y se sacrifica el enlace, en vez de
// devolver 500 y perder lo que el usuario pidió.
//
// Antes de aplicarse las claves foráneas en todas las conexiones, este prompt entraba huérfano en
// unas conexiones y se rechazaba en otras, según cuál del pool lo atendiera. Ahora el resultado es
// uno solo y está definido.
func TestDurabilityPromptHuerfanoSeGuardaSinSesion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "huerfano.db")

	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	promptSvc := service.NewPromptService(
		sqlite.NewPromptRepo(db), sqlite.NewSyncRepo(db), sqlite.NewUnitOfWork(db), clock.System{},
	)
	srv := httptest.NewServer(NewHandler(Services{Prompt: promptSvc}))
	defer srv.Close()

	// Deliberadamente NO se crea la sesión.
	cuerpo := `{"session_id":"sesion-inexistente","project":"kandel","content":"no me pierdas"}`
	resp, err := http.Post(srv.URL+"/prompts", "application/json", strings.NewReader(cuerpo))
	if err != nil {
		t.Fatalf("POST /prompts: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
	t.Logf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, se esperaba 201: el prompt no debe perderse porque falte su sesión",
			resp.StatusCode)
	}
	if !strings.Contains(string(raw), `"session_dropped":true`) {
		t.Errorf("la respuesta no avisa de que se descartó la sesión: %s", raw)
	}

	if n := contarPrompts(t, path); n != 1 {
		t.Fatalf("hay %d prompts en la base, se esperaba 1", n)
	}
	if sid := sesionDelPrompt(t, path); sid.Valid {
		t.Errorf("session_id = %q; debería haber quedado NULL", sid.String)
	}
}

// sesionDelPrompt devuelve el session_id del único prompt de la base.
func sesionDelPrompt(t *testing.T, path string) sql.NullString {
	t.Helper()
	sqldb, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatalf("abrir para leer: %v", err)
	}
	defer sqldb.Close()
	var sid sql.NullString
	if err := sqldb.QueryRow(`SELECT session_id FROM prompts LIMIT 1`).Scan(&sid); err != nil {
		t.Fatalf("leer session_id: %v", err)
	}
	return sid
}
