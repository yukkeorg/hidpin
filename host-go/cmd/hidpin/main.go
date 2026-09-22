// Command hidpin watches and configures hidpin devices. It is the Go counterpart of the Python
// CLI in host/ and takes the same commands and options; unlike it, watch keeps going when the
// device is unplugged and plugged in again:
//
//	hidpin [--json] list
//	hidpin [--json] info [--serial S]
//	hidpin [--json] watch [--serial S] [--active-low 5,6] [--active-high 7] [--all]
//	hidpin [--json] config get [--serial S]
//	hidpin [--json] config set [--serial S] GPIO=SETTING...
//	hidpin [--json] output [--serial S] GPIO=VALUE...
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yukkeorg/hidpin/host-go/hidpin"
)

// openDevice, findDevices, watchBus and scanInterval are variables so tests can replace the
// hardware.
var (
	openDevice   = hidpin.Open
	findDevices  = hidpin.FindDevices
	watchBus     = hidpin.SystemBus()
	scanInterval time.Duration // zero: hidpin.DefaultScanInterval
)

const usage = `usage: hidpin [--json] {list,info,watch,config,output} ...

watch and configure a hidpin device

commands:
  list                        list the connected devices
  info                        show the device information
  watch                       keep printing status reports
  config get                  show the current pin configuration
  config set GPIO=SETTING...  change the pin configuration
                              (for example: 5=pullup:20 6=off 7=out:low; pins not named keep their setting)
  output GPIO=VALUE...        change the value of output pins (for example: 7=high 8=0)

options:
  --json               print the result as JSON
  --serial SERIAL      serial number of the device (optional when only one is connected)
  --active-low LIST    watch: GPIOs where LOW means ON (comma separated)
  --active-high LIST   watch: GPIOs where HIGH means ON (comma separated)
  --all                watch: also print reports that carry no events
`

type options struct {
	json       bool
	serial     string
	activeLow  string
	activeHigh string
	all        bool
	help       bool
	args       []string
}

// usageError is reported with exit status 2, like argparse. With showHelp set, the full help
// is printed before the error (used when no command is given).
type usageError struct {
	message  string
	showHelp bool
}

func (e usageError) Error() string { return e.message }

func parseOptions(argv []string) (options, error) {
	var o options
	valueFlags := map[string]*string{"--serial": &o.serial, "--active-low": &o.activeLow, "--active-high": &o.activeHigh}
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			o.args = append(o.args, argv[i+1:]...)
			break
		}
		name, value, hasValue := strings.Cut(arg, "=")
		if target, ok := valueFlags[name]; ok && strings.HasPrefix(arg, "--") {
			if !hasValue {
				if i+1 >= len(argv) {
					return o, usageError{message: fmt.Sprintf("argument %s: expected one argument", name)}
				}
				i++
				value = argv[i]
			}
			*target = value
			continue
		}
		switch arg {
		case "--json":
			o.json = true
		case "--all":
			o.all = true
		case "-h", "--help":
			o.help = true
		default:
			if strings.HasPrefix(arg, "--") {
				return o, usageError{message: fmt.Sprintf("unrecognized arguments: %s", arg)}
			}
			o.args = append(o.args, arg)
		}
	}
	return o, nil
}

var inputModes = map[string]func(uint8) hidpin.PinSetting{
	"nopull":   hidpin.MonitorNoPull,
	"in":       hidpin.MonitorNoPull,
	"pullup":   hidpin.MonitorPullUp,
	"pulldown": hidpin.MonitorPullDown,
}

var levelWords = map[string]bool{"high": true, "1": true, "on": true, "low": false, "0": false, "off": false}

func parseGPIO(text string) (int, error) {
	gpio, err := strconv.ParseInt(text, 0, 64)
	if err != nil {
		return 0, fmt.Errorf("the GPIO number is not a number: '%s'", text)
	}
	if gpio < 0 || gpio >= hidpin.GPIOCount {
		return 0, fmt.Errorf("GPIO%d is out of range (0-%d)", gpio, hidpin.GPIOCount-1)
	}
	return int(gpio), nil
}

