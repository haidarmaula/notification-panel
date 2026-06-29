CREATE INDEX idx_devices_user
ON devices(user_id);

CREATE INDEX idx_devices_platform
ON devices(platform);

CREATE INDEX idx_devices_active
ON devices(is_active);

CREATE INDEX idx_notifications_status
ON notifications(status);

CREATE INDEX idx_notifications_type
ON notifications(notification_type);

CREATE INDEX idx_notifications_created_at
ON notifications(created_at);

CREATE INDEX idx_notification_targets_notification
ON notification_targets(notification_id);

CREATE INDEX idx_notification_targets_type
ON notification_targets(target_type);

CREATE INDEX idx_notification_deliveries_notification
ON notification_deliveries(notification_id);

CREATE INDEX idx_notification_deliveries_device
ON notification_deliveries(device_id);

CREATE INDEX idx_notification_deliveries_status
ON notification_deliveries(status);
