# Simulación de coste: una tarea, dos agentes

Presupuesto ilustrativo del MVP local, no benchmark ni predicción de cuota. Base: Kandel **actual** con bitácora y contexto, misma tarea, mismo cambio de agente y trabajo de programación en ambas ramas del experimento. No imputamos al relevo el coste de abrir Antigravity ni le acreditamos tiempo ahorrado en investigación. Estimamos tokens visibles de entrada/salida por solicitud al modelo principal, sin descuentos de caché, compactación, reintentos ni tokens internos de razonamiento. Los tamaños son supuestos redondos, no un conteo con un tokenizador real.

## Guion

Petición: «Añade `GET /orders/:id`; exige autenticación, responde 404 si no existe y añade pruebas».

| Solicitudes | Claude Code | Antigravity |
|---|---|---|
| C1–C2 | Lee rutas y middleware. Usaría el middleware existente. Guarda el primer hito en la operación de bitácora ya prevista. | — |
| C3–C4 | Implementa el handler y la prueba de 404. Segundo hito: falta verificar autenticación. | — |
| C5–C6 | Edita el test de autenticación, lo ejecuta y falla. Se agota la cuota antes de guardar otro hito. | — |
| A1–A2 | — | Usuario: «Continúa y revisa lo de Claude». Lee el último estado y contrasta el diff. Repite el test: solo ahora puede afirmar que falla en su copia. |
| A3–A4 | — | Corrige el montaje del middleware, ejecuta la suite y entrega el resultado. |

## Cuenta reproducible

En la base, entradas de C1–C6: 6, 7, 8, 9, 10 y 11 mil tokens. Entradas de A1–A4: 5, 6, 7 y 8 mil. Salida media: 300 tokens por solicitud. Total: **77.000 entrada + 3.000 salida = 80.000**, repartidos en 10 solicitudes. No son diez mensajes humanos: incluyen rondas para usar herramientas.

Opción integrada: los dos checkpoints sustituyen escrituras ya previstas; añadimos **100 tokens de salida netos** a cada una y 20 de confirmación extra. Suponemos historial retenido hasta el cambio de agente: ese par (100 + 20) vuelve como entrada cuatro veces tras C2 y dos tras C4. Una cabecera/instrucción de 30 tokens extra está presente en las diez entradas (se carga una vez por sesión). Antigravity recibe una sola vez un paquete de **350 tokens netos más que el contexto base**; permanece en sus cuatro entradas. La integración lleva el paquete en una ronda ya existente, no en una consulta MCP dedicada. Sin esquemas adicionales grandes ni transcriptos; si se cargan, deben sumarse. La respuesta final no guarda un tercer checkpoint en esta simulación.

| Diseño | Entrada | Salida | Total | Solicitudes al modelo | Diferencia total |
|---|---:|---:|---:|---:|---:|
| Base actual | 77.000 | 3.000 | 80.000 | 10 | — |
| Checkpoints integrados | 79.420 | 3.200 | 82.620 | 10 | +2.620 (+3,3 %) |
| Dos rondas adicionales | 94.420 | 3.400 | 97.820 | 12 | +17.820 (+22,3 %) |

Primera diferencia: entrada = `30×10 + 120×(4+2) + 350×4 = 2.420`; salida = `100×2 = 200`. En la tercera fila añadimos al caso integrado **dos presupuestos marginales** de 8.000 y 7.000 tokens de entrada y 100 de salida cada uno (incluida la exposición posterior de sus mensajes dentro del presupuesto marginal). Modela un cliente que necesita rondas dedicadas para guardar o recuperar; no es un máximo. En ese caso también suben las solicitudes un 20 %. Un paquete de 3.500 tokens en lugar de 350 añadiría por sí solo otros `3.150×4 = 12.600` tokens de entrada al caso integrado.

## Qué consume y cómo medirlo

SQLite, identidad, cálculo de hash, lectura de Git y hooks locales no consumen tokens de la API del agente. Sus resultados sí cuestan entrada si llegan al contexto. Una llamada MCP no equivale a una llamada al modelo: depende de si exige una ronda adicional de decisión/continuación. Los embeddings de Ollama son cómputo separado. Un resumidor o juez LLM adicional **sí** tiene sus propias solicitudes: no se usa en la simulación. La caché puede abaratar entradas repetidas sin que desaparezcan del contexto. Tokens acumulados por solicitudes no equivalen al tamaño de la ventana ni a una cuota de suscripción; el precio depende del proveedor, modelo, tarifas de entrada/salida y caché.

Validación en E0/E3: reproducir este guion con y sin relevo en copias del mismo repo, una vez en cada sentido y con varias repeticiones. Registrar por agente/modelo las solicitudes, entradas no cacheadas, lecturas/escrituras de caché, salidas, herramientas, latencia y coste según uso reportado; marcar «no disponible» donde el cliente no exponga telemetría. Separar casos con y sin relevo, más un control con esquemas completos o transcripción larga. Medir también si el receptor identifica la tarea y el último estado verificable: ahorrar tokens ocultando una laguna no es éxito. No registrar texto privado para obtener estos contadores.
