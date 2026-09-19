package hidpin_test

import (
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
