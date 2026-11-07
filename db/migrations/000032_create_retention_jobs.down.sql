DO $$
BEGIN
    LOCK TABLE public.retention_policies,public.retention_grants,public.retention_batches IN ACCESS EXCLUSIVE MODE;
    IF EXISTS(SELECT 1 FROM public.retention_policies) OR EXISTS(SELECT 1 FROM public.retention_grants)
       OR EXISTS(SELECT 1 FROM public.retention_batches) THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='cannot remove retention policy or maintenance evidence';
    END IF;
    DROP FUNCTION public.run_retention_batch(uuid,uuid,uuid,uuid,text,integer,boolean);
    DROP TABLE public.retention_batches,public.retention_grants,public.retention_policies;
    DROP FUNCTION public.guard_retention_grant();
    DROP INDEX public.idx_origin_retention_app,public.idx_receipt_retention_app;
END;
$$;
