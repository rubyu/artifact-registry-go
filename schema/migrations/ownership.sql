ALTER TYPE __SCHEMA__.artifact_object_status_type OWNER TO __OWNER__;
ALTER TYPE __SCHEMA__.artifact_publication_status_type OWNER TO __OWNER__;
ALTER TABLE __SCHEMA__.artifact_registry_objects OWNER TO __OWNER__;
ALTER TABLE __SCHEMA__.artifact_registry_publications OWNER TO __OWNER__;
ALTER TABLE __SCHEMA__.schema_migrations OWNER TO __OWNER__;
ALTER SEQUENCE __SCHEMA__.artifact_registry_objects_artifact_object_id_seq
    OWNER TO __OWNER__;
ALTER SEQUENCE __SCHEMA__.artifact_registry_publications_artifact_publication_id_seq
    OWNER TO __OWNER__;
ALTER FUNCTION __SCHEMA__.set_updated_at() OWNER TO __OWNER__;
ALTER FUNCTION
    __SCHEMA__.artifact_registry_begin_publication(
        uuid, uuid, text, text, timestamptz
    ) OWNER TO __OWNER__;
ALTER FUNCTION
    __SCHEMA__.artifact_registry_confirm_object_upload(uuid, bigint, timestamptz)
    OWNER TO __OWNER__;
ALTER FUNCTION
    __SCHEMA__.artifact_registry_fail_object_upload(uuid)
    OWNER TO __OWNER__;
ALTER FUNCTION
    __SCHEMA__.artifact_registry_commit_publication(uuid, uuid, timestamptz)
    OWNER TO __OWNER__;
ALTER FUNCTION
    __SCHEMA__.artifact_registry_abort_publication(uuid, uuid, timestamptz)
    OWNER TO __OWNER__;
ALTER FUNCTION
    __SCHEMA__.artifact_registry_request_discard(uuid, timestamptz)
    OWNER TO __OWNER__;
ALTER FUNCTION
    __SCHEMA__.artifact_registry_confirm_object_deleted(uuid, timestamptz)
    OWNER TO __OWNER__;
ALTER FUNCTION
    __SCHEMA__.artifact_registry_mark_object_delete_failed(uuid)
    OWNER TO __OWNER__;
ALTER FUNCTION
    __SCHEMA__.artifact_registry_mark_publication_discarded(uuid, timestamptz)
    OWNER TO __OWNER__;
