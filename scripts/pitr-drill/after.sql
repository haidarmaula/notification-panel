INSERT INTO notifications(id,application_id,title,body,created_by)
VALUES (903,901,'beyond restore point','synthetic',901);
UPDATE staff_sessions SET revoked_at=clock_timestamp(),revocation_reason='logout' WHERE staff_user_id=901;
UPDATE staff_users SET is_active=false WHERE id=901;
