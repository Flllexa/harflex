UPDATE session_delegations
SET task_prompt = 'Prévia indisponível: tarefa legada com caractere de controle.'
WHERE instr(CAST(task_prompt AS BLOB), X'00') > 0;

UPDATE session_delegations
SET task_prompt = substr(task_prompt, 1, 280) || '…'
WHERE length(task_prompt) > 280;
