// Package diskutil resolves block-device relationships shared by the metrics and
// hardware collectors — notably which physical device backs the root filesystem.
package diskutil

import (
	"os"
	"regexp"
	"strings"

	"github.com/shirou/gopsutil/v4/disk"
)

// nvme0n1p2 / mmcblk0p1 → the part before "pN". sda1 / vdb3 → the leading letters.
var (
	nvmePart = regexp.MustCompile(`^(nvme\d+n\d+|mmcblk\d+)p\d+$`)
	sdPart   = regexp.MustCompile(`^([a-z]+)\d+$`)
)

// ParentDevice reduces a partition device name to its parent whole-disk name
// (e.g. "/dev/nvme0n1p2" → "nvme0n1", "sda1" → "sda"). If the input already looks
// like a whole device (or can't be reduced), its base name is returned unchanged.
func ParentDevice(dev string) string {
	name := strings.TrimPrefix(dev, "/dev/")
	if m := nvmePart.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	if m := sdPart.FindStringSubmatch(name); m != nil {
		// Confirm it is an actual partition (not e.g. "loop0") before stripping digits.
		if _, err := os.Stat("/sys/class/block/" + name + "/partition"); err == nil {
			return m[1]
		}
	}
	return name
}

// RootDevice returns the parent whole-disk name backing "/" (e.g. "nvme0n1"), or
// "" if it can't be determined. Linux-meaningful; best-effort elsewhere.
func RootDevice() string {
	parts, err := disk.Partitions(false)
	if err != nil {
		return ""
	}
	for _, p := range parts {
		if p.Mountpoint == "/" {
			return ParentDevice(p.Device)
		}
	}
	return ""
}
