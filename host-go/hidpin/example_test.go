package hidpin_test

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/yukkeorg/hidpin/host-go/hidpin"
	"github.com/yukkeorg/hidpin/host-go/hidpin/hidpintest"
)

// This example runs against the fake device from hidpintest; with a board attached, use
// hidpin.Open("") instead of NewDevice.
func Example() {
	device := hidpin.NewDevice(hidpintest.New(hidpin.ProtocolVersion), "ABCD0123456789EF")
	defer device.Close()

	info, err := device.Info()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(info.BoardName(), len(info.AvailableGPIOs()), "GPIOs")

	settings := map[int]hidpin.PinSetting{5: hidpin.MonitorPullUp(20), 7: hidpin.Output(false)}
	if _, err := device.UpdatePins(settings); err != nil {
		log.Fatal(err)
	}
	config, err := device.PinConfig()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("GPIO5:", config[5], "GPIO7:", config[7])

	status, err := device.RequestStatus()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("GPIO5 on:", device.OnOff(status, config)[5])
	// Output:
	// Raspberry Pi Pico 26 GPIOs
	// GPIO5: pullup:20 GPIO7: out:low
	// GPIO5 on: false
}

// Watching a real board: print every edge event with the time it began.
func ExampleDevice_ReadStatus() {
	device, err := hidpin.Open("") // the only connected board; pass a serial number to pick one
	if err != nil {
		log.Fatal(err)
	}
	defer device.Close()
	config, err := device.PinConfig()
	if err != nil {
		log.Fatal(err)
	}

	for {
		status, ok, err := device.ReadStatus(time.Second)
		if err != nil {
			log.Fatal(err)
		}
		if !ok {
			continue // no report within a second
		}
		for _, event := range status.Events {
			start, _ := event.StartUS(status.TimestampUS)
			on := event.Level != device.IsActiveLow(int(event.GPIO), config)
			fmt.Printf("GPIO%d on=%v at %d us\n", event.GPIO, on, start)
		}
	}
}

// A long-running program keeps GPIO5 monitored and GPIO7 an output, whatever happens to the
// board. This example runs against a simulated board; with a real one, leave Bus nil.
func ExampleWatch() {
	board := hidpintest.NewBoard("ABCD0123456789EF")
	bus := hidpintest.NewBus(board)

	target, err := hidpin.PinConfig{}.WithPins(map[int]hidpin.PinSetting{
		5: hidpin.MonitorPullUp(20), // pins not listed are unused
		7: hidpin.Output(false),
	})
	if err != nil {
		log.Fatal(err)
	}
	w, err := hidpin.Watch(context.Background(), hidpin.WatchOptions{TargetPins: &target, Bus: bus})
	if err != nil {
		log.Fatal(err)
	}
	defer w.Close()

	for event := range w.Events() {
		switch e := event.(type) {
		case hidpin.Connected:
			fmt.Println("connected to", e.Serial)
		case hidpin.OutputsReset:
			fmt.Println("outputs at their initial level:", e.GPIOs, "-", e.Cause)
		case hidpin.InitialOnOff:
			fmt.Println("GPIO5 on:", e.On[5])
			board.SetInput(5, false) // someone presses the switch
		case hidpin.OnOffChange:
			fmt.Println("GPIO5 on:", e.On, "timed:", e.HasTime)
			w.Close()
		}
	}
	if err := w.Err(); err != nil {
		log.Fatal(err)
	}
	// Output:
	// connected to ABCD0123456789EF
	// outputs at their initial level: [7] - pin configuration written
	// GPIO5 on: false
	// GPIO5 on: true timed: true
}
