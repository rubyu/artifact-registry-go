package schema

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"strings"

	"github.com/pressly/goose/v3"
	"github.com/rubyu/artifact-registry-go/dbcore"
)

const Version int64 = 2

//go:embed migrations/*.sql
var migrations embed.FS

type Roles struct{ Owner, Migrator, Runtime, Maintenance, Readiness string }

func (r Roles) Validate() error {
	seen := map[string]bool{}
	for _, name := range []string{r.Owner, r.Migrator, r.Runtime, r.Maintenance, r.Readiness} {
		if err := dbcore.ValidateIdentifier(name); err != nil {
			return err
		}
		if seen[name] {
			return errors.New("database roles must be distinct")
		}
		seen[name] = true
	}
	return nil
}

func BuildTarget(connection dbcore.Config, name string, roles *Roles, createSchema bool) (dbcore.MigrationTarget, error) {
	if err := dbcore.ValidateIdentifier(name); err != nil {
		return dbcore.MigrationTarget{}, err
	}
	role := ""
	if roles != nil {
		copy := *roles
		roles = &copy
		if err := roles.Validate(); err != nil {
			return dbcore.MigrationTarget{}, err
		}
		role = roles.Owner
	}
	up := func(files ...string) *goose.GoFunc {
		return &goose.GoFunc{RunTx: func(ctx context.Context, tx *sql.Tx) error {
			for _, file := range files {
				raw, err := migrations.ReadFile("migrations/" + file)
				if err != nil {
					return err
				}
				sqlText := render(string(raw), name, roles)
				if sqlText == "" {
					continue
				}
				if _, err = tx.ExecContext(ctx, sqlText); err != nil {
					return err
				}
			}
			return nil
		}}
	}
	down := &goose.GoFunc{RunTx: func(context.Context, *sql.Tx) error {
		return errors.New("migration is forward-only; restore a snapshot or apply a corrective forward migration")
	}}
	return dbcore.MigrationTarget{Connection: connection, Schema: name, LockKey: "artifact-registry/" + name, MigrationRole: role, CreateSchema: createSchema, ExpectedVersion: Version, Migrations: []*goose.Migration{
		goose.NewGoMigration(1, up("00001_tables.sql"), down), goose.NewGoMigration(2, up("00002_lifecycle.sql", "ownership.sql", "privileges.sql"), down),
	}}, nil
}

func render(text, name string, roles *Roles) string {
	if roles == nil && (strings.Contains(text, "__OWNER__") || strings.Contains(text, "__MIGRATOR__")) {
		return ""
	}
	runtimeGuard := "IF session_user::text <> current_user::text THEN RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'artifact-registry operation is not permitted for session_user'; END IF;"
	maintenanceGuard, combinedGuard := runtimeGuard, runtimeGuard
	deleteGuard := "publication.status IN ('aborted', 'discarding')"
	if roles != nil {
		check := func(names ...string) string {
			parts := []string{}
			for _, value := range names {
				parts = append(parts, "pg_has_role(session_user, '"+value+"', 'USAGE')")
			}
			return "IF NOT (" + strings.Join(parts, " OR ") + ") THEN RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'artifact-registry operation is not permitted for session_user'; END IF;"
		}
		runtimeGuard = check(roles.Runtime)
		maintenanceGuard = check(roles.Maintenance)
		combinedGuard = check(roles.Runtime, roles.Maintenance)
		deleteGuard = "((pg_has_role(session_user, '" + roles.Runtime + "', 'USAGE') AND publication.status = 'aborted') OR (pg_has_role(session_user, '" + roles.Maintenance + "', 'USAGE') AND publication.status IN ('aborted', 'discarding')))"
		for marker, value := range map[string]string{"__OWNER__": roles.Owner, "__MIGRATOR__": roles.Migrator, "__RUNTIME__": roles.Runtime, "__MAINTENANCE__": roles.Maintenance, "__READINESS__": roles.Readiness} {
			text = strings.ReplaceAll(text, marker, dbcore.QuoteIdentifier(value))
		}
	}
	return strings.NewReplacer("__SCHEMA__", dbcore.QuoteIdentifier(name), "__RUNTIME_GUARD__", runtimeGuard, "__MAINTENANCE_GUARD__", maintenanceGuard, "__RUNTIME_MAINTENANCE_GUARD__", combinedGuard, "__DELETE_PUBLICATION_GUARD__", deleteGuard).Replace(text)
}
