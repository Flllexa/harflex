-- A conversation may follow its profile after an edit (same model, or the profile's when the provider dropped it),
-- and only with a new catalog check: what the choice is bound to (the session, its backend, its folder and
-- executable) never changes, and nothing is rewritten without a fresh checked_at.
DROP TRIGGER session_model_selection_immutable;
CREATE TRIGGER session_model_selection_immutable BEFORE UPDATE ON session_model_selections
WHEN NEW.session_id IS NOT OLD.session_id OR NEW.backend_id IS NOT OLD.backend_id OR NEW.workspace_path IS NOT OLD.workspace_path
  OR NEW.executable_path IS NOT OLD.executable_path OR NEW.executable_version IS NOT OLD.executable_version
  OR NEW.checked_at IS OLD.checked_at
BEGIN SELECT RAISE(ABORT,'immutable session model selection'); END;
