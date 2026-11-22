# Reporte de Auditoría: Seguridad y Sincronización

**Fecha:** 2026-08-14  
**Actualización (remediación):** 2026-11-21  
**Estado:** Finalizado — 2 hallazgos críticos **remediados** (2026-11-21)

Este documento recoge los resultados de la auditoría de seguridad sobre el protocolo de sincronización y los mecanismos de protección de datos distribuidos en Kandel.

---

## 1. Ataques de Tiempo (Timing Attacks)
**Estado: 🟢 Superado**
* **Hallazgo:** El código del Hub (`internal/hub/hub.go`) implementa correctamente la función `crypto/subtle.ConstantTimeCompare` al validar el `sync_token`. 
* **Impacto:** Esto hace matemáticamente imposible que un atacante adivine el token midiendo el tiempo de respuesta del servidor frente a intentos fallidos.

## 2. Inyección de Payload y RCE
**Estado: 🟢 Superado**
* **Hallazgo:** Se inspeccionó el modelo central de memoria (`internal/domain/memory.go`) y el servicio de sincronización. Las ideas previas de agregar un campo `verifies_path` o comandos ejecutables dentro de la memoria **no fueron implementadas**. 
* **Impacto:** Al ser campos puramente descriptivos (título, contenido, etc.) que se serializan de forma segura, la superficie de ataque de ejecución de código remoto (RCE) a través del motor de sincronización es nula.

---

## 3. Pérdida de Datos por LWW + Schema Drift
**Estado: 🔴 CRÍTICO (Vulnerabilidad Detectada)**
* **Hallazgo:** Cuando el cliente HTTP (`internal/adapter/httpsync.go`) recibe el JSON del Hub, la lógica interna deserializa el payload (`json.Unmarshal`) hacia la estructura rígida de Go `domain.Memory`. Al usar la librería estándar de Go, **cualquier campo nuevo que el binario viejo no reconozca es descartado silenciosamente**.
* **Escenario de desastre:** 
  1. La máquina A (Kandel v1.2) sincroniza una memoria con un campo nuevo.
  2. La máquina B (Kandel v1.0) lee la memoria. El campo nuevo se borra de la RAM local.
  3. La máquina B edita la memoria y la sincroniza al Hub.
  4. Por la política *Last-Write-Wins*, el Hub acepta la versión de la máquina B (porque tiene un *timestamp* más reciente) y **sobrescribe la versión de la máquina A, destruyendo el campo nuevo permanentemente para toda la red**.
* **Recomendación:** Agregar un campo `UnknownFields map[string]any` a `domain.Memory` o rechazar explícitamente memorias con campos no mapeados.

## 4. Transporte Inseguro (Fuga del Sync Token)
**Estado: 🔴 CRÍTICO (Vulnerabilidad Detectada)**
* **Hallazgo:** No existe ninguna validación en el código (`httpsync.go` ni `config.go`) que fuerce el uso estricto de cifrado de red (TLS/HTTPS).
* **Escenario de desastre:** Si un usuario configura accidentalmente `sync_hub_url = http://midominio.com:8766`, Kandel enviará el `sync_token` (un secreto de alta entropía con permisos absolutos de lectura/escritura) **en texto plano** a través de la red de internet, dejándolo expuesto a ataques de intermediario (Man-In-The-Middle).
* **Recomendación:** Modificar el constructor de `httpsync` para que devuelva un error si la URL comienza con `http://` y no apunta a `localhost` o `127.0.0.1`.

---
**Conclusión:** La criptografía básica está bien implementada, pero existen dos riesgos severos de pérdida de datos y fuga de tokens que deben parchearse antes de un despliegue en producción con sincronización remota.

---

## 5. Estado de remediación (2026-11-21)

Ambos hallazgos críticos quedaron cerrados:

1. **Pérdida de Datos por LWW + Schema Drift (🟢 Remediado):**
   - `domain.Memory.UnknownFields` (`map[string]string`) preserva claves JSON desconocidas recibidas desde versiones más nuevas de Kandel sin contaminar el dominio con etiquetas `json`/`db`.
   - `internal/service/sync_codec.go` (`unmarshalMemoryPayload` / `marshalMemoryPayload`) extrae claves desconocidas en `Pull` / `ResolveConflict` y las reinyecta en `Push` / `MemoryService.enqueue` sin pisar los campos conocidos.
   - `internal/adapter/sqlite/migrate.go` (`schemaV10`) añade la columna `unknown_fields` en `memories`, persistida y recuperada en `MemoryRepo` / `scanner.go`.
   - `PullResult.SchemaDrift` reporta el número de memorias con deriva de esquema en `kandel sync` y `mem_sync`.

2. **Transporte Inseguro / Fuga del Sync Token (🟢 Remediado):**
   - `httpsync.ValidateHubURL` y `httpsync.Client.Push`/`Pull` rechazan cualquier URL que no sea `https://`, permitiendo `http://` únicamente en loopback (`localhost`, `127.0.0.1`, `::1`), antes de construir o enviar la petición con `Authorization: Bearer`.
   - `config.ValidateSyncURL` aplica la misma validación al configurar `kandel config set sync_url ...`, al correr `kandel sync` y al ejecutar `mem_sync`.
