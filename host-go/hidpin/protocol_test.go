package hidpin

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"testing"
)

// number accepts both JSON numbers and "0x..." strings, as protocol/vectors.json uses both.
type number uint64

func (n *number) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		value, err := strconv.ParseUint(text, 0, 64)
		*n = number(value)
		return err
	}
	var value uint64
	err := json.Unmarshal(data, &value)
	*n = number(value)
	return err
}

type vectorFile struct {
	ReportDescriptor string `json:"report_descriptor"`
	Status           []struct {
		Name        string `json:"name"`
		Seq         number `json:"seq"`
		Reason      number `json:"reason"`
		Flags       number `json:"flags"`
		Monitored   number `json:"monitored"`
		Outputs     number `json:"outputs"`
		Levels      number `json:"levels"`
		TimestampUS number `json:"timestamp_us"`
		Events      []struct {
			GPIO  number `json:"gpio"`
			Level number `json:"level"`
			AgeUS number `json:"age_us"`
		} `json:"events"`
		Payload string `json:"payload"`
	} `json:"status"`
	DeviceInfo []struct {
		Name      string `json:"name"`
		FwMajor   number `json:"fw_major"`
		FwMinor   number `json:"fw_minor"`
		FwPatch   number `json:"fw_patch"`
		Board     number `json:"board"`
		Available number `json:"available"`
		Payload   string `json:"payload"`
	} `json:"device_info"`
	PinConfig []struct {
		Name       string   `json:"name"`
		Result     number   `json:"result"`
		ResultGPIO number   `json:"result_gpio"`
		RequestID  number   `json:"request_id"`
		Mode       []number `json:"mode"`
		Param      []number `json:"param"`
		Payload    string   `json:"payload"`
	} `json:"pin_config"`
	PinConfigSet []struct {
		Name       string `json:"name"`
		Available  number `json:"available"`
		Payload    string `json:"payload"`
		Result     number `json:"result"`
		ResultGPIO number `json:"result_gpio"`
		RequestID  number `json:"request_id"`
	} `json:"pin_config_set"`
	Output []struct {
		Name    string `json:"name"`
		Payload string `json:"payload"`
		Valid   bool   `json:"valid"`
		Mask    number `json:"mask"`
		Value   number `json:"value"`
	} `json:"output"`
}

func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	data, err := os.ReadFile("../../protocol/vectors.json")
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var v vectorFile
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	return v
}

func unhex(t *testing.T, text string) []byte {
	t.Helper()
	data, err := hex.DecodeString(text)
	if err != nil {
		t.Fatalf("bad hex %q: %v", text, err)
	}
	return data
}

func TestStatusVectors(t *testing.T) {
	for _, c := range loadVectors(t).Status {
		payload := unhex(t, c.Payload)
		s, err := DecodeStatus(payload)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		want := Status{
			Seq: uint16(c.Seq), Reason: Reason(c.Reason), Flags: StatusFlags(c.Flags),
			Monitored: uint32(c.Monitored), Outputs: uint32(c.Outputs), Levels: uint32(c.Levels),
			TimestampUS: uint64(c.TimestampUS), Events: []EdgeEvent{},
		}
		for _, e := range c.Events {
			want.Events = append(want.Events, EdgeEvent{GPIO: uint8(e.GPIO), Level: e.Level != 0, AgeUS: uint32(e.AgeUS)})
		}
		if !reflect.DeepEqual(s, want) {
			t.Errorf("%s: decoded %+v, want %+v", c.Name, s, want)
		}
		encoded, err := s.Encode()
		if err != nil || !bytes.Equal(encoded, payload) {
			t.Errorf("%s: re-encoding differs (%v)", c.Name, err)
		}
	}
}

func TestEventStartTime(t *testing.T) {
	for _, c := range loadVectors(t).Status {
		s, _ := DecodeStatus(unhex(t, c.Payload))
		for _, e := range s.Events {
			start, ok := e.StartUS(s.TimestampUS)
			if e.Saturated() {
				if ok {
					t.Errorf("%s: saturated event must have no start time", c.Name)
				}
				continue
			}
			if !ok || start != s.TimestampUS-uint64(e.AgeUS) {
				t.Errorf("%s: start %d, %v", c.Name, start, ok)
			}
		}
	}
}

func TestDeviceInfoVectors(t *testing.T) {
	boards := []string{}
	for _, c := range loadVectors(t).DeviceInfo {
		payload := unhex(t, c.Payload)
		info, err := DecodeDeviceInfo(payload)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if info.ProtocolVersion != ProtocolVersion || info.Firmware != [3]uint8{uint8(c.FwMajor), uint8(c.FwMinor), uint8(c.FwPatch)} ||
			info.Board != Board(c.Board) || info.Available != uint32(c.Available) || info.GPIOCount != GPIOCount ||
			info.EventsPerReport != EventsPerReport {
			t.Errorf("%s: decoded %+v", c.Name, info)
		}
		if !bytes.Equal(info.Encode(), payload) {
			t.Errorf("%s: re-encoding differs", c.Name)
		}
		boards = append(boards, info.BoardName())
	}
	if !reflect.DeepEqual(boards, []string{"Raspberry Pi Pico", "Adafruit QT Py RP2040"}) {
		t.Errorf("board names %v", boards)
	}
}

