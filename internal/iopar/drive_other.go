//go:build !windows

package iopar

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// On Linux the kernel says whether a block device rotates; an NVMe drive is
// named so. Elsewhere it is not told.
func driveKind(path string) Kind {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return Unknown
	}
	major, minor := (st.Dev>>8)&0xfff, (st.Dev&0xff)|((st.Dev>>12)&0xfff00)
	dev, err := os.Readlink("/sys/dev/block/" + itoa(uint64(major)) + ":" + itoa(uint64(minor)))
	if err != nil {
		return Unknown
	}
	// A partition: its disk is the folder above it.
	disk := filepath.Base(dev)
	sys := filepath.Join("/sys/class/block", disk)
	if _, err := os.Stat(filepath.Join(sys, "partition")); err == nil {
		disk = filepath.Base(filepath.Dir(dev))
		sys = filepath.Join("/sys/class/block", disk)
	}
	if strings.HasPrefix(disk, "nvme") {
		return NVMe
	}
	b, err := os.ReadFile(filepath.Join(sys, "queue", "rotational"))
	if err != nil {
		return Unknown
	}
	if strings.TrimSpace(string(b)) == "1" {
		return HDD
	}
	return SATA
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
