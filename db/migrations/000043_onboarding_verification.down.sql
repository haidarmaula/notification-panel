DO $$ BEGIN
    IF EXISTS(SELECT FROM application_onboarding_verifier_scopes) OR EXISTS(SELECT FROM application_provider_checks)
    THEN RAISE EXCEPTION 'verification evidence requires explicit recovery before rollback'; END IF;
END $$;
DROP FUNCTION finish_application_onboarding_verification(uuid,uuid,bigint,text,text);
DROP FUNCTION claim_application_onboarding_verification(uuid,uuid);
DROP TABLE application_onboarding_verifier_scopes;
