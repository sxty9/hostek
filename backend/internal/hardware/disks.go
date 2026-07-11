package hardware

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"

	"github.com/shirou/gopsutil/v4/disk"

	"hostek/internal/diskutil"
)

// DiskPartition is one mounted (or mountable) partition under a whole disk.
type DiskPartition struct {
	Name      string  `json:"name"`
	Mount     string  `json:"mount,omitempty"`
	Fstype    string  `json:"fstype,omitempty"`
	SizeBytes uint64  `json:"sizeBytes"`
	Used      uint64  `json:"used,omitempty"`
	Total     uint64  `json:"total,omitempty"`
	Percent   float64 `json:"percent,omitempty"`
}

// DiskDevice is a whole physical disk and its partitions (for the Disks tab).
type DiskDevice struct {
	Name       string `json:"name"` // "nvme0n1", "sda"
	Model      string `json:"model,omitempty"`
	Serial     string `json:"serial,omitempty"`
	Transport  string `json:"transport,omitempty"` // "nvme"/"sata"/"usb"
	Port       string `json:"port,omitempty"`      // friendly, e.g. "SATA Port 3" / "NVMe"
	SizeBytes  uint64 `json:"sizeBytes"`
	Rotational bool   `json:"rotational"`
	Type       string `json:"type,omitempty"` // "NVMe"/"SSD"/"HDD"
	IsSystem   bool   `json:"isSystem"`
	// SMART (best-effort, from the ~30s cache) — symmetric with the System-tab disk card.
	// Embedded so health/tempC/firmware/powerOnHours/lifePercent/… serialize inline.
	SmartHealth
	Partitions []DiskPartition `json:"partitions,omitempty"`
}

// lsblkNode mirrors the subset of `lsblk -J` fields we consume; children are the
// node's partitions. Size is bytes (lsblk -b), Rota is "0"/"1" (lsblk emits it
// as a string under -b, sometimes a bool under newer versions — handle both).
type lsblkNode struct {
	Name       string      `json:"name"`
	Model      string      `json:"model"`
	Serial     string      `json:"serial"`
	Tran       string      `json:"tran"`
	Rota       flexBool    `json:"rota"`
	Size       uint64      `json:"size"`
	Type       string      `json:"type"`
	Mountpoint string      `json:"mountpoint"`
	Fstype     string      `json:"fstype"`
	Children   []lsblkNode `json:"children"`
}

// flexBool decodes lsblk's rotational flag whether it arrives as a JSON bool,
// the string "0"/"1", or "true"/"false".
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	*b = flexBool(s == "1" || s == "true")
	return nil
}

// lsblkDev is the flattened whole-disk view used by the System-disk probe.
type lsblkDev struct {
	Name   string
	Model  string
	Serial string
	Tran   string
	Rota   bool
	Size   uint64
}

// lsblkDevices runs lsblk for a single whole disk and returns its flattened row.
// With args==nil it queries that one device path; callers pass "/dev/<name>".
func lsblkDevices(dev string) ([]lsblkDev, bool) {
	out, ok := runCmd(cmdTimeout, "lsblk", "-J", "-b", "-d", "-o", "NAME,MODEL,SERIAL,TRAN,ROTA,SIZE", dev)
	if !ok {
		return nil, false
	}
	var doc struct {
		Blockdevices []lsblkNode `json:"blockdevices"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return nil, false
	}
	out2 := make([]lsblkDev, 0, len(doc.Blockdevices))
	for _, n := range doc.Blockdevices {
		out2 = append(out2, lsblkDev{
			Name:   n.Name,
			Model:  strings.TrimSpace(n.Model),
			Serial: strings.TrimSpace(n.Serial),
			Tran:   n.Tran,
			Rota:   bool(n.Rota),
			Size:   n.Size,
		})
	}
	return out2, true
}

// Disks returns every whole physical disk with its partitions. It is computed
// live (it runs lsblk) — cheap enough since callers poll only every few seconds.
func (c *Collector) Disks() []DiskDevice {
	out, ok := runCmd(cmdTimeout, "lsblk", "-J", "-b", "-o",
		"NAME,MODEL,SERIAL,TRAN,ROTA,SIZE,TYPE,MOUNTPOINT,FSTYPE")
	if !ok {
		return nil
	}
	var doc struct {
		Blockdevices []lsblkNode `json:"blockdevices"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return nil
	}

	root := diskutil.RootDevice() // whole-disk name backing "/"
	c.mu.RLock()
	smart := c.smart // copy-on-write snapshot; safe to read after unlock
	c.mu.RUnlock()

	var devices []DiskDevice
	for _, n := range doc.Blockdevices {
		// Only real whole disks — skip loop/rom/ram pseudo-devices.
		if n.Type != "disk" {
			continue
		}
		d := DiskDevice{
			Name:       n.Name,
			Model:      strings.TrimSpace(n.Model),
			Serial:     strings.TrimSpace(n.Serial),
			Transport:  n.Tran,
			SizeBytes:  n.Size,
			Rotational: bool(n.Rota),
			Type:       diskListType(n.Tran, bool(n.Rota)),
			Port:       portLabel(n.Tran, n.Name),
			IsSystem:   root != "" && n.Name == root,
		}
		if sd, ok := smart[n.Name]; ok {
			d.SmartHealth = sd
		}
		for _, ch := range n.Children {
			d.Partitions = append(d.Partitions, partition(ch))
		}
		devices = append(devices, d)
	}
	return devices
}

