# Plan de Auditoría: Arquitectura Hexagonal

Este documento detalla el plan de acción para ejecutar la **Auditoría Arquitectónica** del punto 2 de nuestras propuestas. Dado que Kandel es estricto con su arquitectura (según indica el `README.md`), el objetivo es asegurar que la "Regla de Dependencia" apunte siempre hacia el centro del hexágono y que no existan fugas de responsabilidades (ej. el dominio acoplado a bases de datos).

---

## 🎯 Objetivos de la Auditoría
1. **Proteger el Dominio:** Garantizar que las entidades puras (`internal/domain`) no importen nada relacionado con infraestructura.
2. **Aislar la Lógica de Negocio:** Asegurar que los casos de uso (`internal/service`) dependan exclusivamente de interfaces (`internal/ports`) y no de implementaciones (`internal/adapter` o `internal/port_in`).
3. **Revisar las Entradas y Salidas:** Confirmar que el código de infraestructura (SQLite, HTTP, Ollama, MCP) se mantiene confinado en la capa más externa (`internal/adapter` y `internal/port_in`).
4. **Verificar el Ensamblaje:** Comprobar que `cmd/kandel` sea el único lugar donde se acoplan las implementaciones concretas.

---

## 📋 Pasos de Ejecución

### 1. Verificación del Centro del Hexágono (`internal/domain`)
*   **Acción:** Extraer y revisar todos los `import` de los archivos `.go` dentro de `internal/domain`.
*   **Criterio de éxito:** No debe existir absolutamente ninguna importación hacia `internal/adapter`, `internal/service`, `internal/port_in` ni a librerías de terceros (SQL, HTTP, MCP). El dominio solo puede depender de la biblioteca estándar de Go.

### 2. Verificación de los Contratos (`internal/ports`)
*   **Acción:** Analizar las interfaces definidas en `internal/ports`.
*   **Criterio de éxito:** Los puertos (interfaces) solo deben conocer los tipos definidos en `domain` o tipos genéricos de Go. No deben contener anotaciones json, referencias a esquemas de base de datos ni depender de utilidades de infraestructura.

### 3. Verificación de la Lógica de Negocio (`internal/service`)
*   **Acción:** Buscar en los archivos de `internal/service` cualquier mención a carpetas prohibidas.
*   **Criterio de éxito:** Los servicios deben implementar su lógica apoyándose en `domain` y en los contratos de `ports`. Está **prohibido** que un servicio importe `internal/adapter` o `internal/port_in`.

### 4. Verificación de la Infraestructura Externa (`internal/adapter` y `internal/port_in`)
*   **Acción:** Listar las dependencias de estas carpetas.
*   **Criterio de éxito:**
    *   `internal/adapter` (SQLite, Ollama) puede importar `domain` y `ports` (para cumplir las interfaces), pero su lógica de base de datos no debe filtrarse hacia afuera.
    *   `internal/port_in` (MCP, HTTP, CLI, Hooks) es la capa de presentación. Consume `service` pero no debe saltarse esta capa para acceder directamente a la base de datos de los adaptadores.

### 5. Verificación de Ensamblaje (Wiring en `cmd/kandel`)
*   **Acción:** Revisar `cmd/kandel/main.go` o archivos de configuración.
*   **Criterio de éxito:** Este debe ser el único lugar donde se importa todo a la vez. Aquí es donde se instancian los adaptadores de SQLite y Ollama y se le inyectan a la capa `service`, para luego arrancar los servidores (HTTP, MCP).

### 6. Validación de Análisis Estático
*   **Acción:** Ejecutar `go vet ./...` y verificar que no existan ciclos de importación ni violaciones de los chequeos estándar de Go en las capas internas.

---

## 📈 Entregable Esperado
Un documento consolidado en `docs/reporte_auditoria_arquitectura.md` que detalle:
1. Las posibles infracciones detectadas en los flujos de importación.
2. Recomendaciones de refactorización para cada caso donde se haya roto el aislamiento.
3. El resultado final del chequeo estático y la salud de la arquitectura.