// parsePinSpec parses "5=pullup:20", "6=off" or "7=out:low".
func parsePinSpec(text string) (int, hidpin.PinSetting, error) {
	gpioText, settingText, ok := strings.Cut(text, "=")
	if !ok {
		return 0, hidpin.PinSetting{}, fmt.Errorf("'%s' is not of the form GPIO=SETTING", text)
	}
	gpio, err := parseGPIO(gpioText)
	if err != nil {
		return 0, hidpin.PinSetting{}, err
	}
	kind, param, hasParam := strings.Cut(settingText, ":")
	kind = strings.ToLower(kind)
	switch {
	case kind == "off" || kind == "unused":
		if hasParam {
			return 0, hidpin.PinSetting{}, fmt.Errorf("'%s': off takes no value", text)
		}
		return gpio, hidpin.Unused(), nil
	case inputModes[kind] != nil:
		debounce := int64(hidpin.DefaultDebounceMS)
		if hasParam {
			if debounce, err = strconv.ParseInt(param, 0, 64); err != nil {
				return 0, hidpin.PinSetting{}, fmt.Errorf("'%s': the debounce time is not a number", text)
			}
		}
		if debounce < 0 || debounce > 255 {
			return 0, hidpin.PinSetting{}, fmt.Errorf("'%s': the debounce time must be 0-255 ms", text)
		}
		return gpio, inputModes[kind](uint8(debounce)), nil
	case kind == "out":
		level, known := levelWords[strings.ToLower(param)]
		if !known {
			return 0, hidpin.PinSetting{}, fmt.Errorf("'%s': an output is out:high or out:low", text)
		}
		return gpio, hidpin.Output(level), nil
	}
	return 0, hidpin.PinSetting{}, fmt.Errorf("'%s': the setting must be one of off, nopull, pullup, pulldown, out", text)
}

// parseOutputSpec parses "7=high" or "7=0".
func parseOutputSpec(text string) (int, bool, error) {
	gpioText, levelText, ok := strings.Cut(text, "=")
	if !ok {
		return 0, false, fmt.Errorf("'%s' is not of the form GPIO=VALUE", text)
	}
	gpio, err := parseGPIO(gpioText)
	if err != nil {
		return 0, false, err
	}
	level, known := levelWords[strings.ToLower(levelText)]
	if !known {
		return 0, false, fmt.Errorf("'%s': the value must be high/low or 1/0", text)
	}
	return gpio, level, nil
}

func parseGPIOList(text string) ([]int, error) {
	var gpios []int
	for _, part := range strings.Split(text, ",") {
		if part == "" {
			continue
		}
		gpio, err := parseGPIO(part)
		if err != nil {
			return nil, err
		}
		gpios = append(gpios, gpio)
	}
	return gpios, nil
}

// gpioMap marshals as a JSON object whose keys are GPIO numbers in ascending order.
type gpioMap[V any] map[int]V

func (m gpioMap[V]) MarshalJSON() ([]byte, error) {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		value, err := json.Marshal(m[k])
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "\"%d\": %s", k, value)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

type app struct {
	opts   options
	stdout io.Writer
	stderr io.Writer
}

func (a *app) printJSON(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, string(data))
	return nil
}

func (a *app) cmdList() error {
	entries, err := findDevices()
	if err != nil {
		return err
	}
	if a.opts.json {
		type item struct {
			Serial       string `json:"serial"`
			Manufacturer string `json:"manufacturer"`
			Product      string `json:"product"`
		}
		items := []item{}
		for _, e := range entries {
			items = append(items, item{e.Serial, e.Manufacturer, e.Product})
		}
		return a.printJSON(items)
	}
	if len(entries) == 0 {
		fmt.Fprintln(a.stdout, "no hidpin device found")
		return nil
	}
	for _, e := range entries {
		fmt.Fprintf(a.stdout, "%s  %s %s\n", e.Serial, e.Manufacturer, e.Product)
	}
	return nil
}

