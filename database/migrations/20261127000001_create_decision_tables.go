package migrations

import (
	"github.com/lathief/RiskForge/app/facades"
)

type M20261127000001CreateDecisionTables struct{}

// Signature The unique signature for the migration.
func (r *M20261127000001CreateDecisionTables) Signature() string {
	return "20261127000001_create_decision_tables"
}

// Up Run the migrations. Durable decisions and idempotency (SYSTEM_DESIGN.md 7, 13.5, RF-015).
//
//   - decision.requests: one row per (client, idempotency key) with the hash of the request.
//     The unique key is what makes two concurrent requests with one key produce one run: the
//     second insert waits for the first transaction and then finds the row.
//   - decision.runs: the stored decision. It names the policy version and checksum it was made
//     with and keeps the normalized input and the full outcome, so it can be shown and
//     replayed without recomputation.
//   - decision.rule_traces: one row per evaluated RAC rule, for queries.
//
// All three tables are append-only: a trigger refuses UPDATE and DELETE, as for the audit
// trail the design asks for. Only a completed decision is stored; a request that fails rolls
// back with its key, so the key stays usable.
func (r *M20261127000001CreateDecisionTables) Up() error {
	for _, stmt := range []string{
		`CREATE TABLE decision.requests (
			id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			client_id       UUID NOT NULL REFERENCES identity.users (id),
			idempotency_key TEXT NOT NULL,
			request_hash    TEXT NOT NULL,
			created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
			CONSTRAINT decision_requests_client_key UNIQUE (client_id, idempotency_key),
			CONSTRAINT decision_requests_key_format CHECK (idempotency_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'),
			CONSTRAINT decision_requests_hash_format CHECK (request_hash ~ '^[0-9a-f]{64}$')
		)`,
		`CREATE TABLE decision.runs (
			id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			request_id        UUID NOT NULL UNIQUE REFERENCES decision.requests (id),
			client_id         UUID NOT NULL REFERENCES identity.users (id),
			application_id    TEXT NOT NULL,
			product_code      TEXT NOT NULL,
			policy_version_id UUID NOT NULL REFERENCES policy.policy_versions (id),
			policy_checksum   TEXT NOT NULL,
			input_snapshot    JSONB NOT NULL,
			status            TEXT NOT NULL DEFAULT 'COMPLETED',
			final_decision    TEXT NOT NULL,
			risk_grade        TEXT,
			total_score       NUMERIC(12,2),
			reason_codes      JSONB NOT NULL,
			outcome           JSONB NOT NULL,
			created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
			CONSTRAINT decision_runs_status CHECK (status = 'COMPLETED'),
			CONSTRAINT decision_runs_decision CHECK (final_decision IN ('APPROVE', 'REFER', 'REJECT')),
			CONSTRAINT decision_runs_checksum_format CHECK (policy_checksum ~ '^[0-9a-f]{64}$'),
			CONSTRAINT decision_runs_input_is_object CHECK (jsonb_typeof(input_snapshot) = 'object'),
			CONSTRAINT decision_runs_reasons_is_array CHECK (jsonb_typeof(reason_codes) = 'array'),
			CONSTRAINT decision_runs_outcome_is_object CHECK (jsonb_typeof(outcome) = 'object'),
			CONSTRAINT decision_runs_scored_together CHECK ((risk_grade IS NULL) = (total_score IS NULL))
		)`,
		`CREATE INDEX decision_runs_application_idx ON decision.runs (application_id, created_at DESC)`,
		`CREATE INDEX decision_runs_policy_idx ON decision.runs (policy_version_id, created_at DESC)`,
		`CREATE INDEX decision_runs_client_idx ON decision.runs (client_id, created_at DESC)`,
		`CREATE TABLE decision.rule_traces (
			id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			run_id      UUID NOT NULL REFERENCES decision.runs (id),
			trace_order INTEGER NOT NULL,
			rule_code   TEXT NOT NULL,
			reason_code TEXT NOT NULL,
			action      TEXT NOT NULL,
			expression  TEXT NOT NULL,
			passed      BOOLEAN NOT NULL,
			inputs      JSONB NOT NULL,
			CONSTRAINT decision_rule_traces_order_key UNIQUE (run_id, trace_order),
			CONSTRAINT decision_rule_traces_order_positive CHECK (trace_order >= 1),
			CONSTRAINT decision_rule_traces_action CHECK (action IN ('REJECT', 'REFER')),
			CONSTRAINT decision_rule_traces_inputs_is_array CHECK (jsonb_typeof(inputs) = 'array')
		)`,
		// A run must carry the checksum of the version it names: the stored decision can then
		// always be tied to the exact frozen snapshot that made it.
		`CREATE FUNCTION decision.guard_run_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		DECLARE
			v_checksum TEXT;
			v_status   TEXT;
		BEGIN
			SELECT checksum, status INTO v_checksum, v_status FROM policy.policy_versions WHERE id = NEW.policy_version_id;
			IF v_checksum IS DISTINCT FROM NEW.policy_checksum THEN
				RAISE EXCEPTION 'the run must carry the checksum of the policy version it was made with'
					USING ERRCODE = 'integrity_constraint_violation';
			END IF;
			IF v_status IS DISTINCT FROM 'ACTIVE' AND v_status IS DISTINCT FROM 'ARCHIVED' THEN
				RAISE EXCEPTION 'a decision can only be made with a policy version that is or was ACTIVE (got %)', v_status
					USING ERRCODE = 'integrity_constraint_violation';
			END IF;
			RETURN NEW;
		END
		$$`,
		`CREATE TRIGGER decision_runs_guard BEFORE INSERT ON decision.runs
			FOR EACH ROW EXECUTE FUNCTION decision.guard_run_insert()`,
		`CREATE FUNCTION decision.reject_change() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION '% on decision.% is not allowed: decisions are kept as they were made', TG_OP, TG_TABLE_NAME
				USING ERRCODE = 'integrity_constraint_violation';
		END
		$$`,
		`CREATE TRIGGER decision_requests_append_only BEFORE UPDATE OR DELETE ON decision.requests
			FOR EACH ROW EXECUTE FUNCTION decision.reject_change()`,
		`CREATE TRIGGER decision_runs_append_only BEFORE UPDATE OR DELETE ON decision.runs
			FOR EACH ROW EXECUTE FUNCTION decision.reject_change()`,
		`CREATE TRIGGER decision_rule_traces_append_only BEFORE UPDATE OR DELETE ON decision.rule_traces
			FOR EACH ROW EXECUTE FUNCTION decision.reject_change()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}

// Down Reverse the migrations. Rolling back deletes every stored decision (see AGENT_RUNBOOK.md).
func (r *M20261127000001CreateDecisionTables) Down() error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS decision.rule_traces`,
		`DROP TABLE IF EXISTS decision.runs`,
		`DROP TABLE IF EXISTS decision.requests`,
		`DROP FUNCTION IF EXISTS decision.guard_run_insert()`,
		`DROP FUNCTION IF EXISTS decision.reject_change()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}
