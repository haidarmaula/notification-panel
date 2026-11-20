DROP FUNCTION supersede_unusable_onboarding_release(bigint,text);
DROP FUNCTION bind_onboarding_release(uuid,uuid,bigint);
DROP FUNCTION apply_onboarding_release(uuid,uuid,uuid);
DROP FUNCTION lock_onboarding_release_approval(uuid,text,text);
DROP TRIGGER require_onboarding_dispatch_completion ON application_dispatch_grants;
DROP FUNCTION require_onboarding_dispatch_completion();
DROP TABLE application_onboarding_dispatch_delegations;