func (a *app) cmdInfo(device *hidpin.Device) error {
	info, err := device.Info()
	if err != nil {
		return err
	}
	if a.opts.json {
		return a.printJSON(struct {
			Serial             string `json:"serial"`
			ProtocolVersion    uint8  `json:"protocol_version"`
			Firmware           string `json:"firmware"`
			Board              uint8  `json:"board"`
			BoardName          string `json:"board_name"`
			Available          uint32 `json:"available"`
			AvailableGPIOs     []int  `json:"available_gpios"`
			PeriodicIntervalMS uint16 `json:"periodic_interval_ms"`
			EventsPerReport    uint8  `json:"events_per_report"`
			EventQueueSize     uint8  `json:"event_queue_size"`
		}{device.Serial, info.ProtocolVersion, info.FirmwareVersion(), uint8(info.Board), info.BoardName(),
			info.Available, info.AvailableGPIOs(), info.PeriodicIntervalMS, info.EventsPerReport, info.EventQueueSize})
	}
	fmt.Fprintf(a.stdout, "serial number     : %s\n", device.Serial)
	fmt.Fprintf(a.stdout, "board             : %s\n", info.BoardName())
	fmt.Fprintf(a.stdout, "firmware          : %s\n", info.FirmwareVersion())
	fmt.Fprintf(a.stdout, "protocol version  : %d\n", info.ProtocolVersion)
	fmt.Fprintf(a.stdout, "available GPIOs   : %d pins %s\n", len(info.AvailableGPIOs()), formatInts(info.AvailableGPIOs()))
	fmt.Fprintf(a.stdout, "periodic interval : %d ms\n", info.PeriodicIntervalMS)
	fmt.Fprintf(a.stdout, "events per report : %d (queue of %d)\n", info.EventsPerReport, info.EventQueueSize)
	return nil
}

func formatInts(values []int) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.Itoa(v)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func (a *app) cmdConfigGet(device *hidpin.Device) error {
	report, err := device.GetPinConfig()
	if err != nil {
		return err
	}
	info, err := device.Info()
	if err != nil {
		return err
	}
	if a.opts.json {
		pins := gpioMap[string]{}
		for _, gpio := range info.AvailableGPIOs() {
			pins[gpio] = report.Config[gpio].String()
		}
		return a.printJSON(struct {
			Result     uint8           `json:"result"`
			ResultGPIO uint8           `json:"result_gpio"`
			RequestID  uint8           `json:"request_id"`
			Pins       gpioMap[string] `json:"pins"`
		}{uint8(report.Result), report.ResultGPIO, report.RequestID, pins})
	}
	fmt.Fprintf(a.stdout, "result of the last write: %d (%s)\n", uint8(report.Result), report.Result.Message())
	for _, gpio := range info.AvailableGPIOs() {
		fmt.Fprintf(a.stdout, "  GPIO%-2d %s\n", gpio, report.Config[gpio])
	}
	return nil
}

func (a *app) cmdConfigSet(device *hidpin.Device, specs []string) error {
	settings := map[int]hidpin.PinSetting{}
	for _, spec := range specs {
		gpio, setting, err := parsePinSpec(spec)
		if err != nil {
			return usageError{message: "argument GPIO=SETTING: " + err.Error()}
		}
		settings[gpio] = setting
	}
	report, err := device.UpdatePins(settings)
	if err != nil {
		return err
	}
	gpios := make([]int, 0, len(settings))
	for gpio := range settings {
		gpios = append(gpios, gpio)
	}
	sort.Ints(gpios)
	if a.opts.json {
		pins := gpioMap[string]{}
		for _, gpio := range gpios {
			pins[gpio] = settings[gpio].String()
		}
		return a.printJSON(struct {
			RequestID uint8           `json:"request_id"`
			Pins      gpioMap[string] `json:"pins"`
		}{report.RequestID, pins})
	}
	for _, gpio := range gpios {
		fmt.Fprintf(a.stdout, "GPIO%d is now %s\n", gpio, settings[gpio])
	}
	return nil
}

func (a *app) cmdOutput(device *hidpin.Device, specs []string) error {
	levels := map[int]bool{}
	var gpios []int
	for _, spec := range specs {
		gpio, level, err := parseOutputSpec(spec)
		if err != nil {
			return usageError{message: "argument GPIO=VALUE: " + err.Error()}
		}
		if _, seen := levels[gpio]; !seen {
			gpios = append(gpios, gpio)
		}
		levels[gpio] = level
	}
	sort.Ints(gpios)
	config, err := device.PinConfig()
	if err != nil {
		return err
	}
	outputs := config.OutputsMask()
	var ignored []string
	for _, gpio := range gpios {
		if outputs>>gpio&1 == 0 {
			ignored = append(ignored, fmt.Sprintf("GPIO%d", gpio))
		}
	}
	if len(ignored) > 0 {
		fmt.Fprintf(a.stderr, "warning: %s is not an output pin and will be ignored\n", strings.Join(ignored, ", "))
	}
	if err := device.SetOutputs(levels); err != nil {
		return err
	}
	status, err := device.RequestStatus()
	if err != nil {
		return err
	}
	if a.opts.json {
		return a.printJSON(struct {
			Levels  uint32 `json:"levels"`
			Outputs uint32 `json:"outputs"`
		}{status.Levels, status.Outputs})
	}
	for _, gpio := range gpios {
		if outputs>>gpio&1 != 0 {
			fmt.Fprintf(a.stdout, "GPIO%d = %s\n", gpio, levelName(status.Level(gpio)))
		}
	}
	return nil
}

