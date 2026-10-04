# RPI2040/35-IO

**English** | [日本語](CONCEPT-ja.md)

- Use the GPIO pins and the USB port of an RPi2040/2035 microcontroller board.
- Monitor the GPIO ports and pass their state to a computer through the USB port.
- To the computer, the board works as a USB-HID device (like a keyboard).
- Data is exchanged following the rules of HID.

## Development environment

- Develop in C/C++ with the Pico SDK
- Use TinyUSB as the library

## HID

- Follow HID 1.11
- Input: device -> host
	- Sequence number
	- GPIO state (32 bits)
	- Timestamp (µs)
	- Edge events
- Output: host -> device
	- Mask and values of the output pins
- Feature:
	- Whether each pin is an input or an output
	- Pull-up / pull-down
	- Settings
- UsagePage
	- Vendor-defined

## Performance

1. Time resolution
   - At Full Speed, there is one transaction per 1 ms frame, carrying at most 64 bytes.
   - Pulses shorter than 1 ms, and two or more edges within 1 ms, are lost if only the state is sent.
   - Countermeasure: the device collects timestamped events using interrupts and sends them together in 64-byte reports.
2. Receive buffers on the host
   - Windows has 32 input buffers by default (HidD_SetNumInputBuffers raises this to at most 512).
   - They can overflow when the application falls behind in reading, so the reports carry a sequence number to detect losses.
   - The state at start-up is read with Get_Report(Input).

## RPi204x boards available for testing

- Raspberry Pi Pico
- Adafruit Qi Py RP2040
