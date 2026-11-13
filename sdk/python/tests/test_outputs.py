from conftest import FIELD_EPOCH_MS

RED = (255, 0, 0)
BLACK = (0, 0, 0)


def sync_clock(env, dev, field_ms=FIELD_EPOCH_MS):
    env.fms("field/clock", {"t": field_ms})
    assert dev.clock_synced


def test_digital_relay_pwm_display_and_sound_commands(env):
    dev = env.device("stack-light", "Stack light")
    calls = []
    dev.digital_out("green", on_change=lambda on: calls.append(("green", on)))
    dev.relay("horn", on_change=lambda on: calls.append(("horn", on)))
    dev.pwm("fan", on_change=lambda level: calls.append(("fan", level)))
    dev.display("timer", on_change=lambda text, number: calls.append(("timer", text, number)))
    dev.sound("buzzer", on_play=lambda name: calls.append(("buzzer", name)))
    env.start(dev)

    env.fms("dev/stack-light/out/green", {"v": 1}, retain=True)
    env.fms("dev/stack-light/out/green", {"v": 1}, retain=True)  # no change, no call
    env.fms("dev/stack-light/out/horn", {"v": 1}, retain=True)
    env.fms("dev/stack-light/out/horn", {"v": 0}, retain=True)
    env.fms("dev/stack-light/out/fan", {"v": 0.35}, retain=True)
    env.fms("dev/stack-light/out/fan", {"v": 7}, retain=True)
    env.fms("dev/stack-light/out/timer", {"text": "1:32"}, retain=True)
    env.fms("dev/stack-light/out/timer", {"v": 92}, retain=True)
    env.fms("dev/stack-light/out/buzzer", {"play": "buzzer"}, retain=True)
    assert calls == [
        ("green", True),
        ("horn", True),
        ("horn", False),
        ("fan", 0.35),
        ("fan", 1.0),
        ("timer", "1:32", None),
        ("timer", "92", 92),
        ("buzzer", "buzzer"),
    ]


def test_retained_outputs_arrive_on_connect_but_sounds_do_not_replay(env):
    dev = env.device("stack-light", "Stack light")
    calls = []
    dev.digital_out("green", on_change=lambda on: calls.append(("green", on)))
    dev.sound("buzzer", on_play=lambda name: calls.append(("buzzer", name)))
    env.fms("dev/stack-light/out/green", {"v": 1}, retain=True)
    env.fms("dev/stack-light/out/buzzer", {"play": "buzzer"}, retain=True)
    env.start(dev)
    assert calls == [("green", True)]


def test_cleared_output_turns_off(env):
    dev = env.device("stack-light", "Stack light")
    green = dev.digital_out("green")
    strip = dev.led_strip("strip", 4)
    env.start(dev)
    env.fms("dev/stack-light/out/green", {"v": 1}, retain=True)
    env.fms("dev/stack-light/out/strip", {"p": "solid", "c": ["#ff0000"]}, retain=True)
    assert green.value is True and strip.frame == [RED] * 4
    env.fms("dev/stack-light/out/green", b"", retain=True)
    env.fms("dev/stack-light/out/strip", b"", retain=True)
    assert green.value is False and strip.frame == [BLACK] * 4


def test_bad_messages_are_ignored(env):
    dev = env.device("stack-light", "Stack light")
    green = dev.digital_out("green")
    env.start(dev)
    env.fms("dev/stack-light/out/green", b"{not json", retain=True)
    env.fms("dev/stack-light/out/green", b"[1]", retain=True)
    env.fms("dev/stack-light/out/nothing", {"v": 1}, retain=True)
    assert green.value is None


def test_at_waits_for_the_field_clock(env):
    dev = env.device("goal-red", "Goal lights")
    strip = dev.led_strip("strip", 3)
    env.start(dev)
    sync_clock(env, dev)
    t0 = dev.field_time_ms()
    env.fms("dev/goal-red/out/strip", {"p": "solid", "c": ["#ff0000"], "at": t0 + 500}, retain=True)
    assert strip.frame in (None, [BLACK] * 3)
    env.step(dev, 490)
    assert strip.frame != [RED] * 3
    env.step(dev, 10)
    assert strip.frame == [RED] * 3


def test_at_in_the_past_is_shown_at_once(env):
    dev = env.device("goal-red", "Goal lights")
    strip = dev.led_strip("strip", 3)
    env.start(dev)
    sync_clock(env, dev)
    env.fms("dev/goal-red/out/strip", {"p": "solid", "c": ["#ff0000"], "at": dev.field_time_ms() - 60_000})
    assert strip.frame == [RED] * 3


