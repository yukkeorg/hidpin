package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yukkeorg/hidpin/host-go/hidpin"
	"github.com/yukkeorg/hidpin/host-go/hidpin/hidpintest"
)

func TestParsePinSpec(t *testing.T) {
	good := map[string]struct {
		gpio    int
		setting hidpin.PinSetting
	}{
		"5=pullup:20": {5, hidpin.MonitorPullUp(20)},
		"6=off":       {6, hidpin.Unused()},
		"7=out:low":   {7, hidpin.Output(false)},
		"8=out:high":  {8, hidpin.Output(true)},
		"9=pulldown":  {9, hidpin.MonitorPullDown(hidpin.DefaultDebounceMS)},
		"10=nopull:0": {10, hidpin.MonitorNoPull(0)},
		"11=in:255":   {11, hidpin.MonitorNoPull(255)},
	}
	for text, want := range good {
		gpio, setting, err := parsePinSpec(text)
		if err != nil || gpio != want.gpio || setting != want.setting {
			t.Errorf("%s -> %d %v %v", text, gpio, setting, err)
		}
	}
	for _, bad := range []string{"5", "5=", "x=off", "30=off", "5=bogus", "5=pullup:abc", "5=pullup:256", "5=off:1", "5=out:maybe"} {
		if _, _, err := parsePinSpec(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestParseOutputSpec(t *testing.T) {
	if gpio, level, err := parseOutputSpec("7=high"); err != nil || gpio != 7 || !level {
		t.Errorf("7=high -> %d %v %v", gpio, level, err)
	}
	if gpio, level, err := parseOutputSpec("7=0"); err != nil || gpio != 7 || level {
		t.Errorf("7=0 -> %d %v %v", gpio, level, err)
	}
	for _, bad := range []string{"7", "7=maybe", "31=1"} {
		if _, _, err := parseOutputSpec(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestParseOptionsAllowsFlagsAnywhere(t *testing.T) {
	o, err := parseOptions([]string{"config", "set", "5=off", "--serial", "ABC", "--json", "6=off"})
	if err != nil || o.serial != "ABC" || !o.json || strings.Join(o.args, " ") != "config set 5=off 6=off" {
		t.Errorf("%+v %v", o, err)
	}
	if _, err := parseOptions([]string{"info", "--serial"}); err == nil {
		t.Error("missing value accepted")
	}
	if _, err := parseOptions([]string{"info", "--bogus"}); err == nil {
		t.Error("unknown flag accepted")
	}
}

// withFake points the CLI at a fake device for the duration of a test.
func withFake(t *testing.T) *hidpintest.Fake {
	t.Helper()
	fake := hidpintest.New(hidpin.ProtocolVersion)
	savedOpen, savedFind := openDevice, findDevices
	openDevice = func(serial string) (*hidpin.Device, error) {
		device := hidpin.NewDevice(fake, "ABCD0123456789EF")
		_, err := device.Info()
		return device, err
	}
	findDevices = func() ([]hidpin.Entry, error) {
		return []hidpin.Entry{{Path: "/dev/hidraw0", Serial: "ABCD0123456789EF", Manufacturer: "yukke.org", Product: "hidpin"}}, nil
	}
	t.Cleanup(func() { openDevice, findDevices = savedOpen, savedFind })
	return fake
}

func runCLI(ctx context.Context, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(ctx, args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestListCommand(t *testing.T) {
	withFake(t)
	code, out, _ := runCLI(context.Background(), "list")
	if code != 0 || out != "ABCD0123456789EF  yukke.org hidpin\n" {
		t.Errorf("%d %q", code, out)
	}
}

func TestInfoCommand(t *testing.T) {
	withFake(t)
	code, out, _ := runCLI(context.Background(), "info")
	if code != 0 || !strings.Contains(out, "Raspberry Pi Pico") || !strings.Contains(out, "26 pins") {
		t.Errorf("%d %q", code, out)
	}
	code, out, _ = runCLI(context.Background(), "--json", "info")
	var payload map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &payload) != nil || payload["board_name"] != "Raspberry Pi Pico" ||
		payload["firmware"] != "0.1.0" || len(payload["available_gpios"].([]any)) != 26 {
		t.Errorf("%d %q", code, out)
	}
}

func TestConfigSetCommand(t *testing.T) {
	fake := withFake(t)
	code, out, _ := runCLI(context.Background(), "config", "set", "5=pulldown:5", "7=out:high")
	if code != 0 || fake.Config[5] != hidpin.MonitorPullDown(5) || fake.Config[7] != hidpin.Output(true) {
		t.Fatalf("%d %q", code, out)
	}
	if !strings.Contains(out, "GPIO5 is now pulldown:5") {
		t.Errorf("output %q", out)
	}
}

func TestConfigSetRejectsUnavailableGPIO(t *testing.T) {
	withFake(t)
	code, _, errOut := runCLI(context.Background(), "config", "set", "23=pullup:20")
	if code != 1 || !strings.Contains(errOut, "GPIO23") {
		t.Errorf("%d %q", code, errOut)
	}
}

func TestUsageErrorsExitWithTwo(t *testing.T) {
	withFake(t)
	for _, args := range [][]string{{}, {"bogus"}, {"config"}, {"config", "set", "5=bogus"}, {"output", "7=maybe"}} {
		if code, _, _ := runCLI(context.Background(), args...); code != 2 {
			t.Errorf("%v exited with %d", args, code)
		}
	}
}

func TestNoCommandPrintsHelpAndError(t *testing.T) {
	for _, args := range [][]string{{}, {"--json"}, {"config"}} {
		code, out, errOut := runCLI(context.Background(), args...)
		if code != 2 || out != "" {
			t.Errorf("%v: exit %d, stdout %q", args, code, out)
		}
		if !strings.HasPrefix(errOut, "usage: hidpin") || !strings.Contains(errOut, "commands:") {
			t.Errorf("%v: help missing from %q", args, errOut)
		}
		if lines := strings.Split(strings.TrimSpace(errOut), "\n"); !strings.HasPrefix(lines[len(lines)-1], "hidpin: error: ") ||
			!strings.Contains(lines[len(lines)-1], "the following arguments are required") {
			t.Errorf("%v: error line missing from %q", args, errOut)
		}
	}
}

func TestOtherUsageErrorsPrintOnlyTheError(t *testing.T) {
	code, _, errOut := runCLI(context.Background(), "bogus")
	if code != 2 || strings.Contains(errOut, "commands:") || !strings.HasPrefix(errOut, "hidpin: error: invalid choice") {
		t.Errorf("%d %q", code, errOut)
	}
}

func TestConfigGetCommand(t *testing.T) {
	withFake(t)
	code, out, _ := runCLI(context.Background(), "config", "get")
	if code != 0 || !strings.Contains(out, "GPIO0  pullup:20") || strings.Contains(out, "GPIO23") {
		t.Errorf("%d %q", code, out)
	}
	code, out, _ = runCLI(context.Background(), "config", "get", "--json")
	if code != 0 || !strings.Contains(out, `"pins":{"0":"pullup:20","1":"pullup:20"`) {
		t.Errorf("json keys must be in GPIO order: %q", out)
	}
}

func TestOutputCommandWarnsAboutNonOutputPins(t *testing.T) {
	fake := withFake(t)
	runCLI(context.Background(), "config", "set", "7=out:low")
	code, _, errOut := runCLI(context.Background(), "output", "7=high", "8=high")
	if code != 0 || !strings.Contains(errOut, "GPIO8 is not an output pin") {
		t.Errorf("%d %q", code, errOut)
	}
	mask, value, err := hidpin.DecodeOutput(fake.OutputWrites[len(fake.OutputWrites)-1][1:])
	if err != nil || mask != 1<<7|1<<8 || value != mask {
		t.Errorf("mask %#x value %#x %v", mask, value, err)
	}
}

func TestWatchPrintsEventsUntilCancelled(t *testing.T) {
	fake := withFake(t)
	s := fake.CurrentStatus()
	s.Seq, s.Reason = 1, hidpin.ReasonLevelChanged
	s.Levels &^= 1 << 5
	s.Events = []hidpin.EdgeEvent{{GPIO: 5, Level: false, AgeUS: 20_000}}
	fake.Queue(s)
	ctx, cancel := context.WithCancel(context.Background())
	fake.OnIdle = cancel
	code, out, _ := runCLI(ctx, "watch")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(lines) != 2 || !strings.Contains(lines[0], "GPIO5=OFF") || !strings.Contains(lines[1], "GPIO5  ON  (LOW)") {
		t.Errorf("%d %q", code, out)
	}
}
