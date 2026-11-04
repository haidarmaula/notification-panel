-- A later authorized request records an immutable no-op against earlier
-- resolution without pretending it performed the original publication.
ALTER TABLE notification_replay_attempts DROP CONSTRAINT notification_replay_attempts_outcome_check;
ALTER TABLE notification_replay_attempts ADD CONSTRAINT notification_replay_attempts_outcome_check
    CHECK (outcome IN ('started','held','rejected','expired','reconciliation_required',
        'already_prepared','terminal','already_resolved','published','publish_unconfirmed','abandoned_unknown'));
