//go:build !darwin || cgo

// Listing the serial ports of the computer with their USB details. On macOS the library reads those through IOKit,
// which needs cgo; serial_transport_ports_nocgo.go lists the names alone there.

package device

import (
	"go.bug.st/serial/enumerator"
)

func listSerialPorts() ([]SerialPortInfo, error) {
	// Without probe filters the library asks no device for its strings, which can disturb a device that is running;
	// what the system already knows (on Windows, the port's friendly name as the product) is enough to tell ports
	// apart.
	details, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return nil, err
	}
	ports := make([]SerialPortInfo, 0, len(details))
	for _, detail := range details {
		ports = append(ports, SerialPortInfo{
			Name: detail.Name, USB: detail.IsUSB, VID: detail.VID, PID: detail.PID, SerialNumber: detail.SerialNumber,
			Manufacturer: detail.Manufacturer, Product: detail.Product,
		})
	}
	return ports, nil
}
