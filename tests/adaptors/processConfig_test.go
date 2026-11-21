package adaptors

import (
	"testing"

	"houseflowApi/internal/config"
)

// LoadConfig without config.json only works in production, so the tests run
// there from an empty directory and see the environment alone.
func loadEnvironmentConfig(t *testing.T) *config.ConfigModel {
	t.Helper()
	t.Setenv("APP_ENV", "production")
	t.Chdir(t.TempDir())
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestProcessConfigDefaultsToAllRole(t *testing.T) {
	t.Setenv("FAUSCREW_ROLE", "")
	t.Setenv("INSTANCE_ID", "")
	t.Setenv("FLY_MACHINE_ID", "")
	cfg := loadEnvironmentConfig(t)
	if cfg.Process.Role != config.ProcessRoleAll || !cfg.Process.ConsumesJobs() || cfg.Process.InstanceID != "" {
		t.Fatalf("process = %+v", cfg.Process)
	}
}

func TestProcessConfigReadsRoleAndInstanceID(t *testing.T) {
	t.Setenv("FAUSCREW_ROLE", " api ")
	t.Setenv("INSTANCE_ID", "")
	t.Setenv("FLY_MACHINE_ID", "machine-1")
	cfg := loadEnvironmentConfig(t)
	if cfg.Process.Role != config.ProcessRoleAPI || cfg.Process.ConsumesJobs() || cfg.Process.InstanceID != "machine-1" {
		t.Fatalf("process = %+v", cfg.Process)
	}

	t.Setenv("INSTANCE_ID", "instance-1")
	if cfg := loadEnvironmentConfig(t); cfg.Process.InstanceID != "instance-1" {
		t.Fatalf("INSTANCE_ID must win over FLY_MACHINE_ID, got %q", cfg.Process.InstanceID)
	}
}

func TestValidateProcessRejectsUnknownRole(t *testing.T) {
	for _, role := range []string{"", config.ProcessRoleAll, config.ProcessRoleAPI} {
		if err := config.ValidateProcess(config.ConfigProcess{Role: role}); err != nil {
			t.Errorf("role %q rejected: %v", role, err)
		}
	}
	// The worker command sets this role; FAUSCREW_ROLE=api stays valid for
	// the API group in the same environment.
	if err := config.ValidateProcess(config.ConfigProcess{Role: config.ProcessRoleWorker}); err == nil {
		t.Error("FAUSCREW_ROLE=worker accepted")
	}
}

func TestOnlyTheAPIRoleLeavesJobsToOthers(t *testing.T) {
	for role, consumes := range map[string]bool{
		"":                       true,
		config.ProcessRoleAll:    true,
		config.ProcessRoleWorker: true,
		config.ProcessRoleAPI:    false,
	} {
		if got := (config.ConfigProcess{Role: role}).ConsumesJobs(); got != consumes {
			t.Errorf("role %q consumes jobs = %v, want %v", role, got, consumes)
		}
	}
}
