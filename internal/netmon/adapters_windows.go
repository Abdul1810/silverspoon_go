//go:build windows

package netmon

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// listAdapters returns up adapters that have an IPv4 address and a default
// gateway, via GetAdaptersAddresses (which also tells us the driver, so a
// USB tether is recognised as one).
func listAdapters() []Adapter {
	const flags = windows.GAA_FLAG_INCLUDE_GATEWAYS | windows.GAA_FLAG_SKIP_ANYCAST |
		windows.GAA_FLAG_SKIP_MULTICAST | windows.GAA_FLAG_SKIP_DNS_SERVER
	size := uint32(16 * 1024)
	var buf []byte
	for i := 0; i < 3; i++ {
		buf = make([]byte, size)
		err := windows.GetAdaptersAddresses(windows.AF_INET, flags, 0,
			(*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if err == nil {
			break
		}
		if err != windows.ERROR_BUFFER_OVERFLOW {
			return nil
		}
	}
	var out []Adapter
	for a := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); a != nil; a = a.Next {
		if a.OperStatus != windows.IfOperStatusUp || a.IfType == windows.IF_TYPE_SOFTWARE_LOOPBACK {
			continue
		}
		if a.FirstGatewayAddress == nil {
			continue // no route to anywhere beyond the local link
		}
		name := windows.UTF16PtrToString(a.FriendlyName)
		desc := windows.UTF16PtrToString(a.Description)
		if isVirtual(name, desc) {
			continue
		}
		gw := a.FirstGatewayAddress.Address.IP()
		for u := a.FirstUnicastAddress; u != nil; u = u.Next {
			ip := u.Address.IP().To4()
			if ip == nil || ip.IsLinkLocalUnicast() || ip.IsLoopback() {
				continue
			}
			ad := Adapter{Name: name, Description: desc, IP: ip.String(),
				Kind: kindFor(name, desc, a.IfType == windows.IF_TYPE_IEEE80211), LinkMbps: a.ReceiveLinkSpeed / 1_000_000}
			if gw != nil {
				ad.Gateway = gw.String()
			}
			out = append(out, ad)
			break
		}
	}
	return out
}
