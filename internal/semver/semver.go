// Package semver ofrece una comparación mínima de versiones SemVer (MAJOR.MINOR.PATCH), lo justo
// para decidir si una versión es más nueva que otra (§21 Eje 6). No implementa el estándar completo:
// ignora pre-release y build metadata (los recorta), que no se usan en los tags de release de kandel.
package semver

import (
	"strconv"
	"strings"
)

// Version es una versión SemVer descompuesta.
type Version struct {
	Major, Minor, Patch int
}

// Parse interpreta una cadena "vX.Y.Z" o "X.Y.Z" (tolera el prefijo "v" y recorta cualquier sufijo
// de pre-release/build tras "-" o "+"). ok es false si no tiene la forma esperada de tres números.
func Parse(s string) (Version, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	var v Version
	var err error
	if v.Major, err = strconv.Atoi(parts[0]); err != nil {
		return Version{}, false
	}
	if v.Minor, err = strconv.Atoi(parts[1]); err != nil {
		return Version{}, false
	}
	if v.Patch, err = strconv.Atoi(parts[2]); err != nil {
		return Version{}, false
	}
	return v, true
}

// Less indica si a es estrictamente menor (más vieja) que b. Si CUALQUIERA de las dos no se puede
// parsear (p. ej. "dev" en una build local), devuelve false: sin certeza, no se afirma que a<b, de
// modo que el aviso de versión nueva nunca salta por comparaciones dudosas.
func Less(a, b string) bool {
	av, ok := Parse(a)
	if !ok {
		return false
	}
	bv, ok := Parse(b)
	if !ok {
		return false
	}
	if av.Major != bv.Major {
		return av.Major < bv.Major
	}
	if av.Minor != bv.Minor {
		return av.Minor < bv.Minor
	}
	return av.Patch < bv.Patch
}
