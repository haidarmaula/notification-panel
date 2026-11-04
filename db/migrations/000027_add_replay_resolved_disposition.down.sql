DO $$
BEGIN
    LOCK TABLE public.notification_replay_attempts IN ACCESS EXCLUSIVE MODE;
    IF EXISTS(SELECT 1 FROM public.notification_replay_attempts WHERE outcome='already_resolved') THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='cannot remove retained replay disposition';
    END IF;
    ALTER TABLE public.notification_replay_attempts DROP CONSTRAINT notification_replay_attempts_outcome_check;
    ALTER TABLE public.notification_replay_attempts ADD CONSTRAINT notification_replay_attempts_outcome_check
        CHECK (outcome IN ('started','held','rejected','expired','reconciliation_required',
            'already_prepared','terminal','published','publish_unconfirmed','abandoned_unknown'));
END;
$$;
