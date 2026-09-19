package hidpin_test

import (
	"errors"
	"testing"
	"time"

	"github.com/yukkeorg/hidpin/host-go/hidpin"
	"github.com/yukkeorg/hidpin/host-go/hidpin/hidpintest"
)

func newDevice() (*hidpin.Device, *hidpintest.Fake) {
	fake := hidpintest.New(hidpin.ProtocolVersion)
	return hidpin.NewDevice(fake, "ABCD0123456789EF"), fake
}

func TestInfoIsReadOnceAndVersionChecked(t *testing.T) {
	device, _ := newDevice()
	info, err := device.Info()
	if err != nil || info.BoardName() != "Raspberry Pi Pico" || info.Available != hidpintest.PicoAvailable {
		t.Fatalf("info %+v, %v", info, err)
	}
	var versionErr *hidpin.ProtocolVersionError
	if _, err := hidpin.NewDevice(hidpintest.New(2), "").Info(); !errors.As(err, &versionErr) || versionErr.DeviceVersion != 2 {
		t.Errorf("expected ProtocolVersionError, got %v", err)
	}
}

func TestSetPinConfigAppliesAndVerifies(t *testing.T) {
	device, fake := newDevice()
	report, err := device.UpdatePins(map[int]hidpin.PinSetting{5: hidpin.MonitorPullDown(5), 7: hidpin.Output(true)})
	if err != nil {
		t.Fatal(err)
	}
	if fake.Config[5] != hidpin.MonitorPullDown(5) || fake.Config[7] != hidpin.Output(true) ||
		fake.Config[6] != hidpin.MonitorPullUp(hidpin.DefaultDebounceMS) {
		t.Errorf("applied config wrong: %v %v %v", fake.Config[5], fake.Config[6], fake.Config[7])
	}
	sent := fake.FeatureWrites[len(fake.FeatureWrites)-1]
	if sent[0] != hidpin.ReportIDPinConfig || len(sent) != hidpin.ReportLen || sent[3] == 0 || report.RequestID != sent[3] {
		t.Errorf("sent report %v, request id %d", sent[:4], report.RequestID)
	}
}

func TestSetPinConfigDetectsConflict(t *testing.T) {
	device, fake := newDevice()
	fake.Conflict = true
	var conflict *hidpin.PinConfigConflictError
	if _, err := device.UpdatePins(map[int]hidpin.PinSetting{5: hidpin.Unused()}); !errors.As(err, &conflict) || conflict.Seen == conflict.Expected {
		t.Errorf("expected conflict, got %v", err)
	}
}

func TestSetPinConfigRejectedByDevice(t *testing.T) {
	device, fake := newDevice()
	none := uint32(0)
	fake.ValidateAvailable = &none
	var rejected *hidpin.PinConfigRejectedError
	_, err := device.UpdatePins(map[int]hidpin.PinSetting{5: hidpin.MonitorPullUp(20)})
	if !errors.As(err, &rejected) || rejected.Local || rejected.Result != hidpin.ResultUnavailableGPIO || rejected.GPIO != 0 {
		t.Errorf("expected device-side rejection, got %v", err)
	}
}

func TestLocalValidationHappensBeforeWriting(t *testing.T) {
	device, fake := newDevice()
	var rejected *hidpin.PinConfigRejectedError
	_, err := device.UpdatePins(map[int]hidpin.PinSetting{23: hidpin.MonitorPullUp(20)})
	if !errors.As(err, &rejected) || !rejected.Local || rejected.GPIO != 23 {
		t.Errorf("expected local rejection, got %v", err)
	}
	if len(fake.FeatureWrites) != 0 {
		t.Error("an invalid configuration was sent")
	}
}

func TestAppliedConfigMustMatch(t *testing.T) {
	device, fake := newDevice()
	fake.AfterFeatureWrite = func() { fake.Config[9] = hidpin.Unused() }
	if _, err := device.UpdatePins(map[int]hidpin.PinSetting{5: hidpin.Unused()}); err == nil {
		t.Error("mismatching configuration accepted")
	}
}

