-- Atendente humana: marca de mensagens já respondidas (debounce em lote) e uso do TTS.
ALTER TABLE conversations ADD COLUMN replied_through bigint NOT NULL DEFAULT 0;

-- Uso do TTS (respostas em áudio) entra na conta de custo.
ALTER TABLE llm_usage DROP CONSTRAINT llm_usage_agent_check;
ALTER TABLE llm_usage ADD CONSTRAINT llm_usage_agent_check
  CHECK (agent IN ('attendant', 'manager', 'transcriber', 'tts'));
