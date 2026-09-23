-- Atendente humana: até qual mensagem de entrada a conversa já foi respondida (debounce em lote).
ALTER TABLE conversations ADD COLUMN replied_through bigint NOT NULL DEFAULT 0;
