CREATE TABLE devices (

    id UNIQUEIDENTIFIER PRIMARY KEY DEFAULT NEWID(),

    user_id UNIQUEIDENTIFIER NOT NULL,

    platform NVARCHAR(20) NOT NULL,

    device_token NVARCHAR(512) NOT NULL UNIQUE,

    app_version NVARCHAR(50),

    os_version NVARCHAR(50),

    device_model NVARCHAR(100),

    is_active BIT NOT NULL DEFAULT 1,

    last_seen_at DATETIME2,

    created_at DATETIME2 NOT NULL DEFAULT SYSUTCDATETIME(),

    updated_at DATETIME2 NOT NULL DEFAULT SYSUTCDATETIME(),

    CONSTRAINT fk_devices_user
        FOREIGN KEY(user_id)
        REFERENCES users(id)
        ON DELETE CASCADE
);
