// Package usb enumerates and talks to USB devices through Windows' WinUSB
// driver, without cgo or libusb.
//
// The API surface WinUSB needs is small -- find the device path, open it,
// write to a pipe, read from a pipe -- so it is bound directly rather than
// pulling in a C library and the toolchain that comes with it.
package usb

import (
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	setupapi = windows.NewLazySystemDLL("setupapi.dll")

	procSetupDiGetClassDevsW              = setupapi.NewProc("SetupDiGetClassDevsW")
	procSetupDiEnumDeviceInterfaces       = setupapi.NewProc("SetupDiEnumDeviceInterfaces")
	procSetupDiGetDeviceInterfaceDetailW  = setupapi.NewProc("SetupDiGetDeviceInterfaceDetailW")
	procSetupDiDestroyDeviceInfoList      = setupapi.NewProc("SetupDiDestroyDeviceInfoList")
	procSetupDiGetDeviceRegistryPropertyW = setupapi.NewProc("SetupDiGetDeviceRegistryPropertyW")
	procSetupDiGetDeviceInstanceIdW       = setupapi.NewProc("SetupDiGetDeviceInstanceIdW")
	procSetupDiGetDevicePropertyW         = setupapi.NewProc("SetupDiGetDevicePropertyW")
	procSetupDiOpenDevRegKey              = setupapi.NewProc("SetupDiOpenDevRegKey")
)

// SetupDiGetClassDevs flags.
const (
	digcfPresent         = 0x00000002
	digcfDeviceInterface = 0x00000010
)

// Device registry properties.
const (
	spdrpDeviceDesc   = 0x00000000
	spdrpFriendlyName = 0x0000000C
	spdrpLocationInfo = 0x0000000D
)

// SetupDiOpenDevRegKey scopes.
const (
	dicsFlagGlobal = 0x00000001
	diregDev       = 0x00000001
)

// devPropKey is DEVPROPKEY, addressing a device property by GUID and index.
type devPropKey struct {
	fmtid windows.GUID
	pid   uint32
}

// devPKeyDeviceBusReportedDeviceDesc is the name a device reports for itself
// over the bus, as opposed to the one its driver supplies.
//
// It is the only field that distinguishes one CDC-ACM device from another:
// Windows binds them all to usbser.sys and describes every one of them as
// "USB Serial Device", while the device's own answer is a real model name.
var devPKeyDeviceBusReportedDeviceDesc = devPropKey{
	fmtid: windows.GUID{
		Data1: 0x540B947E,
		Data2: 0x8B40,
		Data3: 0x45BC,
		Data4: [8]byte{0xA8, 0xA2, 0x6A, 0x0B, 0x89, 0x4C, 0xBD, 0xA2},
	},
	pid: 4,
}

// guidDeviceInterfaceUSB is GUID_DEVINTERFACE_USB_DEVICE, the interface class
// WinUSB-bound devices expose.
var guidDeviceInterfaceUSB = windows.GUID{
	Data1: 0xA5DCBF10,
	Data2: 0x6530,
	Data3: 0x11D2,
	Data4: [8]byte{0x90, 0x1F, 0x00, 0xC0, 0x4F, 0xB9, 0x51, 0xED},
}

// spDeviceInterfaceData is SP_DEVICE_INTERFACE_DATA.
type spDeviceInterfaceData struct {
	Size               uint32
	InterfaceClassGuid windows.GUID
	Flags              uint32
	Reserved           uintptr
}

// spDevinfoData is SP_DEVINFO_DATA.
type spDevinfoData struct {
	Size      uint32
	ClassGuid windows.GUID
	DevInst   uint32
	Reserved  uintptr
}

// DeviceInfo describes one enumerated USB device.
type DeviceInfo struct {
	// Path is the device interface path, which CreateFile opens.
	Path string
	// InstanceID is the device instance identifier, e.g.
	// `USB\VID_4E58&PID_1001\0123456789`. It is stable for a given device on a
	// given port, which is what lets a panel keep its settings across
	// reconnects.
	InstanceID string

	VendorID  uint16
	ProductID uint16

	// Description is the driver-reported device description.
	Description string
	// Location describes where the device is attached, e.g. "Port_#0002.Hub_#0003".
	Location string
	// Serial is the serial number embedded in the instance ID, when the device
	// reports one. A device without one gets a port-derived identifier from
	// Windows instead, which changes if it is moved.
	Serial string

	// BusDescription is the name the device reports for itself, which for a
	// serial panel is the only thing that names the model: its driver-supplied
	// Description is the generic "USB Serial Device".
	BusDescription string
	// PortName is the COM port a device bound to a serial class driver is
	// reachable through, e.g. "COM3". It is empty for every other device.
	PortName string
}

