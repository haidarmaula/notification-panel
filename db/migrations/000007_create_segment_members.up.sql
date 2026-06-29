CREATE TABLE segment_members (
    id BIGSERIAL PRIMARY KEY,

    segment_id BIGINT NOT NULL,

    user_id BIGINT NOT NULL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT fk_segment_members_segment
        FOREIGN KEY (segment_id)
        REFERENCES segments(id)
        ON DELETE CASCADE,

    CONSTRAINT fk_segment_members_user
        FOREIGN KEY (user_id)
        REFERENCES users(id)
        ON DELETE CASCADE,

    CONSTRAINT uq_segment_member
        UNIQUE(segment_id, user_id)
);

CREATE INDEX idx_segment_members_segment
ON segment_members(segment_id);

CREATE INDEX idx_segment_members_user
ON segment_members(user_id);
