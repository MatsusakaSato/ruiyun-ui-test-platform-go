package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"ruiyun-ui-test-platform-go/internal/config"
)

const stopHelperEnv = "RUIYUN_STOP_HELPER"

func TestStopHelperProcess(t *testing.T) {
	if os.Getenv(stopHelperEnv) != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}

func TestRunStateStopTerminatesPipelineAndReportsReason(t *testing.T) {
	previousRoot := config.RootDir
	config.RootDir = t.TempDir()
	t.Cleanup(func() { config.RootDir = previousRoot })

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	appBinary := filepath.Join(t.TempDir(), "ruiyun-stop-test-app-not-running.exe")
	if _, err := config.SaveOverrides(appBinary, "", ""); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestStopHelperProcess$")
	cmd.Env = append(os.Environ(), stopHelperEnv+"=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	state := &RunState{
		proc:        cmd,
		consolePath: filepath.Join(t.TempDir(), "console.log"),
		runID:       "test-stop",
	}
	go state.pump(cmd, stdout, "")

	ok, message := state.Stop()
	if !ok {
		t.Fatalf("Stop() = false, %q", message)
	}
	if message != "已终止测试并关闭被测应用进程" {
		t.Fatalf("Stop() message = %q", message)
	}

	deadline := time.Now().Add(3 * time.Second)
	for state.ProcRunning() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	status := state.Status()
	if status["running"] != false {
		t.Fatalf("status running = %v, want false", status["running"])
	}
	if status["stopped"] != true {
		t.Fatalf("status stopped = %v, want true", status["stopped"])
	}
	if status["termination_reason"] != "用户手动终止测试" {
		t.Fatalf("termination_reason = %v", status["termination_reason"])
	}
}