// IsSerial reports whether the device is exposed as a serial port rather than
// through WinUSB. Such a device cannot be opened by this package -- it is
// driven over its COM port instead.
func (d DeviceInfo) IsSerial() bool { return d.PortName != "" }

// Enumerate lists the present USB devices exposing the WinUSB device
// interface.
//
// Devices bound to a class driver rather than WinUSB do not appear here; that
// is the correct behaviour, since they cannot be driven this way.
func Enumerate() ([]DeviceInfo, error) {
	handle, _, err := procSetupDiGetClassDevsW.Call(
		uintptr(unsafe.Pointer(&guidDeviceInterfaceUSB)),
		0, 0,
		digcfPresent|digcfDeviceInterface,
	)
	if handle == uintptr(windows.InvalidHandle) {
		return nil, fmt.Errorf("enumerate USB devices: %w", err)
	}
	defer procSetupDiDestroyDeviceInfoList.Call(handle)

	var devices []DeviceInfo

	for index := uint32(0); ; index++ {
		interfaceData := spDeviceInterfaceData{Size: uint32(unsafe.Sizeof(spDeviceInterfaceData{}))}

		ret, _, _ := procSetupDiEnumDeviceInterfaces.Call(
			handle, 0,
			uintptr(unsafe.Pointer(&guidDeviceInterfaceUSB)),
			uintptr(index),
			uintptr(unsafe.Pointer(&interfaceData)),
		)
		if ret == 0 {
			break // no more interfaces
		}

		device, ok := describeInterface(handle, &interfaceData)
		if !ok {
			continue
		}
		devices = append(devices, device)
	}

	return devices, nil
}

// describeInterface resolves one device interface to a path and its metadata.
func describeInterface(handle uintptr, interfaceData *spDeviceInterfaceData) (DeviceInfo, bool) {
	// The detail structure is variable length, so its size is queried first.
	var required uint32
	procSetupDiGetDeviceInterfaceDetailW.Call(
		handle,
		uintptr(unsafe.Pointer(interfaceData)),
		0, 0,
		uintptr(unsafe.Pointer(&required)),
		0,
	)
	if required == 0 {
		return DeviceInfo{}, false
	}

	buffer := make([]byte, required)
	detail := (*struct {
		Size uint32
		// DevicePath follows immediately, as a variable-length UTF-16 string.
	})(unsafe.Pointer(&buffer[0]))

	// cbSize describes the fixed part only, and its value differs by
	// architecture: 8 on 64-bit, 6 on 32-bit. This build is 64-bit only.
	detail.Size = 8

	devInfo := spDevinfoData{Size: uint32(unsafe.Sizeof(spDevinfoData{}))}

	ret, _, _ := procSetupDiGetDeviceInterfaceDetailW.Call(
		handle,
		uintptr(unsafe.Pointer(interfaceData)),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(required),
		uintptr(unsafe.Pointer(&required)),
		uintptr(unsafe.Pointer(&devInfo)),
	)
	if ret == 0 {
		return DeviceInfo{}, false
	}

	// The path starts after the 4-byte size field, padded to pointer alignment.
	const pathOffset = 4
	path := windows.UTF16PtrToString((*uint16)(unsafe.Pointer(&buffer[pathOffset])))
	if path == "" {
		return DeviceInfo{}, false
	}

	device := DeviceInfo{
		Path:           path,
		InstanceID:     instanceID(handle, &devInfo),
		Description:    registryString(handle, &devInfo, spdrpDeviceDesc),
		Location:       registryString(handle, &devInfo, spdrpLocationInfo),
		BusDescription: deviceProperty(handle, &devInfo, &devPKeyDeviceBusReportedDeviceDesc),
		PortName:       portName(handle, &devInfo),
	}

	// The vendor and product identifiers are embedded in the path, so they can
	// be read without opening the device.
	device.VendorID, device.ProductID, _ = parseVIDPID(path)
	device.Serial = parseSerial(device.InstanceID)

	return device, true
}

