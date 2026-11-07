-- Optional, reviewed owner step for a separately provisioned inventory identity.
-- Scope this identity's database/tenant access and audit before granting it.
-- The preview login gets neither runtime credentials nor data mutation rights.
GRANT USAGE ON SCHEMA public TO :"preview_reader";
GRANT SELECT (id,public_id,status) ON public.applications TO :"preview_reader";
GRANT SELECT (application_id,user_id,identity_namespace,subject) ON public.mobile_identities TO :"preview_reader";
GRANT SELECT (application_id,id,status) ON public.users TO :"preview_reader";
GRANT SELECT (application_id,owner_user_id) ON public.mobile_installations TO :"preview_reader";
GRANT SELECT (application_id,id,owner_user_id,retired_at) ON public.installation_bindings TO :"preview_reader";
GRANT SELECT (application_id,binding_id,destination) ON public.current_binding_routes TO :"preview_reader";
GRANT SELECT (application_id,user_id,is_active) ON public.device_tokens TO :"preview_reader";
GRANT SELECT (application_id,user_id) ON public.segment_members TO :"preview_reader";
GRANT SELECT (application_id,user_id,notification_id) ON public.notification_targets,public.notification_deliveries,public.notification_reads TO :"preview_reader";
GRANT SELECT (application_id,notification_id,id,owner_user_id,status,attempt_count,last_attempt_at,attempt_resolved_at,last_result_disposition,provider_accepted_at)
    ON public.notification_delivery_intents TO :"preview_reader";
GRANT SELECT (application_id,user_id) ON public.notification_receipt_requests TO :"preview_reader";
GRANT SELECT (application_id,delivery_id,expires_unix) ON public.notification_receipt_metadata TO :"preview_reader";
GRANT SELECT (application_id,delivery_id,event) ON public.notification_observations TO :"preview_reader";
GRANT SELECT (application_id,delivery_id) ON public.notification_update_outbox TO :"preview_reader";
GRANT SELECT (id,application_id,origin_epoch,source_epoch,lease_until,preparation_hold_reason) ON public.notifications TO :"preview_reader";
GRANT SELECT (application_id,notification_id) ON public.notification_send_outbox TO :"preview_reader";
GRANT SELECT (application_id,notification_id,request_id,log_source_id,original_partition,original_offset,origin_epoch,source_epoch,outcome)
    ON public.notification_replay_attempts TO :"preview_reader";
GRANT SELECT (log_source_id,original_partition,original_offset,resolved_application_id,resolved_request_id,resolved_outcome)
    ON public.notification_replay_controls TO :"preview_reader";
GRANT SELECT (singleton,sealed,migration_hold) ON public.notification_runtime TO :"preview_reader";
GRANT SELECT (application_id,hold_new) ON public.application_dispatch_policy TO :"preview_reader";
GRANT SELECT (application_id,held) ON public.retention_policies TO :"preview_reader";
