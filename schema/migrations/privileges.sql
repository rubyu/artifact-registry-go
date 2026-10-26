REVOKE ALL PRIVILEGES ON SCHEMA __SCHEMA__ FROM PUBLIC, __MIGRATOR__, __RUNTIME__, __MAINTENANCE__, __READINESS__;
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA __SCHEMA__
    FROM PUBLIC, __MIGRATOR__, __RUNTIME__, __MAINTENANCE__, __READINESS__;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA __SCHEMA__
    FROM PUBLIC, __MIGRATOR__, __RUNTIME__, __MAINTENANCE__, __READINESS__;
REVOKE USAGE ON TYPE __SCHEMA__.artifact_object_status_type,
    __SCHEMA__.artifact_publication_status_type
    FROM PUBLIC, __MIGRATOR__, __RUNTIME__, __MAINTENANCE__, __READINESS__;
REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA __SCHEMA__
    FROM PUBLIC, __MIGRATOR__, __RUNTIME__, __MAINTENANCE__, __READINESS__;

ALTER DEFAULT PRIVILEGES FOR ROLE __OWNER__
    REVOKE ALL PRIVILEGES ON TYPES FROM PUBLIC, __MIGRATOR__, __RUNTIME__, __MAINTENANCE__, __READINESS__;
ALTER DEFAULT PRIVILEGES FOR ROLE __OWNER__
    REVOKE ALL PRIVILEGES ON FUNCTIONS FROM PUBLIC, __MIGRATOR__, __RUNTIME__, __MAINTENANCE__, __READINESS__;
ALTER DEFAULT PRIVILEGES FOR ROLE __OWNER__ IN SCHEMA __SCHEMA__
    REVOKE ALL PRIVILEGES ON TABLES FROM PUBLIC, __MIGRATOR__, __RUNTIME__, __MAINTENANCE__, __READINESS__;
ALTER DEFAULT PRIVILEGES FOR ROLE __OWNER__ IN SCHEMA __SCHEMA__
    REVOKE ALL PRIVILEGES ON SEQUENCES FROM PUBLIC, __MIGRATOR__, __RUNTIME__, __MAINTENANCE__, __READINESS__;
ALTER DEFAULT PRIVILEGES FOR ROLE __OWNER__ IN SCHEMA __SCHEMA__
    REVOKE ALL PRIVILEGES ON TYPES FROM PUBLIC, __MIGRATOR__, __RUNTIME__, __MAINTENANCE__, __READINESS__;
ALTER DEFAULT PRIVILEGES FOR ROLE __OWNER__ IN SCHEMA __SCHEMA__
    REVOKE ALL PRIVILEGES ON FUNCTIONS FROM PUBLIC, __MIGRATOR__, __RUNTIME__, __MAINTENANCE__, __READINESS__;

GRANT USAGE ON SCHEMA __SCHEMA__
    TO __MIGRATOR__, __RUNTIME__, __MAINTENANCE__,
       __READINESS__;
GRANT SELECT ON __SCHEMA__.schema_migrations
    TO __MIGRATOR__, __READINESS__;
GRANT SELECT (version_id, is_applied) ON __SCHEMA__.schema_migrations
    TO __RUNTIME__;
GRANT SELECT (
    artifact_object_ref, object_path, digest_value, size_bytes, status,
    uploaded_at, deleted_at
) ON __SCHEMA__.artifact_registry_objects TO __RUNTIME__;
GRANT SELECT (
    artifact_ref, artifact_object_ref, status, committed_at,
    discard_requested_at, discarded_at, aborted_at
) ON __SCHEMA__.artifact_registry_publications TO __RUNTIME__;
GRANT SELECT (
    artifact_object_id, artifact_object_ref, object_path, status, updated_at
) ON __SCHEMA__.artifact_registry_objects TO __MAINTENANCE__;
GRANT SELECT (
    artifact_publication_id, artifact_ref, artifact_object_ref, status,
    created_at, aborted_at, discard_requested_at
) ON __SCHEMA__.artifact_registry_publications TO __MAINTENANCE__;

GRANT EXECUTE ON FUNCTION
    __SCHEMA__.artifact_registry_begin_publication(uuid, uuid, text, text, timestamptz),
    __SCHEMA__.artifact_registry_confirm_object_upload(uuid, bigint, timestamptz),
    __SCHEMA__.artifact_registry_fail_object_upload(uuid),
    __SCHEMA__.artifact_registry_commit_publication(uuid, uuid, timestamptz),
    __SCHEMA__.artifact_registry_abort_publication(uuid, uuid, timestamptz),
    __SCHEMA__.artifact_registry_request_discard(uuid, timestamptz),
    __SCHEMA__.artifact_registry_confirm_object_deleted(uuid, timestamptz),
    __SCHEMA__.artifact_registry_mark_object_delete_failed(uuid)
    TO __RUNTIME__;
GRANT EXECUTE ON FUNCTION
    __SCHEMA__.artifact_registry_abort_publication(uuid, uuid, timestamptz),
    __SCHEMA__.artifact_registry_confirm_object_deleted(uuid, timestamptz),
    __SCHEMA__.artifact_registry_mark_object_delete_failed(uuid),
    __SCHEMA__.artifact_registry_mark_publication_discarded(uuid, timestamptz)
    TO __MAINTENANCE__;
