-- Create restricted synthetic local identities for the real onboarding daemons.
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='hello_onboarding_provisioner') THEN
        CREATE ROLE hello_onboarding_provisioner NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='hello_onboarding_verifier') THEN
        CREATE ROLE hello_onboarding_verifier NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='local_onboarding_provisioner') THEN
        CREATE ROLE local_onboarding_provisioner LOGIN PASSWORD 'fixture-only';
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='local_onboarding_verifier') THEN
        CREATE ROLE local_onboarding_verifier LOGIN PASSWORD 'fixture-only';
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='local_onboarding_verifier_1') THEN
        CREATE ROLE local_onboarding_verifier_1 LOGIN PASSWORD 'fixture-only';
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='local_onboarding_verifier_2') THEN
        CREATE ROLE local_onboarding_verifier_2 LOGIN PASSWORD 'fixture-only';
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='local_onboarding_verifier_3') THEN
        CREATE ROLE local_onboarding_verifier_3 LOGIN PASSWORD 'fixture-only';
    END IF;
END $$;
GRANT hello_onboarding_provisioner TO local_onboarding_provisioner;
GRANT hello_onboarding_verifier TO local_onboarding_verifier, local_onboarding_verifier_1,
    local_onboarding_verifier_2, local_onboarding_verifier_3;
