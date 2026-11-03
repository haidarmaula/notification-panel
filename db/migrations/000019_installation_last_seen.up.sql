ALTER TABLE mobile_installations ADD COLUMN last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now();
