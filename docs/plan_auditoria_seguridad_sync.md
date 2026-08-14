# Plan de Auditoría: Seguridad y Sincronización

Este documento detalla el plan para ejecutar la auditoría de riesgos transversales (Punto 3). Dado que Kandel es una aplicación local pero cuenta con un componente distribuido (el *Hub* de sincronización), existen riesgos inherentes a la comunicación por red, la autenticación y la resolución de conflictos distribuidos.

La investigación externa resalta dos grandes familias de vulnerabilidades para este escenario:
1. **Ataques a la API de Go (Tokens):** Fugas por *Timing Attacks* al comparar strings estáticos y transmisión insegura.
2. **Pérdida de datos por LWW + Schema Drift:** Cuando máquinas con diferentes versiones del binario sincronizan, la que tiene el código más antiguo tiende a descartar los campos nuevos. El mecanismo "Last-Write-Wins" (LWW) puede hacer que esa versión vieja sobreescriba y destruya de forma permanente los campos de las máquinas actualizadas.

---

## 🎯 Objetivos de la Auditoría
1. **Criptografía del Token:** Garantizar que el `sync_token` sea invulnerable a ataques de tiempo y que no se filtre en logs.
2. **Sanitización del Payload (Anti-RCE):** Verificar que el protocolo prohíba estrictamente la ejecución de código remoto encubierto dentro de las memorias.
3. **Resiliencia ante Schema Drift:** Evaluar cómo el sistema maneja la desincronización de esquemas entre nodos mixtos sin perder datos.
4. **Transporte Seguro:** Confirmar que la comunicación con el Hub exige o promueve fuertemente el cifrado (HTTPS).

---

## 📋 Pasos de Ejecución

### 1. Auditoría de Comparación de Tokens (Timing Attacks)
*   **Acción:** Inspeccionar el código del Hub (`internal/hub`) donde se valida el `sync_token` entrante.
*   **Criterio de éxito:** La validación **no debe** usar el operador estándar `==`. Debe utilizar la función criptográfica `crypto/subtle.ConstantTimeCompare` para evitar que un atacante descubra el token midiendo los tiempos de respuesta del servidor (ataque de canal lateral).

### 2. Auditoría del Payload y Riesgo de RCE (Ejecución de Código Remoto)
*   **Acción:** Revisar la estructura `Memory` en `internal/domain/memory.go` y su serialización.
*   **Criterio de éxito:**
    *   Confirmar que la idea rechazada en el plan de mejoras original (guardar un comando bash dentro de una memoria) **no fue implementada**.
    *   Si se implementó el campo `verifies_path`, verificar que solo sea una ruta de archivo evaluada de forma pasiva (`os.Stat`), y que nunca sea pasada a un `os/exec` o una terminal (`Bash`).

### 3. Auditoría de LWW y Deriva de Esquema (Schema Drift)
*   **Acción:** Analizar cómo los adaptadores deserializan un JSON entrante en `internal/service/sync.go` o `internal/hub`.
*   **Criterio de éxito:**
    *   Revisar si los campos JSON que la versión local no conoce (porque provienen de una máquina con una versión de Kandel más nueva) se descartan silenciosamente. 
    *   Si se descartan, confirmar si existe el mecanismo de protección planeado: que `kandel doctor` o la sincronización aborten/avisen si detectan una versión incompatible, o si se empuja a un campo "bolsa" genérico.

### 4. Auditoría de Transporte (HTTPS)
*   **Acción:** Buscar en `internal/adapter/httpsync` validaciones de URL.
*   **Criterio de éxito:** Si la URL del hub arranca con `http://` en lugar de `https://`, el sistema debería emitir una advertencia crítica o negarse a transmitir el `sync_token`, salvo que sea explícitamente `localhost` o `127.0.0.1`.

---

## 📈 Entregable Esperado
Una vez ejecutado, generaremos el documento `docs/reporte_auditoria_seguridad.md` que incluirá:
1. El veredicto sobre la vulnerabilidad a Timing Attacks.
2. Confirmación de la superficie de ataque del Payload.
3. Propuesta de mitigación técnica si descubrimos que el *Schema Drift* destruye datos actualmente.
