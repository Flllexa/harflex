ALTER TABLE session_model_selections
 ADD COLUMN supported_reasoning_efforts TEXT NOT NULL DEFAULT 'null'
 CHECK(length(supported_reasoning_efforts)<=4096 AND json_valid(supported_reasoning_efforts));

CREATE TRIGGER session_model_selection_immutable BEFORE UPDATE ON session_model_selections
BEGIN SELECT RAISE(ABORT,'immutable session model selection'); END;
