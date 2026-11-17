BEGIN;
CREATE OR REPLACE FUNCTION log_parameter_change() RETURNS TRIGGER AS $$
BEGIN
    IF (TG_OP = 'UPDATE') THEN
        INSERT INTO parameter_history (key, scope_type, scope_id, old_value, new_value, changed_by)
        VALUES (OLD.key, OLD.scope_type, OLD.scope_id, OLD.value, NEW.value, NEW.updated_by);
    ELSIF (TG_OP = 'DELETE') THEN
        INSERT INTO parameter_history (key, scope_type, scope_id, old_value, new_value, changed_by)
        VALUES (OLD.key, OLD.scope_type, OLD.scope_id, OLD.value, '<deleted>', current_user);
    ELSIF (TG_OP = 'INSERT') THEN
        INSERT INTO parameter_history (key, scope_type, scope_id, old_value, new_value, changed_by)
        VALUES (NEW.key, NEW.scope_type, NEW.scope_id, NULL, NEW.value, NEW.updated_by);
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER parameter_values_history_trigger
    AFTER INSERT OR UPDATE OR DELETE ON parameter_values
    FOR EACH ROW EXECUTE FUNCTION log_parameter_change();
COMMIT;
