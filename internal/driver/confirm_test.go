package driver

import (
	"path/filepath"
	"strings"
	"testing"

	"ruiyun-ui-test-platform-go/internal/cdp"
)

func TestAutoConfirmScriptDefaultsNilKeywordsToArray(t *testing.T) {
	script := autoConfirmScript(nil)
	if !strings.Contains(script, "const prefer = [] || [];") {
		t.Fatalf("nil confirm keywords were not rendered as an array: %s", script[:min(len(script), 120)])
	}
	if strings.Contains(script, "const prefer = null") {
		t.Fatal("nil confirm keywords were rendered as null")
	}
}

func TestConfirmCDPFailuresDoNotRequireManualCardHandling(t *testing.T) {
	d := &RuiyunUIDriver{AutoConfirm: true, CurrentSess: "sess-1"}
	for range 5 {
		d.confirmFail("CDP unavailable")
	}

	if d.AutoConfirm {
		t.Fatal("AutoConfirm remains enabled after repeated CDP failures")
	}
	if d.ConfirmHumanNeeded {
		t.Fatal("CDP failures without a detected card must not require manual card handling")
	}
	if d.ConfirmHumanSess != "" {
		t.Fatalf("ConfirmHumanSess = %q, want empty", d.ConfirmHumanSess)
	}
}

func TestConfirmClickLimitDoesNotRequireManualCardHandling(t *testing.T) {
	d := &RuiyunUIDriver{
		AutoConfirm:      true,
		ConfirmMaxClicks: 1,
		ConfirmEvents:    []map[string]any{{"mode": "keyword"}},
		CurrentSess:      "sess-1",
		CDP:              new(cdp.CDPSession),
	}

	d.maybeAutoConfirm(false, "sess-1")

	if d.AutoConfirm {
		t.Fatal("AutoConfirm remains enabled after reaching its click limit")
	}
	if d.ConfirmHumanNeeded {
		t.Fatal("click limit without a detected card must not require manual card handling")
	}
	if d.ConfirmHumanSess != "" {
		t.Fatalf("ConfirmHumanSess = %q, want empty", d.ConfirmHumanSess)
	}
}

func TestQCardStopStillRequiresManualHandling(t *testing.T) {
	d := &RuiyunUIDriver{AutoConfirm: true, CurrentSess: "sess-1"}
	d.qcardStop("card did not advance")

	if !d.ConfirmHumanNeeded {
		t.Fatal("a detected card that cannot advance must require manual handling")
	}
	if d.ConfirmHumanSess != "sess-1" {
		t.Fatalf("ConfirmHumanSess = %q, want sess-1", d.ConfirmHumanSess)
	}
}

func TestWaitSomeSettledReturnsWhenAppHasExited(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ruiyun-missing-"+filepath.Base(t.TempDir())+".exe")
	d := &RuiyunUIDriver{Binary: binary}

	settled := d.WaitSomeSettled([]string{t.TempDir()}, 10, 0.5, nil, 0)
	if len(settled) != 0 {
		t.Fatalf("WaitSomeSettled() = %v, want no settled sessions", settled)
	}
	if !d.AppExited {
		t.Fatal("WaitSomeSettled() did not record that the app exited")
	}
}
