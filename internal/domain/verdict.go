package domain

// Verdict es el dictamen del juez LLM al comparar dos memorias: qué relación semántica hay
// entre ellas, con cuánta confianza y por qué. Es lo que devuelve el AgentRunner y lo que
// la capa de servicio convierte en un juicio sobre una MemoryRelation.
type Verdict struct {
	Relation   RelationType // veredicto: conflicts_with | supersedes | scoped | related | compatible | not_conflict
	Confidence float64      // confianza autoinformada por el modelo, en [0..1]
	Reasoning  string       // explicación breve del modelo
	Model      string       // identificador del modelo que emitió el veredicto (atribución)
}
