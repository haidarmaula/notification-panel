-- Run in a read-only transaction with a trusted search_path and a statement timeout.
-- Count each binding once, even when multiple campaigns target its generation.
WITH pending_by_version AS (
    SELECT b.application_id, b.encryption_key_version, count(*) AS pending
    FROM notification_delivery_intents d
    JOIN installation_bindings b ON (b.application_id, b.id) = (d.application_id, d.binding_id)
    WHERE d.status = 'PENDING'
    GROUP BY b.application_id, b.encryption_key_version
), bindings AS (
    SELECT application_id, encryption_key_version, count(*) AS retained,
           count(*) FILTER (WHERE retired_at IS NULL) AS active
    FROM installation_bindings
    GROUP BY application_id, encryption_key_version
), inventory AS (
    SELECT b.application_id, 'binding_encryption' AS purpose, NULL::text AS environment,
           b.encryption_key_version::text AS key_id, b.retained, coalesce(p.pending, 0) AS pending,
           b.active AS active_bindings, NULL::bigint AS unexpired_capabilities,
           NULL::bigint AS last_expiry_unix
    FROM bindings b LEFT JOIN pending_by_version p USING (application_id, encryption_key_version)
    UNION ALL
    SELECT m.application_id, 'receipt', m.environment, m.key_id, count(*),
           count(*) FILTER (WHERE d.status = 'PENDING'), NULL::bigint,
           count(*) FILTER (WHERE m.expires_unix > extract(epoch FROM transaction_timestamp())),
           max(m.expires_unix)
    FROM notification_receipt_metadata m JOIN notification_delivery_intents d
        ON (d.application_id, d.id) = (m.application_id, m.delivery_id)
    GROUP BY m.application_id, m.environment, m.key_id
)
SELECT a.public_id AS application, i.purpose, i.environment, i.key_id, i.retained, i.pending,
       i.active_bindings, i.unexpired_capabilities, i.last_expiry_unix
FROM inventory i JOIN applications a ON a.id = i.application_id
ORDER BY a.public_id, i.purpose, i.environment, i.key_id;
