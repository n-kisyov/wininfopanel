package winapi

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// x/sys/windows exposes CreateFileMapping but not OpenFileMapping, so it is
// bound here. Lazy system DLL loading keeps the binary cgo-free.
var (
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procOpenFileMappingW = kernel32.NewProc("OpenFileMappingW")
)

// openFileMapping opens an existing named file-mapping object.
//
// It returns a wrapped error when the section does not exist, which is the
// ordinary case when the publishing application is not running yet.
func openFileMapping(access uint32, inheritHandle bool, name *uint16) (windows.Handle, error) {
	var inherit uintptr
	if inheritHandle {
		inherit = 1
	}

	handle, _, err := procOpenFileMappingW.Call(
		uintptr(access),
		inherit,
		uintptr(unsafe.Pointer(name)),
	)
	if handle == 0 {
		if err == nil {
			return 0, fmt.Errorf("OpenFileMapping failed with no error code")
		}
		return 0, err
	}
	return windows.Handle(handle), nil
}
