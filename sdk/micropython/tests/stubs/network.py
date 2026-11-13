"""Stand-in for MicroPython's network module (only what galaxia.py reads for its status)."""

STA_IF = 0


class WLAN:
    def __init__(self, interface=STA_IF):
        self.interface = interface

    def status(self, param=None):
        if param == "rssi":
            return -58
        return 3

    def ipconfig(self, param):
        if param == "addr4":
            return ("10.0.100.61", "255.255.255.0")
        raise ValueError(param)

    def isconnected(self):
        return True
