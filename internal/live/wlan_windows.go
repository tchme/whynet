package live

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Wi-Fi through wlanapi: no admin rights needed for the current connection.
var (
	wlanapi                = windows.NewLazySystemDLL("wlanapi.dll")
	procWlanOpenHandle     = wlanapi.NewProc("WlanOpenHandle")
	procWlanCloseHandle    = wlanapi.NewProc("WlanCloseHandle")
	procWlanEnumInterfaces = wlanapi.NewProc("WlanEnumInterfaces")
	procWlanQueryInterface = wlanapi.NewProc("WlanQueryInterface")
	procWlanFreeMemory     = wlanapi.NewProc("WlanFreeMemory")
)

const (
	wlanStateConnected      = 1
	opcodeCurrentConnection = 7
	opcodeChannelNumber     = 8
	opcodeRSSI              = 0x10000102
)

type wlanInterfaceInfo struct {
	GUID        windows.GUID
	Description [256]uint16
	State       uint32
}

// wlanConnectionAttributes is WLAN_CONNECTION_ATTRIBUTES up to its association part.
type wlanConnectionAttributes struct {
	State         uint32
	Mode          uint32
	Profile       [256]uint16
	SSIDLen       uint32
	SSID          [32]byte
	BssType       uint32
	Bssid         [6]byte // read, never reported: the access point's MAC
	_             [2]byte
	PhyType       uint32
	PhyIndex      uint32
	SignalQuality uint32 // 0..100
	RxRate        uint32 // kbit/s
	TxRate        uint32
}

// phyNames are DOT11_PHY_TYPE values as people know them.
var phyNames = map[uint32]string{4: "802.11a", 5: "802.11b", 6: "802.11g", 7: "802.11n (Wi-Fi 4)", 8: "802.11ac (Wi-Fi 5)", 10: "802.11ax (Wi-Fi 6)", 11: "802.11be (Wi-Fi 7)"}

// ReadWiFi is the Wi-Fi link of the adapter with this GUID right now (the health check); ok is
// false when it is not connected.
func ReadWiFi(guid string) (w WiFi, ok bool, err error) { return systemWiFi(guid) }

// systemWiFi reads the Wi-Fi connection of the adapter with this GUID ("{...}"); with an empty
// GUID, the first connected one. ok is false when there is no connected Wi-Fi.
func systemWiFi(guid string) (w WiFi, ok bool, err error) {
	var negotiated uint32
	var h windows.Handle
	if r, _, _ := procWlanOpenHandle.Call(2, 0, uintptr(unsafe.Pointer(&negotiated)), uintptr(unsafe.Pointer(&h))); r != 0 {
		return w, false, fmt.Errorf("WlanOpenHandle: %w", windows.Errno(r))
	}
	defer procWlanCloseHandle.Call(uintptr(h), 0)

	var list unsafe.Pointer
	if r, _, _ := procWlanEnumInterfaces.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&list))); r != 0 {
		return w, false, fmt.Errorf("WlanEnumInterfaces: %w", windows.Errno(r))
	}
	defer procWlanFreeMemory.Call(uintptr(list))
	n := *(*uint32)(list)
	infos := unsafe.Slice((*wlanInterfaceInfo)(unsafe.Add(list, 8)), n) // after dwNumberOfItems and dwIndex

	for i := range infos {
		info := &infos[i]
		if info.State != wlanStateConnected || guid != "" && !strings.EqualFold(info.GUID.String(), guid) {
			continue
		}
		var size, valueType uint32
		var data unsafe.Pointer
		if r, _, _ := procWlanQueryInterface.Call(uintptr(h), uintptr(unsafe.Pointer(&info.GUID)), opcodeCurrentConnection, 0,
			uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&valueType))); r != 0 {
			return w, false, fmt.Errorf("WlanQueryInterface: %w", windows.Errno(r))
		}
		ca := (*wlanConnectionAttributes)(data)
		w = WiFi{
			Signal: int(ca.SignalQuality),
			PHY:    phyNames[ca.PhyType],
			RxMbps: int(ca.RxRate / 1000),
			TxMbps: int(ca.TxRate / 1000),
			SSID:   string(ca.SSID[:min(int(ca.SSIDLen), len(ca.SSID))]),
		}
		procWlanFreeMemory.Call(uintptr(data))
		if v, ok := queryUint32(h, &info.GUID, opcodeChannelNumber); ok {
			w.Channel = int(v)
			w.Band = band(w.Channel)
		}
		if v, ok := queryUint32(h, &info.GUID, opcodeRSSI); ok {
			w.RSSI = int(int32(v))
		}
		return w, true, nil
	}
	return w, false, nil
}

func queryUint32(h windows.Handle, guid *windows.GUID, opcode uint32) (uint32, bool) {
	var size, valueType uint32
	var data unsafe.Pointer
	if r, _, _ := procWlanQueryInterface.Call(uintptr(h), uintptr(unsafe.Pointer(guid)), uintptr(opcode), 0,
		uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&valueType))); r != 0 || data == nil || size < 4 {
		return 0, false
	}
	defer procWlanFreeMemory.Call(uintptr(data))
	return *(*uint32)(data), true
}
