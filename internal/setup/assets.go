package setup

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// writeAsset escribe un único archivo embebido (assetPath dentro de assets/) en dest con el modo
// dado, creando el directorio padre si falta.
func writeAsset(assetPath, dest string, mode os.FileMode) error {
	data, err := assets.ReadFile(assetPath)
	if err != nil {
		return fmt.Errorf("leer asset %q: %w", assetPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("crear directorio de %q: %w", dest, err)
	}
	if err := os.WriteFile(dest, data, mode); err != nil {
		return fmt.Errorf("escribir %q: %w", dest, err)
	}
	return nil
}

// writeAssetTree copia todos los archivos de un directorio embebido (assetDir) a destDir con el
// modo dado. Devuelve cuántos archivos escribió.
func writeAssetTree(assetDir, destDir string, mode os.FileMode) (int, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return 0, fmt.Errorf("crear directorio %q: %w", destDir, err)
	}
	count := 0
	err := fs.WalkDir(assets, assetDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		dest := filepath.Join(destDir, d.Name())
		if err := writeAsset(path, dest, mode); err != nil {
			return err
		}
		count++
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}
