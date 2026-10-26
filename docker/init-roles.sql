DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_roles WHERE rolname = 'artifact_registry_owner'
    ) THEN
        CREATE ROLE artifact_registry_owner NOLOGIN;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_roles WHERE rolname = 'artifact_registry_migrator'
    ) THEN
        CREATE ROLE artifact_registry_migrator LOGIN NOINHERIT
            PASSWORD 'artifact-registry-migrator-test-only';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_roles WHERE rolname = 'artifact_registry_runtime'
    ) THEN
        CREATE ROLE artifact_registry_runtime LOGIN NOINHERIT
            PASSWORD 'artifact-registry-runtime-test-only';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_roles WHERE rolname = 'artifact_registry_maintenance'
    ) THEN
        CREATE ROLE artifact_registry_maintenance LOGIN NOINHERIT
            PASSWORD 'artifact-registry-maintenance-test-only';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_roles WHERE rolname = 'artifact_registry_readiness'
    ) THEN
        CREATE ROLE artifact_registry_readiness LOGIN NOINHERIT
            PASSWORD 'artifact-registry-readiness-test-only';
    END IF;
END
$$;

ALTER ROLE artifact_registry_owner WITH NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB
    NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1;
ALTER ROLE artifact_registry_migrator WITH LOGIN NOINHERIT NOSUPERUSER NOCREATEDB
    NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1
    PASSWORD 'artifact-registry-migrator-test-only' VALID UNTIL 'infinity';
ALTER ROLE artifact_registry_runtime WITH LOGIN NOINHERIT NOSUPERUSER NOCREATEDB
    NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1
    PASSWORD 'artifact-registry-runtime-test-only' VALID UNTIL 'infinity';
ALTER ROLE artifact_registry_maintenance WITH LOGIN NOINHERIT NOSUPERUSER
    NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1
    PASSWORD 'artifact-registry-maintenance-test-only' VALID UNTIL 'infinity';
ALTER ROLE artifact_registry_readiness WITH LOGIN NOINHERIT NOSUPERUSER NOCREATEDB
    NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1
    PASSWORD 'artifact-registry-readiness-test-only' VALID UNTIL 'infinity';

ALTER ROLE artifact_registry_owner RESET ALL;
ALTER ROLE artifact_registry_migrator RESET ALL;
ALTER ROLE artifact_registry_runtime RESET ALL;
ALTER ROLE artifact_registry_maintenance RESET ALL;
ALTER ROLE artifact_registry_readiness RESET ALL;

DO $$
DECLARE
    membership record;
BEGIN
    FOR membership IN
        SELECT granted.rolname AS granted_role, member.rolname AS member_role
        FROM pg_auth_members AS auth_members
        JOIN pg_roles AS granted ON granted.oid = auth_members.roleid
        JOIN pg_roles AS member ON member.oid = auth_members.member
        WHERE granted.rolname = ANY(ARRAY[
            'artifact_registry_owner',
            'artifact_registry_migrator',
            'artifact_registry_runtime',
            'artifact_registry_maintenance',
            'artifact_registry_readiness'
        ]::text[])
           OR member.rolname = ANY(ARRAY[
            'artifact_registry_owner',
            'artifact_registry_migrator',
            'artifact_registry_runtime',
            'artifact_registry_maintenance',
            'artifact_registry_readiness'
        ]::text[])
    LOOP
        EXECUTE format(
            'REVOKE %I FROM %I',
            membership.granted_role,
            membership.member_role
        );
    END LOOP;
END
$$;

GRANT artifact_registry_owner TO artifact_registry_migrator
    WITH INHERIT FALSE, SET TRUE;

REVOKE ALL PRIVILEGES ON DATABASE artifact_registry FROM
    PUBLIC,
    artifact_registry_owner,
    artifact_registry_migrator,
    artifact_registry_runtime,
    artifact_registry_maintenance,
    artifact_registry_readiness;
GRANT CONNECT ON DATABASE artifact_registry TO
    artifact_registry_migrator,
    artifact_registry_runtime,
    artifact_registry_maintenance,
    artifact_registry_readiness;

CREATE SCHEMA IF NOT EXISTS artifact_registry
    AUTHORIZATION artifact_registry_owner;
ALTER SCHEMA artifact_registry OWNER TO artifact_registry_owner;
REVOKE ALL PRIVILEGES ON SCHEMA artifact_registry FROM
    PUBLIC,
    artifact_registry_migrator,
    artifact_registry_runtime,
    artifact_registry_maintenance,
    artifact_registry_readiness;
GRANT USAGE ON SCHEMA artifact_registry TO artifact_registry_migrator;

REVOKE ALL PRIVILEGES ON SCHEMA public FROM
    PUBLIC,
    artifact_registry_owner,
    artifact_registry_migrator,
    artifact_registry_runtime,
    artifact_registry_maintenance,
    artifact_registry_readiness;
