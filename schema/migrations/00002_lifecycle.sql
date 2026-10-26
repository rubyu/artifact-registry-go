CREATE OR REPLACE FUNCTION __SCHEMA__.artifact_registry_begin_publication(
    p_artifact_ref UUID,
    p_artifact_object_ref UUID,
    p_object_path TEXT,
    p_digest_value TEXT,
    p_created_at TIMESTAMPTZ
)
RETURNS TABLE (artifact_ref UUID, artifact_object_ref UUID)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    __RUNTIME_GUARD__

    INSERT INTO __SCHEMA__.artifact_registry_objects (
        artifact_object_ref,
        object_path,
        digest_value,
        size_bytes,
        status,
        created_at
    ) VALUES (
        p_artifact_object_ref,
        p_object_path,
        p_digest_value,
        NULL,
        'uploading',
        p_created_at
    );

    INSERT INTO __SCHEMA__.artifact_registry_publications (
        artifact_ref,
        artifact_object_ref,
        status,
        created_at
    ) VALUES (
        p_artifact_ref,
        p_artifact_object_ref,
        'staged',
        p_created_at
    );

    RETURN QUERY SELECT p_artifact_ref, p_artifact_object_ref;
END;
$$;

CREATE OR REPLACE FUNCTION __SCHEMA__.artifact_registry_confirm_object_upload(
    p_artifact_object_ref UUID,
    p_size_bytes BIGINT,
    p_uploaded_at TIMESTAMPTZ
)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
    changed BOOLEAN;
BEGIN
    __RUNTIME_GUARD__

    WITH updated AS (
        UPDATE __SCHEMA__.artifact_registry_objects AS object_row
        SET size_bytes = p_size_bytes,
            status = 'available',
            uploaded_at = p_uploaded_at
        WHERE object_row.artifact_object_ref = p_artifact_object_ref
          AND object_row.status = 'uploading'
          AND EXISTS (
              SELECT 1
              FROM __SCHEMA__.artifact_registry_publications AS publication
              WHERE publication.artifact_object_ref = p_artifact_object_ref
                AND publication.status = 'staged'
          )
        RETURNING 1
    )
    SELECT EXISTS (SELECT 1 FROM updated) INTO changed;
    RETURN changed;
END;
$$;

CREATE OR REPLACE FUNCTION __SCHEMA__.artifact_registry_fail_object_upload(
    p_artifact_object_ref UUID
)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
    changed BOOLEAN;
BEGIN
    __RUNTIME_GUARD__

    WITH updated AS (
        UPDATE __SCHEMA__.artifact_registry_objects AS object_row
        SET status = 'upload_failed'
        WHERE object_row.artifact_object_ref = p_artifact_object_ref
          AND object_row.status = 'uploading'
          AND EXISTS (
              SELECT 1
              FROM __SCHEMA__.artifact_registry_publications AS publication
              WHERE publication.artifact_object_ref = p_artifact_object_ref
                AND publication.status IN ('staged', 'aborted')
          )
        RETURNING 1
    )
    SELECT EXISTS (SELECT 1 FROM updated) INTO changed;
    RETURN changed;
END;
$$;

CREATE OR REPLACE FUNCTION __SCHEMA__.artifact_registry_commit_publication(
    p_artifact_ref UUID,
    p_artifact_object_ref UUID,
    p_committed_at TIMESTAMPTZ
)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
    changed BOOLEAN;
BEGIN
    __RUNTIME_GUARD__

    WITH updated AS (
        UPDATE __SCHEMA__.artifact_registry_publications AS publication
        SET status = 'committed',
            committed_at = p_committed_at
        WHERE publication.artifact_ref = p_artifact_ref
          AND publication.artifact_object_ref = p_artifact_object_ref
          AND publication.status = 'staged'
          AND EXISTS (
              SELECT 1
              FROM __SCHEMA__.artifact_registry_objects AS object_row
              WHERE object_row.artifact_object_ref = p_artifact_object_ref
                AND object_row.status = 'available'
          )
        RETURNING 1
    )
    SELECT EXISTS (SELECT 1 FROM updated) INTO changed;
    RETURN changed;
END;
$$;

CREATE OR REPLACE FUNCTION __SCHEMA__.artifact_registry_abort_publication(
    p_artifact_ref UUID,
    p_artifact_object_ref UUID,
    p_aborted_at TIMESTAMPTZ
)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
    changed BOOLEAN;
BEGIN
    __RUNTIME_MAINTENANCE_GUARD__

    WITH updated AS (
        UPDATE __SCHEMA__.artifact_registry_publications AS publication
        SET status = 'aborted',
            aborted_at = p_aborted_at
        WHERE publication.artifact_ref = p_artifact_ref
          AND publication.artifact_object_ref = p_artifact_object_ref
          AND publication.status = 'staged'
        RETURNING 1
    )
    SELECT EXISTS (SELECT 1 FROM updated) INTO changed;
    RETURN changed;
