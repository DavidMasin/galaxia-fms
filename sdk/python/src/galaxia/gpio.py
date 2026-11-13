"""Raspberry Pi GPIO helpers built on gpiozero (pip install "galaxia-device[gpio]").

Pins are gpiozero pin names: a BCM number such as 17, or "GPIO17", "BOARD11" and so on. Each helper creates the
channel and wires it to the pin, and returns the channel.
"""


def _gpiozero():
    try:
        import gpiozero
    except ImportError as error:
        raise ImportError('galaxia.gpio needs gpiozero: pip install "galaxia-device[gpio]"') from error
    return gpiozero


def _keep(channel, device):
    # The gpiozero object must stay alive as long as the channel does.
    channel.gpio = device
    return channel


def digital_input(device, channel_id, pin, name=None, pull_up=True, debounce_ms=20, invert=False):
    """A button or switch: 1 while pressed (pin pulled to ground when pull_up is True, to 3.3 V otherwise)."""
    button = _gpiozero().Button(pin, pull_up=pull_up)
    channel = device.digital(channel_id, name, debounce_ms=debounce_ms, invert=invert)

    def update():
        channel.set(button.is_pressed)

    button.when_pressed = update
    button.when_released = update
    update()
    return _keep(channel, button)


def counter_input(device, channel_id, pin, name=None, pull_up=True, edge="pressed", bounce_ms=None):
    """Counts edges on a pin: edge is "pressed" (the default: the pin goes active), "released" or "both".

    For a beam-break sensor with an open-collector output, keep pull_up=True: the beam breaking pulls the pin
    to ground, which is "pressed". bounce_ms is passed to gpiozero for mechanical switches.
    """
    if edge not in ("pressed", "released", "both"):
        raise ValueError('edge is "pressed", "released" or "both"')
    bounce = bounce_ms / 1000.0 if bounce_ms else None
    button = _gpiozero().Button(pin, pull_up=pull_up, bounce_time=bounce)
    channel = device.counter(channel_id, name)

    def count():
        channel.increment()

    if edge in ("pressed", "both"):
        button.when_pressed = count
    if edge in ("released", "both"):
        button.when_released = count
    return _keep(channel, button)


def safety_input(device, channel_id, pin, name=None, pull_up=True, recover_ms=50):
    """A normally-closed stop circuit between the pin and ground: closed is safe, open (pressed, or a broken
    wire) is a stop."""
    button = _gpiozero().Button(pin, pull_up=pull_up)
    channel = device.safety_stop(channel_id, name, recover_ms=recover_ms)

    def update():
        channel.set(button.is_pressed)

    button.when_pressed = update
    button.when_released = update
    update()
    return _keep(channel, button)


def digital_output(device, channel_id, pin, name=None, active_high=True, relay=False):
    """A light, relay or buzzer on a pin. Many relay boards switch on with a low pin: pass active_high=False."""
    out = _gpiozero().DigitalOutputDevice(pin, active_high=active_high, initial_value=False)

    def change(on):
        out.value = 1 if on else 0

    if relay:
        channel = device.relay(channel_id, name, on_change=change)
    else:
        channel = device.digital_out(channel_id, name, on_change=change)
    return _keep(channel, out)


def pwm_output(device, channel_id, pin, name=None, frequency=1000, active_high=True):
    """A PWM output, level 0 to 1."""
    out = _gpiozero().PWMOutputDevice(pin, active_high=active_high, initial_value=0, frequency=frequency)

    def change(level):
        out.value = level

    channel = device.pwm(channel_id, name, on_change=change)
    return _keep(channel, out)