func levelName(high bool) string {
	if high {
		return "HIGH"
	}
	return "LOW"
}

type jsonEvent struct {
	GPIO    uint8   `json:"gpio"`
	Level   bool    `json:"level"`
	AgeUS   uint32  `json:"age_us"`
	StartUS *uint64 `json:"start_us"`
}

func (a *app) reportJSON(received hidpin.StatusReceived, missed int) error {
	s := received.Status
	events := []jsonEvent{}
	for _, e := range s.Events {
		event := jsonEvent{GPIO: e.GPIO, Level: e.Level, AgeUS: e.AgeUS}
		if start, ok := e.StartUS(s.TimestampUS); ok {
			event.StartUS = &start
		}
		events = append(events, event)
	}
	return a.printJSON(struct {
		Seq           uint16        `json:"seq"`
		Reason        []string      `json:"reason"`
		Flags         []string      `json:"flags"`
		TimestampUS   uint64        `json:"timestamp_us"`
		Monitored     uint32        `json:"monitored"`
		Outputs       uint32        `json:"outputs"`
		Levels        uint32        `json:"levels"`
		On            gpioMap[bool] `json:"on"`
		Events        []jsonEvent   `json:"events"`
		MissedReports int           `json:"missed_reports"`
	}{s.Seq, s.Reason.Names(), s.Flags.Names(), s.TimestampUS, s.Monitored, s.Outputs, s.Levels,
		gpioMap[bool](received.On), events, missed})
}

func onOffName(on bool) string {
	if on {
		return "ON"
	}
	return "OFF"
}

func (a *app) printChange(c hidpin.OnOffChange) {
	when := "         ?"
	if c.HasTime {
		when = fmt.Sprintf("%10.6f", float64(c.DeviceUS)/1e6)
	}
	fmt.Fprintf(a.stdout, "[%s] GPIO%-2d %-3s (%s)\n", when, c.GPIO, onOffName(c.On), levelName(c.Level))
}

// note prints a message about the connection: on stdout, or on stderr with --json so that stdout
// stays a stream of status reports.
func (a *app) note(message string) {
	if a.opts.json {
		fmt.Fprintln(a.stderr, message)
	} else {
		fmt.Fprintln(a.stdout, message)
	}
}

