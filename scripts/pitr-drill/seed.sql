INSERT INTO roles(name) VALUES ('synthetic-pitr');
INSERT INTO staff_users(id,role_id,name,email,password_hash)
SELECT actor.id, r.id, 'Fixture', actor.id || '@fixture.invalid', 'synthetic-disabled-password'
FROM roles r CROSS JOIN (VALUES(901),(902)) AS actor(id) WHERE r.name='synthetic-pitr';

INSERT INTO staff_sessions(id,staff_user_id,auth_epoch,session_epoch,refresh_digest,expires_at)
SELECT ('00000000-0000-4000-8000-' || lpad(s.id::text,12,'0'))::uuid,s.id,s.auth_epoch,c.session_epoch,
    sha256(convert_to('synthetic-family-' || s.id,'UTF8')),clock_timestamp()+interval '2 hours'
FROM staff_users s CROSS JOIN staff_session_control c WHERE s.id IN (901,902);

INSERT INTO applications(id,key,name) VALUES (901,'pitr-fixture','PITR fixture');
UPDATE notification_runtime SET sealed=false,migration_hold=false;
INSERT INTO notifications(id,application_id,title,body,created_by)
VALUES (901,901,'base','synthetic',901);
