DO $$ BEGIN
    IF EXISTS (SELECT FROM application_onboarding_approved_targets)
       OR EXISTS (SELECT FROM application_onboarding_cohort_targets)
       OR EXISTS (SELECT FROM application_onboarding_cohort_scopes)
    THEN RAISE EXCEPTION 'cannot remove cohort scopes or retained evidence'; END IF;
END $$;
DROP FUNCTION finish_onboarding_cohort_target(uuid,text,uuid,uuid,uuid,uuid,uuid,bigint,uuid,bigint,text,text);
DROP FUNCTION claim_onboarding_cohort_target(uuid,text,uuid);
DROP FUNCTION finalize_onboarding_cohort(uuid,text,uuid,uuid,bigint);
DROP FUNCTION initialize_onboarding_cohort(uuid,text,uuid,uuid);
DROP FUNCTION onboarding_cohort_eligible(uuid,boolean);
DROP FUNCTION lock_onboarding_cohort(uuid,text,uuid,text);
DROP VIEW application_onboarding_live_targets;
DROP FUNCTION provision_onboarding_cohort_approval(uuid,text,uuid,uuid,bigint[],text,bytea,timestamptz);
DROP TABLE application_onboarding_cohort_scopes;
DROP TABLE application_onboarding_cohort_targets;
DROP TABLE application_onboarding_approved_targets;