END;
$$;

CREATE OR REPLACE FUNCTION __SCHEMA__.artifact_registry_request_discard(
    p_artifact_ref UUID,
    p_requested_at TIMESTAMPTZ
)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
    changed BOOLEAN;
BEGIN
    __RUNTIME_GUARD__

    WITH updated AS (
        UPDATE __SCHEMA__.artifact_registry_publications AS publication
        SET status = 'discarding',
            discard_requested_at = p_requested_at
        WHERE publication.artifact_ref = p_artifact_ref
          AND publication.status = 'committed'
        RETURNING 1
    )
    SELECT EXISTS (SELECT 1 FROM updated) INTO changed;
    RETURN changed;
END;
$$;

CREATE OR REPLACE FUNCTION __SCHEMA__.artifact_registry_confirm_object_deleted(
    p_artifact_object_ref UUID,
    p_deleted_at TIMESTAMPTZ
)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
    changed BOOLEAN;
BEGIN
    __RUNTIME_MAINTENANCE_GUARD__

    WITH updated AS (
        UPDATE __SCHEMA__.artifact_registry_objects AS object_row
        SET status = 'deleted',
            deleted_at = p_deleted_at
        WHERE object_row.artifact_object_ref = p_artifact_object_ref
          AND object_row.status IN (
              'uploading', 'upload_failed', 'available', 'delete_failed'
          )
          AND EXISTS (
              SELECT 1
              FROM __SCHEMA__.artifact_registry_publications AS publication
              WHERE publication.artifact_object_ref = p_artifact_object_ref
                AND __DELETE_PUBLICATION_GUARD__
          )
        RETURNING 1
    )
    SELECT EXISTS (SELECT 1 FROM updated) INTO changed;
    RETURN changed;
END;
$$;

CREATE OR REPLACE FUNCTION __SCHEMA__.artifact_registry_mark_object_delete_failed(
    p_artifact_object_ref UUID
)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
    changed BOOLEAN;
BEGIN
    __RUNTIME_MAINTENANCE_GUARD__

    WITH updated AS (
        UPDATE __SCHEMA__.artifact_registry_objects AS object_row
        SET status = 'delete_failed'
        WHERE object_row.artifact_object_ref = p_artifact_object_ref
          AND object_row.status IN (
              'uploading', 'upload_failed', 'available', 'delete_failed'
          )
          AND EXISTS (
              SELECT 1
              FROM __SCHEMA__.artifact_registry_publications AS publication
              WHERE publication.artifact_object_ref = p_artifact_object_ref
                AND __DELETE_PUBLICATION_GUARD__
          )
        RETURNING 1
    )
    SELECT EXISTS (SELECT 1 FROM updated) INTO changed;
    RETURN changed;
END;
$$;

CREATE OR REPLACE FUNCTION __SCHEMA__.artifact_registry_mark_publication_discarded(
    p_artifact_ref UUID,
    p_discarded_at TIMESTAMPTZ
)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
    changed BOOLEAN;
BEGIN
    __MAINTENANCE_GUARD__

    WITH updated AS (
        UPDATE __SCHEMA__.artifact_registry_publications AS publication
        SET status = 'discarded',
            discarded_at = p_discarded_at
        WHERE publication.artifact_ref = p_artifact_ref
          AND publication.status = 'discarding'
          AND EXISTS (
              SELECT 1
              FROM __SCHEMA__.artifact_registry_objects AS object_row
              WHERE object_row.artifact_object_ref = publication.artifact_object_ref
                AND object_row.status = 'deleted'
          )
        RETURNING 1
    )
    SELECT EXISTS (SELECT 1 FROM updated) INTO changed;
    RETURN changed;
END;
$$;

REVOKE EXECUTE ON FUNCTION __SCHEMA__.set_updated_at() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION
    __SCHEMA__.artifact_registry_begin_publication(uuid, uuid, text, text, timestamptz),
    __SCHEMA__.artifact_registry_confirm_object_upload(uuid, bigint, timestamptz),
    __SCHEMA__.artifact_registry_fail_object_upload(uuid),
    __SCHEMA__.artifact_registry_commit_publication(uuid, uuid, timestamptz),
    __SCHEMA__.artifact_registry_abort_publication(uuid, uuid, timestamptz),
    __SCHEMA__.artifact_registry_request_discard(uuid, timestamptz),
    __SCHEMA__.artifact_registry_confirm_object_deleted(uuid, timestamptz),
    __SCHEMA__.artifact_registry_mark_object_delete_failed(uuid),
    __SCHEMA__.artifact_registry_mark_publication_discarded(uuid, timestamptz)
    FROM PUBLIC;
