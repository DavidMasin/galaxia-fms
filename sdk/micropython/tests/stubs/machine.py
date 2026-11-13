"""Stand-in for MicroPython's machine module, for running galaxia.py under CPython."""


class Pin:
    IN = 0
    OUT = 1
    PULL_UP = 1
    PULL_DOWN = 2
    IRQ_FALLING = 4
    IRQ_RISING = 8

    pins = {}

    def __init__(self, pin_id, mode=None, pull=None, value=None):
        self.id = pin_id
        self.mode = mode
        self.pull = pull
        self.handler = None
        self.trigger = 0
        if value is not None:
            self._value = value
        else:
            self._value = 1 if pull == Pin.PULL_UP else 0
        Pin.pins[pin_id] = self

    def value(self, v=None):
        if v is None:
            return self._value
        self._value = 1 if v else 0
        return None

    def irq(self, handler=None, trigger=0):
        self.handler = handler
        self.trigger = trigger

    def drive(self, level):
        """Test helper: the outside world sets the pin level, firing the interrupt like the hardware would."""
        old = self._value
        self._value = 1 if level else 0
        if self.handler is None or old == self._value:
            return
        if (self._value == 0 and self.trigger & Pin.IRQ_FALLING) or (self._value == 1 and self.trigger & Pin.IRQ_RISING):
            self.handler(self)


class PWM:
    def __init__(self, pin):
        self.pin = pin
        self.frequency = None
        self.duty = None

    def freq(self, value):
        self.frequency = value

    def duty_u16(self, value):
        self.duty = value


resets = []


def reset():
    resets.append(True)


def unique_id():
    return b"\x01\x02\x03\x04"
