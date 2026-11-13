"""Stand-in for MicroPython's neopixel module."""

strips = []


class NeoPixel:
    def __init__(self, pin, n):
        self.pin = pin
        self.n = n
        self.buffer = [(0, 0, 0)] * n
        self.shown = None
        self.writes = 0
        strips.append(self)

    def __len__(self):
        return self.n

    def __setitem__(self, index, color):
        self.buffer[index] = tuple(color)

    def __getitem__(self, index):
        return self.buffer[index]

    def write(self):
        self.shown = list(self.buffer)
        self.writes += 1
