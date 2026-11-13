import pytest

gpiozero = pytest.importorskip("gpiozero")

from gpiozero.pins.mock import MockFactory, MockPWMPin  # noqa: E402

from galaxia import gpio  # noqa: E402


@pytest.fixture
def pins():
    factory = MockFactory(pin_class=MockPWMPin)
    previous = gpiozero.Device.pin_factory
    gpiozero.Device.pin_factory = factory
    yield factory
    factory.reset()
    gpiozero.Device.pin_factory = previous


def test_digital_input_follows_the_pin(env, pins):
    dev = env.device("arcade-1", "Start button")
    button = gpio.digital_input(dev, "button", 17, debounce_ms=20)
    env.start(dev)
    env.step(dev, 30)
    pin = pins.pin(17)
    pin.drive_low()  # pressed: the button pulls the pin to ground
    env.step(dev, 30)
    pin.drive_high()
    env.step(dev, 30)
    assert [m["v"] for m in env.sent(dev, "in/button")] == [0, 1, 0]
    assert button.value == 0


def test_counter_input_counts_presses(env, pins):
    dev = env.device()
    balls = gpio.counter_input(dev, "count", 27)
    env.start(dev)
    pin = pins.pin(27)
    for _ in range(3):
        pin.drive_low()
        pin.drive_high()
    assert balls.total == 3
    assert [m["v"] for m in env.sent(dev, "in/count")] == [0, 1, 2, 3]


def test_safety_input_reads_a_normally_closed_contact(env, pins):
    dev = env.device("estop-box-red", "E-stop box")
    stop = gpio.safety_input(dev, "stop", 22)
    pin = pins.pin(22)
    assert not stop.safe  # the pull-up holds the pin high: an open circuit is a stop
    pin.drive_low()  # contact closed: safe
    env.start(dev)
    env.step(dev, 60)
    assert stop.safe
    pin.drive_high()  # contact open: stop
    assert not stop.safe
    assert env.sent(dev, "hb")[-1]["ch"] == {"stop": 0}


def test_outputs_drive_pins(env, pins):
    dev = env.device("stack-light", "Stack light")
    gpio.digital_output(dev, "green", 5)
    gpio.digital_output(dev, "red", 6, active_high=False, relay=True)
    gpio.pwm_output(dev, "fan", 13)
    env.start(dev)
    env.fms("dev/stack-light/out/green", {"v": 1}, retain=True)
    env.fms("dev/stack-light/out/red", {"v": 1}, retain=True)
    env.fms("dev/stack-light/out/fan", {"v": 0.25}, retain=True)
    assert pins.pin(5).state == 1
    assert pins.pin(6).state == 0  # active low relay board: on means a low pin
    assert pins.pin(13).state == pytest.approx(0.25)
    assert dev.manifest()["channels"][1]["type"] == "relay"