func TestReadStatusCountsMissedReports(t *testing.T) {
	device, fake := newDevice()
	for _, seq := range []uint16{10, 14, 15} {
		s := fake.CurrentStatus()
		s.Seq, s.Reason = seq, hidpin.ReasonPeriodic
		fake.Queue(s)
	}
	for i, wantMissed := range []int{0, 3, 3} {
		if _, ok, err := device.ReadStatus(time.Second); !ok || err != nil {
			t.Fatalf("read %d: %v %v", i, ok, err)
		}
		if device.MissedReports() != wantMissed {
			t.Errorf("after read %d missed = %d, want %d", i, device.MissedReports(), wantMissed)
		}
	}
	if _, ok, _ := device.ReadStatus(time.Second); ok {
		t.Error("expected a timeout")
	}
}

func TestSeqWrapsAndHostRequestsAreIgnored(t *testing.T) {
	device, fake := newDevice()
	for _, r := range []struct {
		seq    uint16
		reason hidpin.Reason
	}{{65535, hidpin.ReasonPeriodic}, {0, hidpin.ReasonPeriodic}, {0, hidpin.ReasonHostRequest}, {1, hidpin.ReasonPeriodic}} {
		s := fake.CurrentStatus()
		s.Seq, s.Reason = r.seq, r.reason
		fake.Queue(s)
		device.ReadStatus(time.Second)
	}
	if device.MissedReports() != 0 {
		t.Errorf("missed = %d", device.MissedReports())
	}
}

func TestOnOffFollowsPullDirection(t *testing.T) {
	device, fake := newDevice()
	if _, err := device.UpdatePins(map[int]hidpin.PinSetting{6: hidpin.MonitorPullDown(0)}); err != nil {
		t.Fatal(err)
	}
	config, _ := device.PinConfig()
	s := fake.CurrentStatus()
	s.Levels &^= 1 << 5 // GPIO5 pulled low by a switch
	states := device.OnOff(s, config)
	if !states[5] || states[4] || !states[6] {
		t.Errorf("states 4,5,6 = %v %v %v", states[4], states[5], states[6])
	}
	device.SetPolarity(5, false)
	if device.OnOff(s, config)[5] {
		t.Error("polarity override ignored")
	}
}

func TestSetOutputsEncodesMaskAndValue(t *testing.T) {
	device, fake := newDevice()
	if err := device.SetOutputs(map[int]bool{7: true, 8: false}); err != nil {
		t.Fatal(err)
	}
	written := fake.OutputWrites[0]
	mask, value, err := hidpin.DecodeOutput(written[1:])
	if written[0] != hidpin.ReportIDOutput || err != nil || mask != 1<<7|1<<8 || value != 1<<7 {
		t.Errorf("output report %v", written)
	}
	if err := device.SetOutputs(map[int]bool{30: true}); err == nil {
		t.Error("GPIO30 accepted")
	}
}

func TestRequestStatus(t *testing.T) {
	device, fake := newDevice()
	s, err := device.RequestStatus()
	if err != nil || s.Reason&hidpin.ReasonHostRequest == 0 || s.Monitored != fake.Config.MonitoredMask() {
		t.Errorf("status %+v, %v", s, err)
	}
}

func TestCloseClosesTransport(t *testing.T) {
	device, fake := newDevice()
	device.Close()
	if !fake.Closed {
		t.Error("transport not closed")
	}
}

func TestUSBIDsFromEnvironment(t *testing.T) {
	t.Setenv("HIDPIN_VID", "")
	t.Setenv("HIDPIN_PID", "")
	if vid, pid, err := hidpin.USBIDs(); err != nil || vid != 0x1209 || pid != 0x0001 {
		t.Errorf("defaults %#x %#x %v", vid, pid, err)
	}
	t.Setenv("HIDPIN_PID", "0x6870")
	t.Setenv("HIDPIN_VID", "4660")
	if vid, pid, err := hidpin.USBIDs(); err != nil || vid != 4660 || pid != 0x6870 {
		t.Errorf("overrides %#x %#x %v", vid, pid, err)
	}
	for _, bad := range []string{"zz", "0x10000", "-1"} {
		t.Setenv("HIDPIN_PID", bad)
		if _, _, err := hidpin.USBIDs(); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