// wholeDiskNames lists the base names of every whole physical disk (type "disk").
func wholeDiskNames() []string {
	out, ok := runCmd(cmdTimeout, "lsblk", "-J", "-d", "-o", "NAME,TYPE")
	if !ok {
		return nil
	}
	var doc struct {
		Blockdevices []lsblkNode `json:"blockdevices"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return nil
	}
	var names []string
	for _, n := range doc.Blockdevices {
		if n.Type == "disk" {
			names = append(names, n.Name)
		}
	}
	return names
}

// partition builds a DiskPartition, filling live usage when the partition is
// mounted (gopsutil disk.Usage on the mountpoint).
func partition(n lsblkNode) DiskPartition {
	p := DiskPartition{
		Name:      n.Name,
		Mount:     n.Mountpoint,
		Fstype:    n.Fstype,
		SizeBytes: n.Size,
	}
	if n.Mountpoint != "" {
		if u, err := disk.Usage(n.Mountpoint); err == nil && u.Total > 0 {
			p.Used = u.Used
			p.Total = u.Total
			p.Percent = roundMHz(u.UsedPercent*10) / 10 // 1-decimal percent
		}
	}
	return p
}

// diskListType is the short label for the Disks tab ("NVMe"/"SSD"/"HDD").
func diskListType(tran string, rota bool) string {
	if strings.EqualFold(tran, "nvme") {
		return "NVMe"
	}
	if rota {
		return "HDD"
	}
	return "SSD"
}

// portLabel is a friendly connection hint derived from transport + device name.
// For SATA/ATA disks it resolves the physical mainboard port ("SATA Port 3")
// from sysfs; otherwise it falls back to the transport family.
func portLabel(tran, name string) string {
	switch strings.ToLower(tran) {
	case "nvme":
		return "NVMe"
	case "usb":
		return "USB"
	case "sata", "ata":
		if p := sataPort(name); p != "" {
			return "SATA Port " + p
		}
		return "SATA"
	}
	return ""
}

// ataPathRe pulls the ataN segment out of a /sys/block/<disk> link target, e.g.
// ".../ata3/host2/target3:0:0/3:0:0:0/block/sdc" → "ata3".
var ataPathRe = regexp.MustCompile(`ata(\d+)`)

// sataPort resolves the physical mainboard SATA port for a whole-disk name
// ("sda") by walking /sys/block/<name> to its libata ataN link and reading the
// kernel's port_no — the controller-relative hardware port that maps to the
// board's SATA connector. Falls back to the ataN enumeration index when
// port_no is absent, and to "" when the topology can't be resolved at all.
// Best-effort, unprivileged sysfs only.
func sataPort(name string) string {
	link, err := os.Readlink("/sys/block/" + name)
	if err != nil {
		return ""
	}
	m := ataPathRe.FindStringSubmatch(link)
	if m == nil {
		return ""
	}
	if pn := readSysStr("/sys/class/ata_port/" + m[0] + "/port_no"); pn != "" {
		return pn
	}
	return m[1] // ataN index as a last resort
}
