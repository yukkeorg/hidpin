"""The asyncio form of the Watch layer."""

import asyncio

import pytest

from hidpin import (
    Connected,
    DeviceDisconnected,
    Disconnected,
    InitialOnOff,
    OnOffChange,
    PinConfig,
    PinMode,
    PinSetting,
    SeveralDevices,
    WatcherClosed,
    async_watch,
)
from hidpin.testing import FakeBoard, FakeBus

TIMEOUT = 3.0


async def await_event(watcher, kind):
    while True:
        event = await asyncio.wait_for(watcher.get(), TIMEOUT)
        if isinstance(event, kind):
            return event


def test_async_watch_follows_a_board():
    async def scenario():
        board = FakeBoard("A")
        bus = FakeBus(board)
        target = PinConfig.unused().with_pins({5: PinSetting.monitor(PinMode.PULLUP, 20), 7: PinSetting.output(False)})
        async with async_watch(target_pins=target, bus=bus, scan_interval=0.005) as watcher:
            assert (await await_event(watcher, InitialOnOff)).on == {5: False}
            board.set_input(5, False)
            change = await await_event(watcher, OnOffChange)
            assert change.gpio == 5 and change.on and change.has_time

            await watcher.set_outputs({7: True})
            assert board.level(7)

            bus.unplug(board)
            await await_event(watcher, Disconnected)
            with pytest.raises(DeviceDisconnected):
                await watcher.set_outputs({7: True})
            bus.plug(board)
            assert (await await_event(watcher, Connected)).reconnected

    asyncio.run(scenario())


def test_async_iteration_ends_when_the_watcher_stops():
    async def scenario():
        watcher = async_watch(bus=FakeBus(FakeBoard("A"), FakeBoard("B")), scan_interval=0.005)
        events = [event async for event in watcher]
        assert events == []
        assert isinstance(watcher.error, SeveralDevices)
        with pytest.raises(WatcherClosed):
            await watcher.get()
        await watcher.close()

    asyncio.run(asyncio.wait_for(scenario(), TIMEOUT))


def test_cancelling_a_wait_loses_no_event():
    async def scenario():
        board = FakeBoard("A")
        async with async_watch(bus=FakeBus(board), scan_interval=0.005) as watcher:
            await await_event(watcher, InitialOnOff)
            waiting = asyncio.create_task(watcher.get())
            await asyncio.sleep(0.05)
            waiting.cancel()
            with pytest.raises(asyncio.CancelledError):
                await waiting
            board.set_input(5, False)
            change = await await_event(watcher, OnOffChange)
            assert change.gpio == 5 and change.on

    asyncio.run(scenario())


def test_async_watch_needs_a_running_loop():
    with pytest.raises(RuntimeError):
        async_watch(bus=FakeBus())


def test_async_set_target_pins():
    async def scenario():
        board = FakeBoard("A")
        async with async_watch(bus=FakeBus(board), scan_interval=0.005) as watcher:
            await await_event(watcher, Connected)
            target = PinConfig.unused().with_pins({7: PinSetting.output(True)})
            await watcher.set_target_pins(target)
            assert board.pin_config == target and board.level(7)

    asyncio.run(scenario())
