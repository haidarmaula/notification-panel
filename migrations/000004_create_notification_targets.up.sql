CREATE TABLE notification_targets (

    id UNIQUEIDENTIFIER PRIMARY KEY DEFAULT NEWID(),

    notification_id UNIQUEIDENTIFIER NOT NULL,

    target_type NVARCHAR(30) NOT NULL,

    target_value NVARCHAR(255),

    created_at DATETIME2 NOT NULL DEFAULT SYSUTCDATETIME(),

    CONSTRAINT fk_target_notification
        FOREIGN KEY(notification_id)
        REFERENCES notifications(id)
        ON DELETE CASCADE
);
