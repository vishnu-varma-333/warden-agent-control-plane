-- DB-level write prevention (the spec's production-readiness requirement,
-- distinct from the hash chain's own job): the chain DETECTS tampering
-- after the fact (internal/audit.Verify); this trigger PREVENTS it in the
-- first place, at the database layer, independent of and beneath the Go
-- application entirely — even a compromised or buggy Warden process
-- connected with full privileges cannot UPDATE or DELETE a row here, only
-- INSERT. A trigger, not a REVOKE/GRANT change: REVOKE doesn't bind a
-- Postgres superuser (and this project's single "warden" role, created via
-- POSTGRES_USER, is one), but a trigger fires for every role, superuser
-- included, since it isn't a permission check at all — it's part of the
-- table's own defined behavior.
CREATE OR REPLACE FUNCTION audit_events_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_events is append-only: % is not permitted (seq=%)', TG_OP, OLD.seq;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_events_no_update
    BEFORE UPDATE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_immutable();

CREATE TRIGGER audit_events_no_delete
    BEFORE DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_immutable();
