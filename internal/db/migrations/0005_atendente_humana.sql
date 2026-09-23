-- Atendente humana: marca de mensagens já respondidas (debounce em lote) e uso do TTS.
ALTER TABLE conversations ADD COLUMN replied_through bigint NOT NULL DEFAULT 0;
-- Conversas que já existem começam com tudo respondido: o primeiro lote não junta mensagens antigas.
UPDATE conversations c SET replied_through = m.last_in
FROM (SELECT conversation_id, max(id) AS last_in FROM messages WHERE direction = 'in' GROUP BY conversation_id) m
WHERE m.conversation_id = c.id;

-- Uso do TTS (respostas em áudio) entra na conta de custo.
ALTER TABLE llm_usage DROP CONSTRAINT llm_usage_agent_check;
ALTER TABLE llm_usage ADD CONSTRAINT llm_usage_agent_check
  CHECK (agent IN ('attendant', 'manager', 'transcriber', 'tts'));
