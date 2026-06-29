DROP INDEX idx_devices_user ON devices;

DROP INDEX idx_devices_platform ON devices;

DROP INDEX idx_devices_active ON devices;

DROP INDEX idx_notifications_status ON notifications;

DROP INDEX idx_notifications_type ON notifications;

DROP INDEX idx_notifications_created_at ON notifications;

DROP INDEX idx_notification_targets_notification ON notification_targets;

DROP INDEX idx_notification_targets_type ON notification_targets;

DROP INDEX idx_notification_deliveries_notification ON notification_deliveries;

DROP INDEX idx_notification_deliveries_device ON notification_deliveries;

DROP INDEX idx_notification_deliveries_status ON notification_deliveries;
