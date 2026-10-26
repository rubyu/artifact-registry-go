package tooling

import (
	"context"
	"encoding/json"
	"io"

	registry "github.com/rubyu/artifact-registry-go"
	"github.com/rubyu/artifact-registry-go/dbcore"
	"github.com/rubyu/artifact-registry-go/internal/buildinfo"
	"github.com/rubyu/artifact-registry-go/schema"
)

func Run(ctx context.Context, workload string, args []string, out io.Writer) int {
	encoder := json.NewEncoder(out)
	if len(args) == 1 && (args[0] == "--version" || args[0] == "version") {
		if encoder.Encode(buildinfo.Current()) != nil {
			return 1
		}
		return 0
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		io.WriteString(out, "artifact-registry-"+workload+" [--version]\n設定は ARTIFACT_REGISTRY_* 環境変数から取得します。\n")
		return 0
	}
	if len(args) != 0 {
		encoder.Encode(map[string]string{"status": "error", "error": "unexpected command arguments"})
		return 1
	}
	var result any
	var err error
	exit := 0
	if workload == "migrate" {
		return dbcore.RunMigrationCLI(out, func() ([]dbcore.MigrationStepResult, error) {
			var name string
			var connection dbcore.Config
			var roles schema.Roles
			var target dbcore.MigrationTarget
			name, err = SchemaName()
			if err == nil {
				connection, err = LoadDB("migration")
			}
			if err == nil {
				roles, err = LoadRoles()
			}
			if err == nil {
				target, err = schema.BuildTarget(connection, name, &roles, false)
			}
			if err == nil {
				step, migrationErr := dbcore.Migrate(ctx, target)
				return []dbcore.MigrationStepResult{step}, migrationErr
			}
			return nil, err
		})
	} else {
		var config registry.MaintenanceConfig
		var maintenance *registry.Maintenance
		var summary registry.MaintenanceSummary
		config, err = LoadMaintenance()
		if err == nil {
			maintenance, err = registry.NewMaintenance(ctx, config)
		}
		if err == nil {
			summary, err = maintenance.RunCleanupCycle(ctx)
			closeErr := maintenance.Close()
			if err == nil {
				err = closeErr
			}
		}
		result = struct {
			registry.MaintenanceSummary
			Component  string `json:"component"`
			SchemaName string `json:"schema_name"`
		}{summary, "artifact-registry-standalone", config.Runtime.Schema}
		if summary.DeleteFailedCount > 0 {
			exit = 2
		}
	}
	if err != nil {
		result = map[string]string{"status": "error", "component": "artifact-registry", "error": err.Error()}
		exit = 1
	}
	if encoder.Encode(result) != nil {
		return 1
	}
	return exit
}
