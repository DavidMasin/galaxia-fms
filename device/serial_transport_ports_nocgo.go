//go:build darwin && !cgo

// Listing the serial ports of a Mac built without cgo: the library reads USB details through IOKit, which needs cgo,
// so the ports are listed by name alone. They can still be opened.

package device

import (
	"go.bug.st/serial"
)

func listSerialPorts() ([]SerialPortInfo, error) {
	names, err := serial.GetPortsList()
	if err != nil {
		return nil, err
	}
	ports := make([]SerialPortInfo, 0, len(names))
	for _, name := range names {
		ports = append(ports, SerialPortInfo{Name: name})
	}
	return ports, nil
}
