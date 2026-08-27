// Package winapi wraps the Win32 calls wininfopanel needs. Everything here
// binds against the OS directly through x/sys/windows: the project is Windows
// 11 only and builds without cgo, so there is no C shim anywhere.
package winapi

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SharedMemory is a read-only view of a named shared-memory section created by
// another process.
//
// HWiNFO publishes its sensor table this way, and plugin image buffers use the
// same mechanism in the other direction.
type SharedMemory struct {
	handle windows.Handle
	addr   uintptr
	data   []byte
}

// OpenSharedMemory maps an existing named section for reading.
//
// The name is the full object name, including any namespace prefix, e.g.
// `Global\HWiNFO_SENS_SM2`. A section that does not exist is reported as an
// error rather than a nil result: callers distinguish "not running yet" by
// inspecting the error, and retry.
func OpenSharedMemory(name string) (*SharedMemory, error) {
	utf16Name, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, fmt.Errorf("encode section name %q: %w", name, err)
	}

	handle, err := openFileMapping(windows.FILE_MAP_READ, false, utf16Name)
	if err != nil {
		return nil, fmt.Errorf("open shared memory %q: %w", name, err)
	}

	// A zero size maps the whole section, whose extent we do not know up front.
	addr, err := windows.MapViewOfFile(handle, windows.FILE_MAP_READ, 0, 0, 0)
	if err != nil {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("map shared memory %q: %w", name, err)
	}

	size, err := regionSize(addr)
	if err != nil {
		windows.UnmapViewOfFile(addr)
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("size shared memory %q: %w", name, err)
	}

	// The view is built once here rather than on each Bytes call. go vet's
	// unsafeptr check flags this conversion because it cannot know the address
	// refers to real, stable memory outside the Go heap, which is exactly what
	// a mapped view is. The analyzer is disabled in build.ps1 for that reason;
	// every other vet check stays on.
	data := unsafe.Slice((*byte)(unsafe.Pointer(addr)), size)

	return &SharedMemory{handle: handle, addr: addr, data: data}, nil
}

// regionSize asks the memory manager how large the mapped view is, since
// MapViewOfFile does not report it.
func regionSize(addr uintptr) (uintptr, error) {
	var info windows.MemoryBasicInformation
	if err := windows.VirtualQuery(addr, &info, unsafe.Sizeof(info)); err != nil {
		return 0, err
	}
	if info.RegionSize == 0 {
		return 0, fmt.Errorf("mapped view reports a zero-length region")
	}
	return info.RegionSize, nil
}

// Size returns the length of the mapped view in bytes.
func (m *SharedMemory) Size() int { return len(m.data) }

// Bytes returns the mapped region as a byte slice.
//
// The slice aliases memory owned by the publishing process: it is valid only
// until Close, and its contents can change underneath the reader at any time.
// Copy anything that must stay stable.
func (m *SharedMemory) Bytes() []byte {
	if m == nil {
		return nil
	}
	return m.data
}

// Close unmaps the view and releases the section handle.
func (m *SharedMemory) Close() error {
	if m == nil || m.addr == 0 {
		return nil
	}

	unmapErr := windows.UnmapViewOfFile(m.addr)
	closeErr := windows.CloseHandle(m.handle)
	m.addr = 0
	m.handle = 0
	m.data = nil

	if unmapErr != nil {
		return fmt.Errorf("unmap shared memory: %w", unmapErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close shared memory handle: %w", closeErr)
	}
	return nil
}
