// Command kandel es el punto de entrada del binario. Construye las dependencias
// (almacén, embebedor, servicios) y delega el despacho en el adaptador de línea de
// comandos. En la Fase 0 solo imprime la versión: el despacho real llega en la fase CLI.
package main

import "fmt"

// version se inyecta en tiempo de compilación con -ldflags "-X main.version=...".
// Mantiene el valor "dev" en construcciones locales sin release.
var version = "dev"

func main() {
	// Andamiaje de la Fase 0. Cuando exista el paquete port_in/cli, este main
	// construirá la App con sus servicios y llamará al despachador de comandos.
	fmt.Printf("kandel %s\n", version)
}