func TestPinConfigVectors(t *testing.T) {
	for _, c := range loadVectors(t).PinConfig {
		payload := unhex(t, c.Payload)
		r, err := DecodePinConfig(payload)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if r.Result != Result(c.Result) || r.ResultGPIO != uint8(c.ResultGPIO) || r.RequestID != uint8(c.RequestID) {
			t.Errorf("%s: header %+v", c.Name, r)
		}
		for gpio := range GPIOCount {
			if r.Config[gpio] != (PinSetting{PinMode(c.Mode[gpio]), uint8(c.Param[gpio])}) {
				t.Errorf("%s: GPIO%d = %+v", c.Name, gpio, r.Config[gpio])
			}
		}
		if !bytes.Equal(r.Encode(), payload) {
			t.Errorf("%s: re-encoding differs", c.Name)
		}
	}
}

func TestDefaultPinConfigMatchesVectors(t *testing.T) {
	v := loadVectors(t)
	for name, available := range map[string]uint32{"pico_default": 0x1C7FFFFF, "qtpy_rp2040_default": 0x3FD00078} {
		for _, c := range v.PinConfig {
			if c.Name != name {
				continue
			}
			config := DefaultPinConfig(available)
			for gpio := range GPIOCount {
				if config[gpio] != (PinSetting{PinMode(c.Mode[gpio]), uint8(c.Param[gpio])}) {
					t.Errorf("%s: GPIO%d = %+v", name, gpio, config[gpio])
				}
			}
		}
	}
}

func TestPinConfigSetValidationVectors(t *testing.T) {
	for _, c := range loadVectors(t).PinConfigSet {
		result, gpio, requestID := ValidatePinConfigPayload(unhex(t, c.Payload), uint32(c.Available))
		if result != Result(c.Result) || gpio != uint8(c.ResultGPIO) || requestID != uint8(c.RequestID) {
			t.Errorf("%s: got (%d, %d, %d), want (%d, %d, %d)", c.Name, result, gpio, requestID, c.Result, c.ResultGPIO, c.RequestID)
		}
	}
}

func TestEncodePinConfigSetRoundTrip(t *testing.T) {
	for _, c := range loadVectors(t).PinConfigSet {
		if c.Name != "valid_mixed" {
			continue
		}
		payload := unhex(t, c.Payload)
		r, err := DecodePinConfig(payload)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := EncodePinConfigSet(r.Config, uint8(c.RequestID))
		if err != nil || !bytes.Equal(encoded, payload) {
			t.Errorf("round trip differs (%v)", err)
		}
	}
}

func TestDecodeRejectsBadPayloads(t *testing.T) {
	if _, err := DecodeStatus(make([]byte, 62)); err == nil {
		t.Error("short status accepted")
	}
	if _, err := DecodeDeviceInfo(make([]byte, 64)); err == nil {
		t.Error("long device info accepted")
	}
	if _, err := DecodePinConfig(make([]byte, 62)); err == nil {
		t.Error("short pin config accepted")
	}
	badMode := make([]byte, PayloadLen)
	badMode[3] = 9
	if _, err := DecodePinConfig(badMode); err == nil {
		t.Error("undefined mode accepted")
	}
	if _, err := EncodePinConfigSet(DefaultPinConfig(0), 0); err == nil {
		t.Error("request_id 0 accepted")
	}
	if _, err := (Status{Events: make([]EdgeEvent, 8)}).Encode(); err == nil {
		t.Error("8 events accepted")
	}
}

func TestOutputVectors(t *testing.T) {
	for _, c := range loadVectors(t).Output {
		payload := unhex(t, c.Payload)
		mask, value, err := DecodeOutput(payload)
		if !c.Valid {
			if err == nil {
				t.Errorf("%s: invalid payload accepted", c.Name)
			}
			continue
		}
		if err != nil || mask != uint32(c.Mask) || value != uint32(c.Value) {
			t.Errorf("%s: got (%#x, %#x, %v)", c.Name, mask, value, err)
		}
		if !bytes.Equal(EncodeOutput(mask, value), payload) {
			t.Errorf("%s: re-encoding differs", c.Name)
		}
	}
}

func TestPinSettingText(t *testing.T) {
	cases := map[string]PinSetting{
		"off":        Unused(),
		"pullup:20":  MonitorPullUp(20),
		"pulldown:5": MonitorPullDown(5),
		"nopull:0":   MonitorNoPull(0),
		"out:high":   Output(true),
		"out:low":    Output(false),
	}
	for want, setting := range cases {
		if setting.String() != want {
			t.Errorf("%+v -> %q, want %q", setting, setting.String(), want)
		}
	}
}

func TestReasonAndFlagNames(t *testing.T) {
	if got := (ReasonLevelChanged | ReasonPeriodic).String(); got != "LEVEL_CHANGED|PERIODIC" {
		t.Errorf("reason %q", got)
	}
	if got := (FlagOverflow | FlagMoreEvents).Names(); !reflect.DeepEqual(got, []string{"OVERFLOW", "MORE_EVENTS"}) {
		t.Errorf("flags %v", got)
	}
}
