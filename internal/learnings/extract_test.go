package learnings

import "testing"

func TestExtract(t *testing.T) {
	t.Run("viñetas mixtas tras encabezado", func(t *testing.T) {
		got := Extract("Aprendizajes:\n- uno\n* dos\n• tres\n\notra cosa")
		want := []string{"uno", "dos", "tres"}
		if len(got) != len(want) {
			t.Fatalf("esperados %d items, fueron %d: %v", len(want), len(got), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("item %d: esperado %q, fue %q", i, want[i], got[i])
			}
		}
	})

	t.Run("encabezado markdown en inglés", func(t *testing.T) {
		got := Extract("## Key Learnings:\n- algo aprendido aquí\n")
		if len(got) != 1 || got[0] != "algo aprendido aquí" {
			t.Fatalf("esperado un item, fue %v", got)
		}
	})

	t.Run("sin sección devuelve nil", func(t *testing.T) {
		if got := Extract("texto sin aprendizajes\n- viñeta suelta"); got != nil {
			t.Errorf("esperado nil, fue %v", got)
		}
	})

	t.Run("nuevo encabezado cierra la sección", func(t *testing.T) {
		got := Extract("Learnings:\n- primero\n## Otra sección\n- segundo")
		if len(got) != 1 || got[0] != "primero" {
			t.Fatalf("esperado solo el primero, fue %v", got)
		}
	})
}

func TestTruncate(t *testing.T) {
	if got := Truncate("corto", 10); got != "corto" {
		t.Errorf("no debe recortar lo corto, fue %q", got)
	}
	if got := Truncate("abcdef", 3); got != "abc…" {
		t.Errorf("esperado abc…, fue %q", got)
	}
}
