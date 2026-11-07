-- This write lock serializes administrative roster changes, including the
-- check for another active super administrator. A write also forces a stale
-- REPEATABLE READ or SERIALIZABLE transaction to restart.
CREATE TABLE staff_administration_guard (
    singleton boolean PRIMARY KEY CHECK (singleton)
);
INSERT INTO staff_administration_guard (singleton) VALUES (true);
