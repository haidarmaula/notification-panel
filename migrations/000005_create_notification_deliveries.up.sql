CREATE TABLE notification_deliveries (

    id UNIQUEIDENTIFIER PRIMARY KEY DEFAULT NEWID(),

    notification_id UNIQUEIDENTIFIER NOT NULL,

    device_id UNIQUEIDENTIFIER NOT NULL,

    status NVARCHAR(30) NOT NULL,

    retry_count INT NOT NULL DEFAULT 0,

    error_message NVARCHAR(MAX),

    fcm_message_id NVARCHAR(255),

    delivered_at DATETIME2,

    created_at DATETIME2 NOT NULL DEFAULT SYSUTCDATETIME(),

    CONSTRAINT fk_delivery_notification
        FOREIGN KEY(notification_id)
        REFERENCES notifications(id)
        ON DELETE CASCADE,

    CONSTRAINT fk_delivery_device
        FOREIGN KEY(device_id)
        REFERENCES devices(id)
        ON DELETE CASCADE
);
