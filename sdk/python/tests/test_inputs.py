import pytest


def test_counter_sends_running_totals_with_sequence_numbers(env):
    dev = env.device()
    balls = dev.counter("count")
    env.start(dev)
    balls.increment()
    balls.increment()
    balls.increment(3, conf=0.934567)
    sent = env.sent(dev, "in/count")
    # The first message is the total (0) sent on connect.
    assert [m["v"] for m in sent] == [0, 1, 2, 5]
    seqs = [m["seq"] for m in sent]
    assert seqs == sorted(seqs) and len(set(seqs)) == len(seqs)
    assert sent[-1]["conf"] == 0.935
    assert "conf" not in sent[1]
    assert all(not m.retain for m in env.broker.messages("galaxia/v1/dev/beam-red-1/in/count"))


def test_counter_restart_starts_again_from_zero(env):
    dev = env.device()
    balls = dev.counter("count")
    env.start(dev)
    balls.increment(7)
    dev.disconnect()
    env.broker.pump()

    restarted = env.device()
    restarted.counter("count")
    env.start(restarted)
    totals = [m["v"] for m in env.sent(restarted, "in/count")]
    # 0 after 7: a lower total tells the FMS the device restarted.
    assert totals[-1] == 0
    assert totals == [0, 7, 0]


def test_counter_only_goes_up(env):
    dev = env.device()
    balls = dev.counter("count")
    with pytest.raises(ValueError):
        balls.increment(-1)


def test_digital_without_debounce_sends_changes_only(env):
    dev = env.device()
    beam = dev.digital("beam")
    env.start(dev)
    for value in (1, 1, 0, 0, 1):
        beam.set(value)
    assert [m["v"] for m in env.sent(dev, "in/beam")] == [1, 0, 1]


def test_debounce_ignores_bounces_and_reports_a_held_change(env):
    dev = env.device()
    button = dev.digital("button", debounce_ms=30)
    env.start(dev)
    button.set(False)
    env.step(dev, 40)
    assert [m["v"] for m in env.sent(dev, "in/button")] == [0]

    # Contact bounce: flips every 5 ms for 20 ms, then settles closed.
    for value in (True, False, True, False, True):
        button.set(value)
        env.step(dev, 5)
    assert [m["v"] for m in env.sent(dev, "in/button")] == [0]
    env.step(dev, 20)
    assert [m["v"] for m in env.sent(dev, "in/button")] == [0]
    env.step(dev, 10)
    assert [m["v"] for m in env.sent(dev, "in/button")] == [0, 1]

    # A glitch shorter than the debounce time is never reported.
    button.set(False)
    env.step(dev, 10)
    button.set(True)
    env.step(dev, 100)
    assert [m["v"] for m in env.sent(dev, "in/button")] == [0, 1]


def test_digital_invert(env):
    dev = env.device()
    switch = dev.digital("switch", invert=True)
    env.start(dev)
    switch.set(True)
    switch.set(False)
    assert [m["v"] for m in env.sent(dev, "in/switch")] == [0, 1]


def test_analog_deadband_and_rate_limit(env):
    dev = env.device()
    weight = dev.analog("weight", deadband=0.5, min_interval_ms=100)
    env.start(dev)
    weight.set(10.0)
    weight.set(10.3)  # inside the deadband
    weight.set(11.0)  # outside, but too soon: held back
    assert [m["v"] for m in env.sent(dev, "in/weight")] == [10.0]
    env.step(dev, 100)
    assert [m["v"] for m in env.sent(dev, "in/weight")] == [10.0, 11.0]
    weight.set(12.0)
    weight.set(13.0)  # the latest value wins
    env.step(dev, 100)
    assert [m["v"] for m in env.sent(dev, "in/weight")] == [10.0, 11.0, 13.0]


def test_encoder_sends_signed_integers(env):
    dev = env.device()
    turret = dev.encoder("turret")
    env.start(dev)
    turret.set(-12.7)
    env.step(dev, 60)
    turret.set(40)
    env.step(dev, 60)
    assert [m["v"] for m in env.sent(dev, "in/turret")] == [-12, 40]


def test_text_reports_every_read(env):
    dev = env.device()
    tag = dev.text("tag")
    env.start(dev)
    tag.set("04a1b2c3")
    tag.set("04a1b2c3", conf=0.5)
    assert [m["v"] for m in env.sent(dev, "in/tag")] == ["04a1b2c3", "04a1b2c3"]
    # Text is an event, so it is not sent again on reconnect.
    env.broker.drop(dev.id)
    env.broker.pump()
    dev._client.reconnect()
    env.broker.pump()
    assert len(env.sent(dev, "in/tag")) == 2


def test_input_timestamp_is_passed_through(env):
    dev = env.device()
    tag = dev.text("tag")
    env.start(dev)
    tag.set("x", ts=dev.field_time_ms())
    assert env.sent(dev, "in/tag")[-1]["ts"] == dev.field_time_ms()


def test_heartbeat_every_200_ms_for_safety_channels(env):
    dev = env.device("estop-box-red", "E-stop box")
    stop = dev.safety_stop("stop")
    env.start(dev)
    stop.set(True)
    env.step(dev, 100)
    start = len(env.sent(dev, "hb"))
    times = []
    for _ in range(200):
        before = len(env.sent(dev, "hb"))
        env.step(dev, 10)
        if len(env.sent(dev, "hb")) > before:
            times.append(env.clock.seconds)
    beats = env.sent(dev, "hb")[start:]
    assert len(beats) == 10  # two seconds
    gaps = [round((b - a) * 1000) for a, b in zip(times, times[1:])]
    assert gaps == [200] * 9
    assert all(beat["ch"] == {"stop": 1} for beat in beats)
    seqs = [beat["seq"] for beat in env.sent(dev, "hb")]
    assert seqs == list(range(1, len(seqs) + 1))


def test_no_heartbeat_without_safety_channels(env):
    dev = env.device()
    dev.counter("count")
    env.start(dev)
    env.step(dev, 1000)
    assert env.sent(dev, "hb") == []


def test_safety_stop_is_immediate_and_recovery_is_held(env):
    dev = env.device("estop-box-red", "E-stop box")
    stop = dev.safety_stop("stop", recover_ms=50)
    env.start(dev)
    # Until the first reading the channel says stop.
    assert env.sent(dev, "hb")[0]["ch"] == {"stop": 0}
    stop.set(True)
    env.step(dev, 40)
    assert env.sent(dev, "in/stop")[-1]["v"] == 0
    env.step(dev, 10)
    assert env.sent(dev, "in/stop")[-1]["v"] == 1
    assert env.sent(dev, "hb")[-1]["ch"] == {"stop": 1}

    beats_before = len(env.sent(dev, "hb"))
    stop.set(False)  # pressed: sent at once, with a heartbeat, without waiting for the cadence
    assert env.sent(dev, "in/stop")[-1]["v"] == 0
    assert len(env.sent(dev, "hb")) == beats_before + 1
    assert env.sent(dev, "hb")[-1]["ch"] == {"stop": 0}

    # A closed contact that bounces open again before recover_ms does not count as safe.
    stop.set(True)
    env.step(dev, 30)
    stop.set(False)
    stop.set(True)
    env.step(dev, 30)
    assert env.sent(dev, "in/stop")[-1]["v"] == 0
    env.step(dev, 30)
    assert env.sent(dev, "in/stop")[-1]["v"] == 1
