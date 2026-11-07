CREATE INDEX idx_active_binding_owner
    ON installation_bindings (application_id,owner_user_id) WHERE retired_at IS NULL;