func (a *app) cmdWatch(ctx context.Context) error {
	activeLow := map[int]bool{}
	for _, flag := range []struct {
		list string
		low  bool
	}{{a.opts.activeLow, true}, {a.opts.activeHigh, false}} {
		gpios, err := parseGPIOList(flag.list)
		if err != nil {
			return usageError{message: err.Error()}
		}
		for _, gpio := range gpios {
			activeLow[gpio] = flag.low
		}
	}

	// Like the other commands, fail at once when there is no device to watch.
	entries, err := watchBus.Devices()
	if err != nil {
		return err
	}
	entry, err := hidpin.ChooseDevice(entries, a.opts.serial)
	if err != nil {
		return err
	}
	w, err := hidpin.Watch(ctx, hidpin.WatchOptions{Serial: entry.Serial, ActiveLow: activeLow, ScanInterval: scanInterval, Bus: watchBus})
	if err != nil {
		return err
	}
	defer w.Close()

	connected := false
	missed := 0
	for event := range w.Events() {
		switch e := event.(type) {
		case hidpin.Connected:
			if e.Reconnected {
				a.note("the device is connected again")
			}
			connected = true
		case hidpin.ConnectFailed:
			if !connected {
				return e.Err
			}
			fmt.Fprintf(a.stderr, "warning: %s\n", e.Err)
		case hidpin.Disconnected:
			a.note("the device was disconnected; waiting for it to come back")
		case hidpin.StatusReceived:
			missed += e.Missed
			if a.opts.json {
				if err := a.reportJSON(e, missed); err != nil {
					return err
				}
				continue
			}
			if e.Missed > 0 {
				fmt.Fprintf(a.stderr, "warning: missed %d status report(s)\n", e.Missed)
			}
			if e.Status.Overflow() {
				fmt.Fprintln(a.stderr, "warning: the device dropped edge events (the history is incomplete)")
			}
			if a.opts.all && len(e.Status.Events) == 0 && e.Status.Reason&hidpin.ReasonHostRequest == 0 {
				fmt.Fprintf(a.stdout, "seq=%d reason=%s\n", e.Status.Seq, strings.Join(e.Status.Reason.Names(), ","))
			}
		case hidpin.InitialOnOff:
			if !a.opts.json {
				gpios := make([]int, 0, len(e.On))
				for gpio := range e.On {
					gpios = append(gpios, gpio)
				}
				sort.Ints(gpios)
				parts := make([]string, len(gpios))
				for i, gpio := range gpios {
					parts[i] = fmt.Sprintf("GPIO%d=%s", gpio, onOffName(e.On[gpio]))
				}
				fmt.Fprintln(a.stdout, strings.Join(parts, " "))
			}
		case hidpin.OnOffChange:
			if !a.opts.json {
				a.printChange(e)
			}
		case hidpin.PinConfigChanged:
			if !a.opts.json {
				fmt.Fprintln(a.stdout, "the pin configuration changed")
			}
		case hidpin.OutputsReset:
			if !a.opts.json {
				cause := e.Cause.String()
				if e.Cause == hidpin.OutputResetSuspend {
					cause = "USB disconnect or suspend"
				}
				fmt.Fprintf(a.stdout, "outputs returned to their initial level (%s)\n", cause)
			}
		}
	}
	return w.Err()
}

func (a *app) run(ctx context.Context) error {
	if a.opts.help {
		fmt.Fprint(a.stdout, usage)
		return nil
	}
	if len(a.opts.args) == 0 {
		return usageError{message: "the following arguments are required: command", showHelp: true}
	}
	command, rest := a.opts.args[0], a.opts.args[1:]

	withDevice := func(f func(*hidpin.Device) error) error {
		device, err := openDevice(a.opts.serial)
		if err != nil {
			return err
		}
		defer device.Close()
		return f(device)
	}

	switch command {
	case "list":
		return a.cmdList()
	case "info":
		return withDevice(a.cmdInfo)
	case "watch":
		return a.cmdWatch(ctx)
	case "config":
		if len(rest) == 0 {
			return usageError{message: "config: the following arguments are required: get or set", showHelp: true}
		}
		switch rest[0] {
		case "get":
			return withDevice(a.cmdConfigGet)
		case "set":
			if len(rest) < 2 {
				return usageError{message: "config set: at least one GPIO=SETTING is required"}
			}
			for _, spec := range rest[1:] {
				if _, _, err := parsePinSpec(spec); err != nil {
					return usageError{message: "argument GPIO=SETTING: " + err.Error()}
				}
			}
			return withDevice(func(d *hidpin.Device) error { return a.cmdConfigSet(d, rest[1:]) })
		}
		return usageError{message: fmt.Sprintf("config: invalid choice '%s' (choose from get, set)", rest[0])}
	case "output":
		if len(rest) == 0 {
			return usageError{message: "output: at least one GPIO=VALUE is required"}
		}
		for _, spec := range rest {
			if _, _, err := parseOutputSpec(spec); err != nil {
				return usageError{message: "argument GPIO=VALUE: " + err.Error()}
			}
		}
		return withDevice(func(d *hidpin.Device) error { return a.cmdOutput(d, rest) })
	}
	return usageError{message: fmt.Sprintf("invalid choice '%s' (choose from list, info, watch, config, output)", command)}
}

func run(ctx context.Context, argv []string, stdout, stderr io.Writer) int {
	opts, err := parseOptions(argv)
	a := &app{opts: opts, stdout: stdout, stderr: stderr}
	if err == nil {
		err = a.run(ctx)
	}
	var usageErr usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &usageErr):
		if usageErr.showHelp {
			fmt.Fprint(stderr, usage)
			fmt.Fprintln(stderr)
		}
		fmt.Fprintf(stderr, "hidpin: error: %s\n", usageErr.message)
		return 2
	default:
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
