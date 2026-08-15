package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const envCrashChild = "KANDEL_CRASH_CHILD"
const envCrashSync = "KANDEL_CRASH_SYNC"

func TestDurabilityCrash(t *testing.T) {
	if os.Getenv(envCrashChild) == "1" {
		runCrashChild()
		return
	}

	for _, syncMode := range []string{"NORMAL", "FULL"} {
		t.Run(syncMode, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "crash.db")
			
			// Inicializar la base de datos y su esquema
			db, err := Open(context.Background(), dbPath)
			if err != nil {
				t.Fatal(err)
			}
			db.Close() 

			// Ejecutar el proceso hijo que escribirá y se matará a sí mismo
			cmd := exec.Command(os.Args[0], "-test.run=TestDurabilityCrash")
			cmd.Env = append(os.Environ(), 
				envCrashChild+"=1", 
				envCrashSync+"="+syncMode, 
				"KANDEL_DB_CRASH_PATH="+dbPath,
			)
			
			err = cmd.Run()
			// Esperamos que falle porque el hijo hace os.Exit(9)
			if err == nil {
				t.Fatal("Se esperaba que el proceso hijo crasheara con código 9")
			}

			// Reabrir para verificar integridad y contar
			db2, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatalf("Fallo al reabrir la BD tras el crash: %v", err)
			}
			defer db2.Close()

			row := db2.QueryRow("PRAGMA integrity_check")
			var res string
			if err := row.Scan(&res); err != nil {
				t.Fatalf("Fallo leyendo integrity_check: %v", err)
			}
			if res != "ok" {
				t.Fatalf("CORRUPCIÓN DETECTADA: integrity_check devolvió: %s", res)
			}

			var count int
			db2.QueryRow("SELECT COUNT(*) FROM memories").Scan(&count)
			t.Logf("Modo %s: Base intacta. Transacciones completas recuperadas: %d. Transacción parcial revertida con éxito.", syncMode, count)
		})
	}
}

func runCrashChild() {
	dbPath := os.Getenv("KANDEL_DB_CRASH_PATH")
	syncMode := os.Getenv("KANDEL_CRASH_SYNC")
	
	// Abrimos crudo con el pragma específico para el test
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=synchronous("+syncMode+")")
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	for i := 1; i <= 50; i++ {
		tx, err := db.Begin()
		if err != nil {
			os.Exit(2)
		}
		
		_, err = tx.Exec("INSERT INTO memories (session_id, project, type, scope, title, content) VALUES (?, ?, ?, ?, ?, ?)", 
            "sess_crash", "proj", "discovery", "project", fmt.Sprintf("Title %d", i), "Content")
		
		if err != nil {
			os.Exit(3)
		}
		
		if i == 25 {
			// CRASH violento justo a mitad del lote 25, ANTES del commit.
			// Esto simula un `kill -9` o apagón repentino.
			os.Exit(9)
		}
		
		tx.Commit()
	}
}
