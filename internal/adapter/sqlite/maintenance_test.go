package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/javierv-dev/kandel/internal/diagnostic"
)

// Regresión: Sweep y DatabaseCheck usaban la columna inexistente confirmed_at (la real es acked_at),
// de modo que la purga fallaba en cada barrido y el chequeo ignoraba el error en silencio.

func countMutations(t *testing.T, db *DB, where string) int {
	t.Helper()
	var n int
	q := "SELECT COUNT(*) FROM sync_mutations"
	if where != "" {
		q += " WHERE " + where
	}
	if err := db.sql.QueryRow(q).Scan(&n); err != nil {
		t.Fatalf("contar mutaciones (%s): %v", where, err)
	}
	return n
}

func TestSweepPurgesOnlyAckedMutations(t *testing.T) {
	db := newTestDB(t)
	repo := NewSyncRepo(db)
	uow := NewUnitOfWork(db)
	ctx := context.Background()

	enqueue(t, uow, repo, sampleMutation("mem-1"))
	enqueue(t, uow, repo, sampleMutation("mem-2"))
	enqueue(t, uow, repo, sampleMutation("mem-3"))

	pending, err := repo.ListPending(ctx, "remote", 0, 10)
	if err != nil || len(pending) != 3 {
		t.Fatalf("ListPending: %d pendientes, err=%v", len(pending), err)
	}
	// Confirmamos solo las dos primeras.
	if err := repo.Ack(ctx, "remote", []int64{pending[0].Seq, pending[1].Seq}); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	purged, err := NewMaintenance(db).Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if purged != 2 {
		t.Errorf("esperaba 2 mutaciones purgadas, obtuve %d", purged)
	}
	if n := countMutations(t, db, ""); n != 1 {
		t.Errorf("deberían quedar 1 mutación, quedan %d", n)
	}
	if n := countMutations(t, db, "acked_at IS NULL"); n != 1 {
		t.Errorf("la mutación no confirmada debe conservarse, pendientes=%d", n)
	}
}

func TestSweepWithoutAckedMutationsPurgesNothing(t *testing.T) {
	db := newTestDB(t)
	repo := NewSyncRepo(db)
	uow := NewUnitOfWork(db)
	enqueue(t, uow, repo, sampleMutation("mem-1"))

	purged, err := NewMaintenance(db).Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if purged != 0 || countMutations(t, db, "") != 1 {
		t.Errorf("no debía purgar nada: purged=%d", purged)
	}
}

func TestDatabaseCheckReportsPendingMutations(t *testing.T) {
	db := newTestDB(t)
	repo := NewSyncRepo(db)
	uow := NewUnitOfWork(db)
	ctx := context.Background()

	for i := 0; i < 51; i++ {
		enqueue(t, uow, repo, sampleMutation("mem-"+string(rune('a'+i%26))+string(rune('a'+i/26))))
	}

	findings := NewDatabaseCheck(db).Run(ctx)
	if !hasFinding(findings, diagnostic.SeverityWarning, "mutaciones pendientes") {
		t.Fatalf("esperaba aviso de mutaciones pendientes, obtuve %+v", findings)
	}

	// Al confirmar todas, el aviso desaparece.
	pending, err := repo.ListPending(ctx, "remote", 0, 100)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	seqs := make([]int64, len(pending))
	for i, m := range pending {
		seqs[i] = m.Seq
	}
	if err := repo.Ack(ctx, "remote", seqs); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	findings = NewDatabaseCheck(db).Run(ctx)
	if hasFinding(findings, diagnostic.SeverityWarning, "mutaciones pendientes") {
		t.Errorf("no debía avisar con todo confirmado: %+v", findings)
	}
	if hasFinding(findings, diagnostic.SeverityError, "") {
		t.Errorf("no debía haber errores: %+v", findings)
	}
}

func hasFinding(fs []diagnostic.Finding, sev diagnostic.Severity, substr string) bool {
	for _, f := range fs {
		if f.Severity == sev && strings.Contains(f.Message, substr) {
			return true
		}
	}
	return false
}
