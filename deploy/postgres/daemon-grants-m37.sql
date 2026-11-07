-- Included by the owner grant template after migration 37.
INSERT INTO daemon_grant_spec VALUES
('sa','current_binding_routes','SELECT','application_id,binding_id,destination,origin_epoch,source_epoch,revision'),
('d','current_binding_routes','SELECT','application_id,binding_id,destination,origin_epoch,source_epoch,revision,resume_from'),
('sa','notification_delivery_intents','INSERT','audience_route_revision');
-- Delivery already has SELECT on notification_delivery_intents, including its frozen route revision.
