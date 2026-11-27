// Asking the operating system which addresses this computer has.

package network

import (
	"net"
)

// LocalIpv4Addresses returns the IPv4 addresses this computer has right now, without loopback and link-local ones.
// Only adapters that are up count: on Windows a static address stops existing while its adapter's cable is out.
func LocalIpv4Addresses() []string {
	var addresses []string
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		ifaceAddresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range ifaceAddresses {
			ipNet, ok := address.(*net.IPNet)
			if !ok {
				continue
			}
			if ip := ipNet.IP.To4(); ip != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
				addresses = append(addresses, ip.String())
			}
		}
	}
	return addresses
}
