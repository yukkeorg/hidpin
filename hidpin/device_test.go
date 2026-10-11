package hidpin_test

import (
	"errors"
	"testing"
	"time"

	"github.com/yukkeorg/hidpin/hidpin"
	"github.com/yukkeorg/hidpin/hidpin/hidpintest"
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
	if vid, pid, err := hidpin.USBIDs(); err != nil || vid != 0x1209 || pid != 0x6870 {
		t.Errorf("defaults %#x %#x %v", vid, pid, err)
	}
	t.Setenv("HIDPIN_PID", "0x0001")
	t.Setenv("HIDPIN_VID", "4660")
	if vid, pid, err := hidpin.USBIDs(); err != nil || vid != 4660 || pid != 0x0001 {
		t.Errorf("overrides %#x %#x %v", vid, pid, err)
	}
	for _, bad := range []string{"zz", "0x10000", "-1"} {
		t.Setenv("HIDPIN_PID", bad)
		if _, _, err := hidpin.USBIDs(); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// --- error messages (HOST_API.md 8) ---------------------------------------------------

func TestErrorMessagesFollowTheSpecification(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{&hidpin.ProtocolVersionError{DeviceVersion: 2}, "device speaks protocol version 2, this library speaks 1"},
		{&hidpin.PinConfigRejectedError{Result: hidpin.ResultUnavailableGPIO, GPIO: 23, Local: true},
			"the pin configuration is invalid (nothing was sent to the device): the pin is not an available GPIO (GPIO23)"},
		{&hidpin.PinConfigRejectedError{Result: hidpin.ResultBadLength, GPIO: hidpin.ResultGPIONone},
			"the device rejected the pin configuration: the payload is not 63 bytes long"},
		{&hidpin.PinConfigConflictError{Expected: 17, Seen: 42},
			"another process changed the pin configuration (sent request_id 17, read back 42)"},
	} {
		if got := c.err.Error(); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

// --- choosing a device (HOST_API.md 6) ---------------------------------------------------

func entries(serials ...string) []hidpin.Entry {
	var list []hidpin.Entry
	for i, serial := range serials {
		list = append(list, hidpin.Entry{Path: "/dev/hidraw" + string(rune('0'+i)), Serial: serial})
	}
	return list
}

func TestChooseDeviceTakesTheOnlyOneOrTheNamedOne(t *testing.T) {
	if entry, err := hidpin.ChooseDevice(entries("A"), ""); err != nil || entry.Serial != "A" {
		t.Errorf("only one: %+v, %v", entry, err)
	}
	if entry, err := hidpin.ChooseDevice(entries("A", "B"), "B"); err != nil || entry.Serial != "B" {
		t.Errorf("named: %+v, %v", entry, err)
	}
}

func TestChooseDeviceWithoutDevices(t *testing.T) {
	if _, err := hidpin.ChooseDevice(nil, ""); err != hidpin.ErrNotFound {
		t.Errorf("got %v", err)
	}
}

func TestChooseDeviceWithAnUnknownSerialNumber(t *testing.T) {
	_, err := hidpin.ChooseDevice(entries("A", "B"), "C")
	if !errors.Is(err, hidpin.ErrNotFound) || err.Error() != "no hidpin device found: no device with serial number C" {
		t.Errorf("got %v", err)
	}
}

func TestChooseDeviceRefusesToGuess(t *testing.T) {
	_, err := hidpin.ChooseDevice(entries("A", "B"), "")
	if !errors.Is(err, hidpin.ErrSeveralDevices) || err.Error() != "several devices are connected; pick one with --serial: A, B" {
		t.Errorf("got %v", err)
	}
}

// --- reports of the wrong shape --------------------------------------------------------

// misbehaving wraps the fake and replaces what some reads return.
type misbehaving struct {
	*hidpintest.Fake
	feature      func(buf []byte) (int, error)
	input        func(buf []byte) (int, error)
	featureReads []uint8
}

func (m *misbehaving) GetFeatureReport(buf []byte) (int, error) {
	m.featureReads = append(m.featureReads, buf[0])
	if m.feature != nil {
		return m.feature(buf)
	}
	return m.Fake.GetFeatureReport(buf)
}

func (m *misbehaving) GetInputReport(buf []byte) (int, error) {
	if m.input != nil {
		return m.input(buf)
	}
	return m.Fake.GetInputReport(buf)
}

func newMisbehaving() (*hidpin.Device, *misbehaving) {
	m := &misbehaving{Fake: hidpintest.New(hidpin.ProtocolVersion)}
	return hidpin.NewDevice(m, "ABCD0123456789EF"), m
}

func TestShortFeatureReportIsAnError(t *testing.T) {
	device, m := newMisbehaving()
	m.feature = func(buf []byte) (int, error) { return 3, nil }
	if _, err := device.Info(); err == nil || err.Error() != "feature report 2 is only 3 bytes long" {
		t.Errorf("got %v", err)
	}
}

func TestFeatureReportWithAnotherIDIsAnError(t *testing.T) {
	device, m := newMisbehaving()
	m.feature = func(buf []byte) (int, error) {
		n, err := m.Fake.GetFeatureReport(buf)
		buf[0] = 3
		return n, err
	}
	if _, err := device.Info(); err == nil || err.Error() != "asked for feature report 2 but got report 3" {
		t.Errorf("got %v", err)
	}
}

func TestFailedFeatureReadIsWrapped(t *testing.T) {
	device, m := newMisbehaving()
	gone := errors.New("no such device")
	m.feature = func(buf []byte) (int, error) { return 0, gone }
	if _, err := device.GetPinConfig(); !errors.Is(err, gone) {
		t.Errorf("got %v", err)
	}
}

func TestUnexpectedInputReportIsAnError(t *testing.T) {
	device, fake := newDevice()
	fake.Reads = append(fake.Reads, append([]byte{hidpin.ReportIDOutput}, make([]byte, hidpin.PayloadLen)...))
	if _, _, err := device.ReadStatus(time.Millisecond); err == nil || err.Error() != "received an unexpected report ID 4" {
		t.Errorf("got %v", err)
	}
}

func TestShortStatusReportIsAnError(t *testing.T) {
	device, fake := newDevice()
	fake.Reads = append(fake.Reads, []byte{hidpin.ReportIDStatus, 0, 0})
	if _, _, err := device.ReadStatus(time.Millisecond); err == nil || err.Error() != "status report is only 3 bytes long" {
		t.Errorf("got %v", err)
	}
}

func TestShortInputReportOnRequestIsAnError(t *testing.T) {
	device, m := newMisbehaving()
	m.input = func(buf []byte) (int, error) { return 1, nil }
	if _, err := device.RequestStatus(); err == nil || err.Error() != "could not read the status report" {
		t.Errorf("got %v", err)
	}
}

// --- caching and request ids (HOST_API.md 7.2, 7.3) --------------------------------------

func TestPinConfigIsReadOnlyWhenNotCached(t *testing.T) {
	device, m := newMisbehaving()
	for range 2 {
		if config, err := device.PinConfig(); err != nil || config[5] != hidpin.MonitorPullUp(20) {
			t.Fatalf("config %v, %v", config[5], err)
		}
	}
	if len(m.featureReads) != 1 || m.featureReads[0] != hidpin.ReportIDPinConfig {
		t.Errorf("feature reads %v", m.featureReads)
	}
}

func TestRequestIDsStayWithin1To255AndChangeEveryWrite(t *testing.T) {
	device, fake := newDevice()
	for i := range 50 {
		if _, err := device.UpdatePins(map[int]hidpin.PinSetting{5: hidpin.MonitorPullUp(uint8(i))}); err != nil {
			t.Fatal(err)
		}
	}
	previous := uint8(0)
	for _, write := range fake.FeatureWrites {
		id := write[3] // report ID, result, result_gpio, request_id
		if id == 0 || id == previous {
			t.Fatalf("request_id %d after %d", id, previous)
		}
		previous = id
	}
}

func TestUpdatePinsRejectsGPIOsOutOfRange(t *testing.T) {
	device, fake := newDevice()
	if _, err := device.UpdatePins(map[int]hidpin.PinSetting{30: hidpin.Unused()}); err == nil || err.Error() != "GPIO30 is out of range (0-29)" {
		t.Errorf("got %v", err)
	}
	if len(fake.FeatureWrites) != 0 {
		t.Error("something was written")
	}
}
