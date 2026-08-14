# Plan de Auditoría: Arquitectura Hexagonal

Este documento detalla el plan de acción para ejecutar la **Auditoría Arquitectónica** del punto 2 de nuestras propuestas. Dado que Kandel es estricto con su arquitectura (según indica el `README.md`), el objetivo es asegurar que la "Regla de Dependencia" apunte siempre hacia el centro del hexágono y evitar antipatrones comunes de Go.

---

## 🎯 Objetivos de la Auditoría
1. **Proteger el Dominio:** Garantizar que las entidades puras (`internal/domain`) no importen nada relacionado con infraestructura.
2. **Aislar la Lógica de Negocio:** Asegurar que los casos de uso (`internal/service`) dependan exclusivamente de interfaces (`internal/ports`) y no de implementaciones.
3. **Revisar las Entradas y Salidas:** Confirmar que el código de infraestructura se mantiene confinado en la capa más externa (`internal/adapter` y `internal/port_in`).
4. **Verificar el Ensamblaje:** Comprobar que `cmd/kandel` sea el único lugar donde se acoplan las implementaciones concretas.
5. **Prevenir Antipatrones Idiomáticos de Go:** Evitar la "contaminación de modelos", el sobre-ingeniería innecesaria y el manejo pobre de errores en las fronteras.

---

## 📋 Pasos de Ejecución

### 1. Verificación del Centro del Hexágono (`internal/domain`)
*   **Acción:** Extraer y revisar todos los `import` y la definición de estructuras (`structs`) dentro de `internal/domain`.
*   **Antipatrones a buscar:**
    *   **Fugas de importación:** Dependencias a `internal/adapter`, `internal/service`, `internal/port_in` o librerías de terceros (SQL, HTTP, MCP).
    *   **Contaminación de Modelos (Model Pollution):** Revisar si las estructuras del dominio tienen etiquetas (`tags`) de infraestructura como `` `db:"..."` `` o `` `json:"..."` ``. El dominio no debería saber cómo se guarda en SQLite ni cómo se serializa en la red.
    *   **Acoplamiento de Errores:** Verificar que el dominio devuelva errores propios y no importe `database/sql` para devolver un `sql.ErrNoRows`.

### 2. Verificación de los Contratos (`internal/ports`)
*   **Acción:** Analizar las interfaces definidas en `internal/ports`.
*   **Antipatrones a buscar:**
    *   **Interfaces monolíticas ("Java-in-Go"):** En Go, las interfaces deben ser pequeñas y definir comportamientos, no reflejar objetos gigantes.
    *   Dependencia de tipos que no pertenezcan al dominio o a la biblioteca estándar.

### 3. Verificación de la Lógica de Negocio (`internal/service`)
*   **Acción:** Buscar en los archivos de `internal/service` cómo se orquesta la lógica.
*   **Antipatrones a buscar:**
    *   Los servicios importando implementaciones directas de `internal/adapter`.
    *   Descarte de errores: Comprobar que los errores devueltos por los adaptadores no sean silenciados, sino enriquecidos con contexto para que el dominio entienda qué falló sin conocer la tecnología subyacente.

### 4. Verificación de la Infraestructura Externa (`internal/adapter` y `internal/port_in`)
*   **Acción:** Revisar la implementación de SQLite, HTTP, MCP, etc.
*   **Antipatrones a buscar:**
    *   **Lógica de negocio en el adaptador (Non-Cohesive Adapters):** Los adaptadores deben ser "tontos" y delgados. Su única función es traducir datos del mundo exterior al formato del dominio. Si hay validaciones de negocio en un adaptador SQLite, es una fuga.
    *   Saltarse la capa `service` y escribir/leer directamente de la base de datos desde los handlers HTTP (`internal/port_in`).

### 5. Verificación de Ensamblaje (Wiring en `cmd/kandel`)
*   **Acción:** Revisar `cmd/kandel/main.go`.
*   **Criterio de éxito:** Este debe ser el único lugar donde se importa todo a la vez para realizar la inyección de dependencias estática.

### 6. Validación de Análisis Estático
*   **Acción:** Ejecutar `go vet ./...` y verificar que el linter no reporte ciclos de importación ni violaciones estructurales.

---

## 📈 Entregable Esperado
Un documento consolidado en `docs/reporte_auditoria_arquitectura.md` que detalle:
1. Las posibles infracciones detectadas en los flujos de importación y etiquetas de estructuras.
2. Recomendaciones de refactorización para los antipatrones encontrados (ej. separación de DTOs vs Entidades de dominio).
3. El resultado final del chequeo estático y la salud de la arquitectura.
