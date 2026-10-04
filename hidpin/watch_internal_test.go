package hidpin

import (
	"testing"
	"time"
)

func TestDeviceClockUsesTheLeastDelayedSample(t *testing.T) {
	base := time.Now()
	var c deviceClock
	c.add(base.Add(5*time.Millisecond), 1_000_000)    // read 5 ms late
	c.add(base.Add(1001*time.Millisecond), 2_000_000) // read 1 ms late
	c.add(base.Add(2030*time.Millisecond), 3_000_000) // read 30 ms late
	if got, want := c.at(2_500_000), base.Add(1501*time.Millisecond); !got.Equal(want) {
		t.Errorf("at 2.5 s: %v, want %v", got.Sub(base), want.Sub(base))
	}
	if got := c.at(2_500_000); got.String() != got.Round(0).String() {
		t.Error("the time carries a monotonic reading")
	}
}

func TestDeviceClockForgetsOldSamples(t *testing.T) {
	base := time.Now()
	var c deviceClock
	c.add(base, 0) // a perfect sample, but the clocks drift apart afterwards
	for i := 1; i <= 3; i++ {
		elapsed := time.Duration(i) * clockWindow
		c.add(base.Add(elapsed+10*time.Millisecond), uint64(elapsed/time.Microsecond))
	}
	device := uint64(3 * clockWindow / time.Microsecond)
	if got, want := c.at(device), base.Add(3*clockWindow+10*time.Millisecond); !got.Equal(want) {
		t.Errorf("used a sample older than two windows: off by %v", got.Sub(want))
	}
}

func TestDrivenOutputs(t *testing.T) {
	var prev, next PinConfig
	prev[1], next[1] = Output(false), Output(false)     // unchanged
	prev[2], next[2] = Output(false), Output(true)      // initial level changed
	prev[3], next[3] = MonitorPullUp(20), Output(false) // became an output
	prev[4], next[4] = Output(true), Unused()           // no longer an output
	if got := drivenOutputs(prev, next); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Errorf("driven %v", got)
	}
}
