package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// loadJSONObject lee un archivo JSON como objeto (map). Si el archivo no existe devuelve un mapa
// vacío (no es error: el instalador lo creará al guardar). Un JSON que no sea un objeto es error.
func loadJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("leer %q: %w", path, err)
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, fmt.Errorf("%q no es un objeto JSON válido: %w", path, err)
	}
	if obj == nil {
		obj = map[string]any{}
	}
	return obj, nil
}

// saveJSONObject escribe el objeto como JSON indentado, creando el directorio padre si falta.
func saveJSONObject(path string, obj map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("crear directorio de %q: %w", path, err)
	}
	data, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("serializar %q: %w", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("escribir %q: %w", path, err)
	}
	return nil
}

// mapAt devuelve la sub-clave key de m como map, creándola si no existe o no es un objeto.
func mapAt(m map[string]any, key string) map[string]any {
	if existing, ok := m[key].(map[string]any); ok {
		return existing
	}
	created := map[string]any{}
	m[key] = created
	return created
}
