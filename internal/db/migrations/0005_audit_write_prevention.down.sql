DROP TRIGGER IF EXISTS audit_events_no_update ON audit_events;
DROP TRIGGER IF EXISTS audit_events_no_delete ON audit_events;
DROP FUNCTION IF EXISTS audit_events_immutable();
