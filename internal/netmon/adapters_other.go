//go:build !windows

package netmon

import (
	"net"
	"os"
	"strings"
)

// listAdapters returns up, non-loopback interfaces with an IPv4 address.
// The probe in Scan then decides which ones actually reach the internet.
func listAdapters() []Adapter {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []Adapter
	for _, it := range ifaces {
		if it.Flags&net.FlagUp == 0 || it.Flags&net.FlagLoopback != 0 || isVirtual(it.Name, "") {
			continue
		}
		addrs, _ := it.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil || ip.IsLinkLocalUnicast() {
				continue
			}
			_, wireless := os.Stat("/sys/class/net/" + it.Name + "/wireless")
			out = append(out, Adapter{Name: it.Name, IP: ip.String(),
				Kind: kindFor(it.Name, "", wireless == nil || strings.HasPrefix(it.Name, "wl"))})
			break
		}
	}
	return out
}
