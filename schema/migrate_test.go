package schema

import (
	"github.com/rubyu/artifact-registry-go/dbcore"
	"strings"
	"testing"
)

func TestMigrationRendering(t *testing.T) {
	roles := Roles{"owner", "migrator", "runtime", "maintenance", "readiness"}
	target, err := BuildTarget(dbcore.Config{}, "select", &roles, false)
	if err != nil || len(target.Migrations) != 2 || target.MigrationRole != "owner" {
		t.Fatal(target, err)
	}
	for _, file := range []string{"00001_tables.sql", "00002_lifecycle.sql", "ownership.sql", "privileges.sql"} {
		raw, _ := migrations.ReadFile("migrations/" + file)
		sql := render(string(raw), "select", &roles)
		if strings.Contains(sql, "__") {
			t.Fatalf("unresolved marker in %s", file)
		}
		if strings.Contains(sql, "alembic_version") {
			t.Fatal("legacy marker")
		}
		if !strings.Contains(sql, `"select"`) {
			t.Fatal("schema was not quoted")
		}
	}
	roles.Readiness = roles.Owner
	if _, err = BuildTarget(dbcore.Config{}, "test", &roles, false); err == nil {
		t.Fatal("duplicate roles")
	}
}
