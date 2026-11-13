// The device transports beyond MQTT and the virtual devices: WebSocket and HTTP, which the web server hands its
// requests to, and USB serial, whose ports are a setting.

package field

import (
	"github.com/DavidMasin/galaxia-fms/device"
	"log"
	"sync"
)

// deviceTransportsMutex keeps two settings reloads at once from adding the transports twice.
var deviceTransportsMutex sync.Mutex

// applyDeviceTransportSettings adds the WebSocket, HTTP and serial transports the first time it runs, from the
// LoadSettings of NewArena (so, like the other transports, they start in Run and a test arena opens nothing), and
// then opens and closes serial ports to match the settings.
func (arena *Arena) applyDeviceTransportSettings() {
	deviceTransportsMutex.Lock()
	defer deviceTransportsMutex.Unlock()
	if arena.DeviceSerial() == nil {
		transports := []device.Transport{device.NewWsTransport(), device.NewHttpTransport(), device.NewSerialTransport()}
		for _, transport := range transports {
			if err := arena.Devices.AddTransport(transport); err != nil {
				log.Printf("Failed to start the %s device transport: %v", transport.Name(), err)
			}
		}
	}
	arena.ConfigureDeviceSerialPorts()
}

// ConfigureDeviceSerialPorts opens the serial ports the settings list for devices and closes the others, at once.
func (arena *Arena) ConfigureDeviceSerialPorts() {
	if serial := arena.DeviceSerial(); serial != nil {
		serial.Configure(arena.EventSettings.SerialPorts())
	}
}

// DeviceWebsocket returns the transport of devices that connect over WebSocket.
func (arena *Arena) DeviceWebsocket() *device.WsTransport {
	transport, _ := arena.Devices.Transport(device.WsTransportName).(*device.WsTransport)
	return transport
}

// DeviceHttp returns the transport of devices that connect over plain HTTP.
func (arena *Arena) DeviceHttp() *device.HttpTransport {
	transport, _ := arena.Devices.Transport(device.HttpTransportName).(*device.HttpTransport)
	return transport
}

// DeviceSerial returns the transport of devices on USB serial ports.
func (arena *Arena) DeviceSerial() *device.SerialTransport {
	transport, _ := arena.Devices.Transport(device.SerialTransportName).(*device.SerialTransport)
	return transport
}
