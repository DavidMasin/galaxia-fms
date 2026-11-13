from conftest import FIELD_EPOCH_MS

from galaxia.timing import FieldClock, Schedule


class Mono:
    def __init__(self):
        self.ms = 5_000

    def __call__(self):
        return self.ms


def test_offset_from_field_clock_messages(env):
    dev = env.device()
    env.start(dev)
    env.fms("field/clock", {"t": FIELD_EPOCH_MS})
    assert dev.field_time_ms() == FIELD_EPOCH_MS
    env.step(dev, 1234)
    assert dev.field_time_ms() == FIELD_EPOCH_MS + 1234


def test_largest_sample_wins_because_delay_only_makes_samples_smaller():
    mono = Mono()
    clock = FieldClock(mono)
    assert clock.now() is None
    clock.sample(1_000_000)  # arrived with 0 ms delay: offset 995 000
    mono.ms += 1000
    clock.sample(1_000_000 + 1000 - 40)  # 40 ms late
    assert clock.offset == 995_000
    assert clock.now() == 1_001_000


def test_old_samples_leave_the_window():
    mono = Mono()
    clock = FieldClock(mono)
    clock.sample(1_000_000)
    for _ in range(FieldClock.WINDOW):
        mono.ms += 1000
        clock.sample(1_000_000 + (mono.ms - 5_000) - 20)
    assert clock.offset == 995_000 - 20


def test_a_single_wild_sample_is_ignored_but_a_real_jump_is_followed():
    mono = Mono()
    clock = FieldClock(mono)
    clock.sample(1_000_000)
    mono.ms += 1000
    clock.sample(1_001_000 + 60_000)  # one strange message
    assert clock.offset == 995_000
    mono.ms += 1000
    clock.sample(1_002_000)
    assert clock.offset == 995_000
    for _ in range(FieldClock.OUTLIERS):  # the FMS clock really moved back an hour
        mono.ms += 1000
        clock.sample(mono.ms + 995_000 - 3_600_000)
    assert clock.offset == 995_000 - 3_600_000


def test_schedule_without_clock_applies_at_once():
    schedule = Schedule(4)
    assert schedule.offer(123, None, "x") is True
    assert len(schedule) == 0


def test_schedule_drops_the_earliest_when_full():
    schedule = Schedule(2)
    for at in (10, 20, 30):
        assert schedule.offer(at, 0, at) is False
    assert schedule.dropped == 1
    assert [command for _, command in schedule.due(100)] == [20, 30]
