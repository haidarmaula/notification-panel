-- Observed through authenticated Kafka admin metadata on 2026-11-12.
-- Registration only: a separate authorized review is required for activation.
\set ON_ERROR_STOP on
BEGIN;
DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM public.notification_log_sources
        WHERE topic IN ('notification.send.requested', 'notification.delivery.updated')
          AND (cluster_identity <> 'eGTxoZosTWKe7DWll1Teug'
            OR topic_incarnation <> CASE topic
                WHEN 'notification.send.requested' THEN '7DWU8nW_Sku7FufiVO568Q'
                ELSE 'Kll9XLH2R2eZUHpILxg2lg' END
            OR active)
    ) THEN
        RAISE EXCEPTION 'Existing source differs or is already active; reconcile before proceeding';
    END IF;
END $$;
INSERT INTO public.notification_log_sources(id,topic,cluster_identity,topic_incarnation,active)
VALUES
  (gen_random_uuid(),'notification.send.requested','eGTxoZosTWKe7DWll1Teug','7DWU8nW_Sku7FufiVO568Q',false),
  (gen_random_uuid(),'notification.delivery.updated','eGTxoZosTWKe7DWll1Teug','Kll9XLH2R2eZUHpILxg2lg',false)
ON CONFLICT (cluster_identity,topic,topic_incarnation) DO NOTHING;
SELECT json_agg(s ORDER BY topic) FROM (
    SELECT id,topic,cluster_identity,topic_incarnation,active
    FROM public.notification_log_sources
    WHERE cluster_identity='eGTxoZosTWKe7DWll1Teug'
      AND topic IN ('notification.send.requested','notification.delivery.updated')
) s;
COMMIT;
