//go:build windows

package iopar

import (
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Asked of the volume a path is on, as Windows' own optimiser asks it: does
// it incur a seek penalty (a spinning disk), and over which bus is it
// attached (NVMe or otherwise).

const (
	ioctlStorageQueryProperty = 0x002D1400

	storageDeviceProperty            = 0
	storageDeviceSeekPenaltyProperty = 7
	propertyStandardQuery            = 0

	busTypeNvme = 17
)

type storagePropertyQuery struct {
	PropertyID uint32
	QueryType  uint32
	Additional [1]byte
}

type deviceSeekPenaltyDescriptor struct {
	Version           uint32
	Size              uint32
	IncursSeekPenalty byte
}

type storageDeviceDescriptor struct {
	Version               uint32
	Size                  uint32
	DeviceType            byte
	DeviceTypeModifier    byte
	RemovableMedia        byte
	CommandQueueing       byte
	VendorIDOffset        uint32
	ProductIDOffset       uint32
	ProductRevisionOffset uint32
	SerialNumberOffset    uint32
	BusType               uint32
	RawPropertiesLength   uint32
}

func driveKind(path string) Kind {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Unknown
	}
	vol := filepath.VolumeName(abs)
	if len(vol) != 2 || vol[1] != ':' {
		return Unknown // a network share or the like: no drive to ask
	}
	name, err := windows.UTF16PtrFromString(`\\.\` + strings.ToUpper(vol))
	if err != nil {
		return Unknown
	}
	// No access rights are needed to ask these properties.
	h, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return Unknown
	}
	defer windows.CloseHandle(h)

	var seek deviceSeekPenaltyDescriptor
	if query(h, storageDeviceSeekPenaltyProperty, unsafe.Pointer(&seek), uint32(unsafe.Sizeof(seek))) && seek.IncursSeekPenalty != 0 {
		return HDD
	}
	var dev storageDeviceDescriptor
	if query(h, storageDeviceProperty, unsafe.Pointer(&dev), uint32(unsafe.Sizeof(dev))) {
		if dev.BusType == busTypeNvme {
			return NVMe
		}
		return SATA
	}
	return Unknown
}

func query(h windows.Handle, property uint32, out unsafe.Pointer, size uint32) bool {
	q := storagePropertyQuery{PropertyID: property, QueryType: propertyStandardQuery}
	var returned uint32
	err := windows.DeviceIoControl(h, ioctlStorageQueryProperty, (*byte)(unsafe.Pointer(&q)), uint32(unsafe.Sizeof(q)),
		(*byte)(out), size, &returned, nil)
	return err == nil && returned >= 8
}
