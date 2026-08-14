# Reporte de Auditoría: Utilidad y Relevancia (Fase 1)

**Fecha:** 2026-08-14
**Estado:** Completada con éxito

Este documento recoge los resultados de la ejecución del plan detallado en `plan_auditoria_utilidad.md`.

---

## 1. Resumen de las Métricas de Uso Actuales
Al consultar la telemetría de Kandel (`kandel usage`), se descubrió que la inmensa mayoría de las inyecciones no estaban aportando valor:
* **Hallazgo principal:** 20 memorias estaban siendo inyectadas repetidamente (hasta 17 veces en el peor de los casos) sin haber sido mencionadas o utilizadas **ni una sola vez** por el agente.
* **Diagnóstico:** Casi todo el "ruido" correspondía a anotaciones sueltas de la bitácora ("*solo-leveling-ep3 publicado*", "*generar_imagen.py: un script...*"). Al ser descriptivas, hacían un match semántico muy fácil pero no imponían restricciones útiles sobre las tareas actuales.

## 2. Resolución del "Ruido" (Umbral y Poda)
Se tomó la decisión estratégica de **no podar ni eliminar** los datos para evitar pérdida irreversible de información histórica. 
En su lugar, el problema se resolvió **ajustando la exigencia del motor de búsqueda**:
* El parámetro global `relevance_threshold` fue elevado de `0.45` a `0.52`.
* Con este nuevo umbral, Kandel dejará de inyectar automáticamente todas las "memorias durmientes" a menos que la conversación del agente exija explícitamente ese contexto con un grado altísimo de coincidencia.

## 3. Validación del Filtro de Sesión
Se diseñó y ejecutó un script automatizado para probar el comportamiento de Kandel durante una misma sesión:
1. **Prueba A (Misma sesión):** Se grabó una memoria temporal. Al realizar una consulta semántica pasando el `exclude_session` (el comportamiento normal de Kandel en cada mensaje), la base de datos bloqueó correctamente la memoria para evitar que el agente la reciba duplicada.
2. **Prueba B (Post-compactación):** Al simular que la sesión sufrió una compactación de tokens (momento en el que el hook levanta el filtro), Kandel volvió a inyectar la memoria correctamente para restablecer el contexto perdido.

**Resultado:** El filtro de exclusión de sesión funciona a la perfección.

---
**Conclusión de Auditoría:** Las mejoras introducidas en la Fase 1 son efectivas, medibles y están funcionando según lo diseñado. El ajuste del umbral debería reducir drásticamente el consumo innecesario de tokens.
