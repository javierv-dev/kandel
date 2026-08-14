# Plan de Auditoría: Utilidad y Relevancia (Fase 1)

Este documento detalla el plan de acción para ejecutar la **Auditoría de Utilidad y Relevancia** del punto 1 de nuestras propuestas. Aprovecharemos las herramientas y comandos de telemetría introducidos en la Fase 1 de Kandel para evaluar la calidad del contexto que se está inyectando.

---

## 🎯 Objetivos de la Auditoría
1. **Medir el "ruido":** Confirmar si Kandel está inyectando demasiadas memorias inútiles o irrelevantes.
2. **Calibrar parámetros:** Asegurar que los valores actuales del umbral (`0.45`) y el límite (`3`) sean correctos.
3. **Validar los filtros:** Comprobar que no se reinyecta el contexto de la misma sesión a menos que haya ocurrido una compactación.
4. **Higiene:** Detectar y purgar las "memorias durmientes".

---

## 📋 Pasos de Ejecución

### 1. Verificación del entorno base
*   **Comprobar la versión instalada:** Ejecutar `kandel status` y verificar que el daemon en ejecución sea compatible con la Fase 1.
*   **Revisión de configuración:** Validar los valores vigentes con `kandel config get relevance_threshold` (debe ser 0.45) y `kandel config get relevance_limit` (debe ser 3).

### 2. Recolección de Métricas Globales (Tasa de Acierto)
*   **Comando a ejecutar:** `kandel usage`
*   **Acción:** Analizar la lista completa de inyecciones contra el total de menciones del agente.
*   **Criterio de éxito:** Si la proporción de "veces mencionada" sobre "veces inyectada" es sistemáticamente baja (ej. < 10%), significa que el límite de inyección está saturando el prompt con irrelevancias o que el umbral es demasiado permisivo.

### 3. Identificación y Poda de Memorias Durmientes
*   **Comandos a ejecutar:** `kandel doctor` y `kandel usage --dormant`
*   **Acción:** Aislar las memorias inyectadas **5 veces o más** que registren **0 menciones**.
*   **Resolución:** 
    *   Revisar manualmente por qué fallan (¿Están mal redactadas? ¿Tienen un título difuso?).
    *   Utilizar `mem_update` para mejorar su legibilidad o `mem_delete` para eliminarlas si ya caducaron (poda).

### 4. Prueba del Umbral Límite (Edge Cases)
*   **Acción:** Revisar manualmente el archivo local de la base de datos de kandel o consultar los logs de inyección para encontrar las últimas 10 memorias inyectadas que tuvieron un score entre **0.45 y 0.50** (muy cerca del umbral).
*   **Análisis:** Contrastar si el agente *realmente* utilizó esas memorias. Si se concluye que todas las inyecciones debajo de 0.52 siguen siendo "basura" (como sospechaban inicialmente en el proyecto), se propondrá subir el umbral.

### 5. Validación del Filtro de Sesión Activa
*   **Prueba práctica:** 
    1. Arrancar una sesión fresca del agente.
    2. Usar `mem_save` para grabar una preferencia aleatoria temporal ("Usa comillas simples siempre").
    3. Hacer una pregunta en el siguiente prompt y comprobar que kandel **no** inyecta esa memoria (el filtro de sesión la bloquea).
    4. Forzar un contexto extenso (ej. enviar textos grandes) para provocar una compactación de sesión.
    5. Volver a preguntar. Esta vez, kandel **debe inyectar** la memoria para restablecer el contexto.

---

## 📈 Entregable Esperado
Una vez completados los 5 pasos, se deberá generar un reporte en `docs/reporte_auditoria_utilidad.md` con:
1. Resumen de las métricas de uso actuales.
2. Listado de memorias "durmientes" que fueron podadas/reemplazadas.
3. Decisión definitiva sobre si ajustar o mantener el umbral de `0.45` y el límite de `3`.
4. El resultado del test del filtro de compactación.