def test_commands_sent_ahead_queue_up_and_an_immediate_one_cancels_them(env):
    dev = env.device("goal-red", "Goal lights")
    seen = []
    dev.display("count", on_change=lambda text, number: seen.append(text))
    env.start(dev)
    sync_clock(env, dev)
    t0 = dev.field_time_ms()
    for beat, text in ((1000, "3"), (2000, "2"), (3000, "1"), (4000, "GO")):
        env.fms("dev/goal-red/out/count", {"text": text, "at": t0 + beat}, retain=True)
    env.step(dev, 2500)
    assert seen == ["3", "2"]
    # The countdown is cancelled: an immediate command drops everything still pending.
    env.fms("dev/goal-red/out/count", {"text": "--"}, retain=True)
    env.step(dev, 3000)
    assert seen == ["3", "2", "--"]


def test_a_later_command_for_an_earlier_time_replaces_the_pending_ones(env):
    dev = env.device("goal-red", "Goal lights")
    seen = []
    dev.display("count", on_change=lambda text, number: seen.append(text))
    env.start(dev)
    sync_clock(env, dev)
    t0 = dev.field_time_ms()
    env.fms("dev/goal-red/out/count", {"text": "late", "at": t0 + 2000})
    env.fms("dev/goal-red/out/count", {"text": "early", "at": t0 + 1000})
    env.step(dev, 3000)
    assert seen == ["early"]


def test_system_clock_is_used_until_field_clock_arrives(env):
    dev = env.device("goal-red", "Goal lights")
    strip = dev.led_strip("strip", 1)
    env.start(dev)
    assert not dev.clock_synced
    wall_ms = int(env.wall() * 1000)
    assert abs(dev.field_time_ms() - wall_ms) <= 1
    env.fms("dev/goal-red/out/strip", {"p": "solid", "c": ["#ff0000"], "at": wall_ms + 100})
    env.step(dev, 100)
    assert strip.frame == [RED]


def test_led_strip_animates_from_at(env):
    dev = env.device("goal-red", "Goal lights")
    frames = []
    dev.led_strip("strip", 2, on_frame=frames.append)
    env.start(dev)
    sync_clock(env, dev)
    t0 = dev.field_time_ms()
    env.fms("dev/goal-red/out/strip", {"p": "blink", "c": ["#ff0000"], "ms": 400, "at": t0 + 100})
    env.step(dev, 100)
    assert frames[-1] == [RED, RED]
    env.step(dev, 200)
    assert frames[-1] == [BLACK, BLACK]
    env.step(dev, 200)
    assert frames[-1] == [RED, RED]
    # Frames are only handed over when they change.
    assert len(frames) == 3


def test_led_strip_limit_caps_brightness(env):
    dev = env.device("goal-red", "Goal lights")
    strip = dev.led_strip("strip", 1, limit=0.5)
    env.start(dev)
    env.fms("dev/goal-red/out/strip", {"p": "solid", "c": ["#ff8000"], "br": 0.5})
    assert strip.frame == [(64, 32, 0)]


def test_identify_flashes_strips_and_calls_back(env):
    dev = env.device("goal-red", "Goal lights")
    strip = dev.led_strip("strip", 2)
    identified = []
    dev.on_identify(lambda: identified.append(True))
    env.start(dev)
    env.fms("dev/goal-red/out/strip", {"p": "solid", "c": ["#ff0000"]})
    env.fms("dev/goal-red/cmd", {"do": "identify"})
    assert identified == [True]
    assert strip.frame == [(128, 128, 128)] * 2
    env.step(dev, 260)  # frames are drawn every 20 ms
    assert strip.frame == [BLACK] * 2
    env.step(dev, 5000)
    assert strip.frame == [RED] * 2


def test_reboot_and_other_commands(env):
    dev = env.device()
    rebooted = []
    other = []
    dev.on_reboot = lambda: rebooted.append(True)
    dev.on_command(other.append)
    env.start(dev)
    env.fms("dev/beam-red-1/cmd", {"do": "reboot"})
    env.fms("dev/beam-red-1/cmd", {"do": "calibrate", "channel": "weight", "step": "tare"})
    assert rebooted == [True]
    assert other == [{"do": "calibrate", "channel": "weight", "step": "tare"}]


def test_field_state(env):
    dev = env.device("timer-1", "Match timer")
    states = []
    dev.on_field_state(states.append)
    env.start(dev, approve=False)  # field/# needs no approval
    env.fms("field/state", {"state": "running", "phase": "teleop", "phase_remaining": 93.4,
                            "match_remaining": 120.1, "scores": {"red": 41, "blue": 38}}, retain=True)
    state = dev.field_state
    assert states == [state]
    assert (state.state, state.phase, state.scores) == ("running", "teleop", {"red": 41, "blue": 38})
    assert dev.phase_remaining() == 93.4
    env.step(dev, 400)
    assert abs(dev.phase_remaining() - 93.0) < 1e-9
    env.fms("field/state", {"state": "post", "phase": "teleop", "phase_remaining": 0}, retain=True)
    env.step(dev, 400)
    assert dev.phase_remaining() == 0


def test_field_state_is_received_when_retained_before_connecting(env):
    env.fms("field/state", {"state": "idle", "phase_remaining": 15}, retain=True)
    dev = env.device("timer-1", "Match timer")
    env.start(dev)
    assert dev.field_state.state == "idle"
