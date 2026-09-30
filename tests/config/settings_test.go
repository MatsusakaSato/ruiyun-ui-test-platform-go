package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ruiyun-ui-test-platform-go/internal/config"
)

func isolateSettings(t *testing.T) {
	t.Helper()

	previousRoot := config.RootDir
	config.RootDir = t.TempDir()
	t.Cleanup(func() { config.RootDir = previousRoot })

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
}

func TestOverridesPersistAcrossRootDirChanges(t *testing.T) {
	isolateSettings(t)

	const appBinary = "/Applications/Test App.app/Contents/MacOS/Test App"
	if _, err := config.SaveOverrides(appBinary, "", ""); err != nil {
		t.Fatalf("SaveOverrides() error = %v", err)
	}

	config.RootDir = t.TempDir()
	got := config.LoadOverrides()
	if got.AppBinary != appBinary {
		t.Fatalf("LoadOverrides().AppBinary = %q, want %q", got.AppBinary, appBinary)
	}
}

func TestLoadOverridesMigratesLegacySettings(t *testing.T) {
	isolateSettings(t)

	want := config.AppOverrides{AppBinary: `C:\Program Files\Test\test.exe`}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(config.RootDir, ".app_settings.json")
	if err := os.WriteFile(legacyPath, data, 0600); err != nil {
		t.Fatal(err)
	}

	got := config.LoadOverrides()
	if got.AppBinary != want.AppBinary {
		t.Fatalf("LoadOverrides().AppBinary = %q, want %q", got.AppBinary, want.AppBinary)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy settings file still exists or could not be checked: %v", err)
	}

	config.RootDir = t.TempDir()
	got = config.LoadOverrides()
	if got.AppBinary != want.AppBinary {
		t.Fatalf("LoadOverrides() after migration = %q, want %q", got.AppBinary, want.AppBinary)
	}
}
