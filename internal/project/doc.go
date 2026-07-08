// Package project resuelve a qué proyecto pertenece un directorio: es la única fuente de verdad de
// detección de proyecto en kandel (remoto git origin > raíz git > basename). El hook y el CLI la
// comparten para derivar siempre el mismo nombre; también expone la regla canónica de normalización.
package project