// instanceID reads a device's instance identifier.
func instanceID(handle uintptr, devInfo *spDevinfoData) string {
	buffer := make([]uint16, 512)
	var required uint32

	ret, _, _ := procSetupDiGetDeviceInstanceIdW.Call(
		handle,
		uintptr(unsafe.Pointer(devInfo)),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
		uintptr(unsafe.Pointer(&required)),
	)
	if ret == 0 {
		return ""
	}
	return windows.UTF16ToString(buffer)
}

// registryString reads a device registry property as text.
func registryString(handle uintptr, devInfo *spDevinfoData, property uint32) string {
	buffer := make([]byte, 512)
	var propertyType, required uint32

	ret, _, _ := procSetupDiGetDeviceRegistryPropertyW.Call(
		handle,
		uintptr(unsafe.Pointer(devInfo)),
		uintptr(property),
		uintptr(unsafe.Pointer(&propertyType)),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
		uintptr(unsafe.Pointer(&required)),
	)
	if ret == 0 {
		return ""
	}
	return windows.UTF16PtrToString((*uint16)(unsafe.Pointer(&buffer[0])))
}

// deviceProperty reads a DEVPKEY string property.
func deviceProperty(handle uintptr, devInfo *spDevinfoData, key *devPropKey) string {
	buffer := make([]byte, 512)
	var propertyType, required uint32

	ret, _, _ := procSetupDiGetDevicePropertyW.Call(
		handle,
		uintptr(unsafe.Pointer(devInfo)),
		uintptr(unsafe.Pointer(key)),
		uintptr(unsafe.Pointer(&propertyType)),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
		uintptr(unsafe.Pointer(&required)),
		0,
	)
	if ret == 0 {
		return ""
	}
	return windows.UTF16PtrToString((*uint16)(unsafe.Pointer(&buffer[0])))
}

// portName reads the COM port assigned to a device, from the same registry key
// Device Manager shows it from. A device that is not a serial port has no such
// value, which is how this distinguishes the two.
func portName(handle uintptr, devInfo *spDevinfoData) string {
	key, _, _ := procSetupDiOpenDevRegKey.Call(
		handle,
		uintptr(unsafe.Pointer(devInfo)),
		dicsFlagGlobal,
		0,
		diregDev,
		windows.KEY_READ,
	)
	if key == 0 || key == uintptr(windows.InvalidHandle) {
		return ""
	}
	defer windows.RegCloseKey(windows.Handle(key))

	var valueType, size uint32
	buffer := make([]byte, 64)
	size = uint32(len(buffer))

	err := windows.RegQueryValueEx(
		windows.Handle(key),
		windows.StringToUTF16Ptr("PortName"),
		nil,
		&valueType,
		&buffer[0],
		&size,
	)
	if err != nil {
		return ""
	}
	return windows.UTF16PtrToString((*uint16)(unsafe.Pointer(&buffer[0])))
}

// parseVIDPID extracts the vendor and product identifiers from a device path
// or instance ID, both of which spell them as "vid_XXXX&pid_YYYY".
func parseVIDPID(s string) (uint16, uint16, bool) {
	lower := strings.ToLower(s)

	vid, okVID := hexField(lower, "vid_")
	pid, okPID := hexField(lower, "pid_")
	return vid, pid, okVID && okPID
}

// hexField reads the four hex digits following a marker.
func hexField(s, marker string) (uint16, bool) {
	index := strings.Index(s, marker)
	if index < 0 {
		return 0, false
	}

	start := index + len(marker)
	if start+4 > len(s) {
		return 0, false
	}

	value, err := strconv.ParseUint(s[start:start+4], 16, 16)
	if err != nil {
		return 0, false
	}
	return uint16(value), true
}

// parseSerial extracts the serial number from an instance ID.
//
// The last segment is the device's own identifier when it reports a serial
// number. When it does not, Windows synthesises one containing an ampersand,
// which is how the two are told apart -- a synthesised value is tied to the
// port and changes if the device is moved, so it is not a serial number.
func parseSerial(instanceID string) string {
	segments := strings.Split(instanceID, `\`)
	if len(segments) < 3 {
		return ""
	}

	last := segments[len(segments)-1]
	if strings.Contains(last, "&") {
		return ""
	}
	return last
}

// FindByVIDPID returns the present devices matching a vendor and product ID.
func FindByVIDPID(vendorID, productID uint16) ([]DeviceInfo, error) {
	devices, err := Enumerate()
	if err != nil {
		return nil, err
	}

	var matches []DeviceInfo
	for _, device := range devices {
		if device.VendorID == vendorID && device.ProductID == productID {
			matches = append(matches, device)
		}
	}
	return matches, nil
}
