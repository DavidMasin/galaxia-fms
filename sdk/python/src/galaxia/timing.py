"""The field clock and scheduled ("at") output commands."""


class FieldClock:
    """Estimates the FMS clock from field/clock messages.

    Each message gives one sample of (FMS time - local time). Network delay can only make a sample too small, so
    the estimate is the largest sample of the last WINDOW. A sample more than JUMP_MS away from the estimate is
    treated as an outlier, unless OUTLIERS of them arrive in a row, which means the FMS clock itself jumped.
    """

    WINDOW = 8
    JUMP_MS = 1000
    OUTLIERS = 3

    def __init__(self, monotonic_ms):
        self._monotonic_ms = monotonic_ms
        self._samples = []
        self._outliers = []
        self._offset = None

    @property
    def synced(self):
        return self._offset is not None

    @property
    def offset(self):
        return self._offset

    def sample(self, field_ms):
        offset = int(field_ms) - self._monotonic_ms()
        if self._offset is not None and abs(offset - self._offset) > self.JUMP_MS:
            self._outliers.append(offset)
            if len(self._outliers) < self.OUTLIERS:
                return
            self._samples = self._outliers
            self._outliers = []
        else:
            self._outliers = []
            self._samples.append(offset)
        self._samples = self._samples[-self.WINDOW:]
        self._offset = max(self._samples)

    def now(self):
        """FMS time in UTC milliseconds, or None before the first field/clock message."""
        if self._offset is None:
            return None
        return self._monotonic_ms() + self._offset


class Schedule:
    """Output commands waiting for their "at" time.

    A new command replaces every pending command due at or after its own time, so an immediate command cancels
    everything pending (a cancelled countdown, say), while commands sent in advance for successive moments queue up.
    """

    def __init__(self, size):
        self.size = size
        self._pending = []
        self.dropped = 0

    def __len__(self):
        return len(self._pending)

    def offer(self, at, now, command):
        """Returns True if the command is due now; otherwise keeps it for later."""
        if at is None or now is None or at <= now:
            self._pending = []
            return True
        self._pending = [entry for entry in self._pending if entry[0] < at]
        self._pending.append((at, command))
        if len(self._pending) > self.size:
            self._pending.pop(0)
            self.dropped += 1
        return False

    def due(self, now):
        """Removes and returns the commands whose time has come, oldest first."""
        if now is None:
            ready = self._pending
            self._pending = []
            return ready
        count = 0
        while count < len(self._pending) and self._pending[count][0] <= now:
            count += 1
        ready = self._pending[:count]
        self._pending = self._pending[count:]
        return ready

    def clear(self):
        self._pending = []
