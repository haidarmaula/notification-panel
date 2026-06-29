CREATE TABLE notifications (

    id UNIQUEIDENTIFIER PRIMARY KEY DEFAULT NEWID(),

    title NVARCHAR(255) NOT NULL,

    body NVARCHAR(MAX) NOT NULL,

    image_url NVARCHAR(500),

    deep_link NVARCHAR(500),

    data NVARCHAR(MAX),

    priority NVARCHAR(20) NOT NULL DEFAULT 'normal',

    notification_type NVARCHAR(30) NOT NULL,

    status NVARCHAR(30) NOT NULL DEFAULT 'pending',

    scheduled_at DATETIME2,

    published_at DATETIME2,

    completed_at DATETIME2,

    created_by UNIQUEIDENTIFIER NOT NULL,

    created_at DATETIME2 NOT NULL DEFAULT SYSUTCDATETIME(),

    updated_at DATETIME2 NOT NULL DEFAULT SYSUTCDATETIME(),

    CONSTRAINT fk_notification_creator
        FOREIGN KEY(created_by)
        REFERENCES users(id)
);
