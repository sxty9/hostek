// Package hardware probes the machine for CPU-Z-level static detail (CPU, RAM
// modules, mainboard, GPUs, the system disk, NICs) and merges in live dynamic
// values (per-core clocks, GPU clocks/temp/power). Everything is best-effort:
// missing tools or permissions simply leave the corresponding fields zero/empty,
// and nothing here ever panics or returns a fatal error. Privileged tools
// (dmidecode/smartctl/decode-dimms) are reached through the sudo wrapper
// /usr/local/sbin/hostek-hwinfo; the rest is unprivileged sysfs/lscpu/nvidia-smi.
package hardware

import (
	"bufio"
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"

	"hostek/internal/diskutil"
)

const hwinfoWrapper = "/usr/local/sbin/hostek-hwinfo"

const (
	staticInterval  = 10 * time.Minute
	dynamicInterval = 2 * time.Second
	smartInterval   = 30 * time.Second
	cmdTimeout      = 3 * time.Second
)

// CPUInfo is static identity plus the live per-core clock readings.
type CPUInfo struct {
	Model        string    `json:"model,omitempty"`
	Vendor       string    `json:"vendor,omitempty"`
	Socket       string    `json:"socket,omitempty"`
	Cores        int       `json:"cores,omitempty"`
	Threads      int       `json:"threads,omitempty"`
	Family       string    `json:"family,omitempty"`
	BaseClockMHz float64   `json:"baseClockMhz,omitempty"`
	MaxClockMHz  float64   `json:"maxClockMhz,omitempty"`
	CurClockMHz  float64   `json:"curClockMhz,omitempty"` // dynamic (avg of per-core)
	PerCoreMHz   []float64 `json:"perCoreMhz,omitempty"`  // dynamic
	TempC        float64   `json:"tempC,omitempty"`       // dynamic (package/Tctl)
	CacheL1      string    `json:"cacheL1,omitempty"`
	CacheL2      string    `json:"cacheL2,omitempty"`
	CacheL3      string    `json:"cacheL3,omitempty"`
}

// MemoryModule is one populated DIMM slot.
type MemoryModule struct {
	Slot          string `json:"slot,omitempty"`
	SizeBytes     uint64 `json:"sizeBytes,omitempty"`
	Type          string `json:"type,omitempty"`          // DDR4 / DDR5
	SpeedMHz      int    `json:"speedMhz,omitempty"`      // rated max
	ConfiguredMHz int    `json:"configuredMhz,omitempty"` // running speed
	Manufacturer  string `json:"manufacturer,omitempty"`
	PartNumber    string `json:"partNumber,omitempty"`
	Rank          string `json:"rank,omitempty"`
	Timings       string `json:"timings,omitempty"` // best-effort "CL16-18-18-38" from decode-dimms
}

// MemoryInfo is total RAM plus the per-slot module breakdown.
type MemoryInfo struct {
	TotalBytes uint64         `json:"totalBytes,omitempty"`
	Modules    []MemoryModule `json:"modules,omitempty"`
}

// BoardInfo is the mainboard and BIOS identity.
type BoardInfo struct {
	Manufacturer string `json:"manufacturer,omitempty"`
	Model        string `json:"model,omitempty"`
	Version      string `json:"version,omitempty"`
	BiosVendor   string `json:"biosVendor,omitempty"`
	BiosVersion  string `json:"biosVersion,omitempty"`
	BiosDate     string `json:"biosDate,omitempty"`
}

// GPUInfo is one NVIDIA GPU; the trailing fields are refreshed dynamically.
type GPUInfo struct {
	Name           string  `json:"name,omitempty"`
	MemTotalBytes  uint64  `json:"memTotalBytes,omitempty"`
	Driver         string  `json:"driver,omitempty"`
	CUDA           string  `json:"cuda,omitempty"`
	BaseClockMHz   float64 `json:"baseClockMhz,omitempty"`   // default application graphics clock
	BoostClockMHz  float64 `json:"boostClockMhz,omitempty"`  // max graphics clock
	CurClockMHz    float64 `json:"curClockMhz,omitempty"`    // dynamic
	MemClockMHz    float64 `json:"memClockMhz,omitempty"`    // dynamic current memory clock
	MemMaxClockMHz float64 `json:"memMaxClockMhz,omitempty"` // max memory clock
	TempC          float64 `json:"tempC,omitempty"`          // dynamic
	PowerW         float64 `json:"powerW,omitempty"`         // dynamic
	PowerLimitW    float64 `json:"powerLimitW,omitempty"`
}

// DiskHWInfo is the SYSTEM disk only (for the System tab).
type DiskHWInfo struct {
	Device       string  `json:"device,omitempty"`
	Model        string  `json:"model,omitempty"`
	Serial       string  `json:"serial,omitempty"`
	Firmware     string  `json:"firmware,omitempty"`
	SizeBytes    uint64  `json:"sizeBytes,omitempty"`
	Type         string  `json:"type,omitempty"`   // "NVMe SSD" / "SATA SSD" / "HDD"
	Health       string  `json:"health,omitempty"` // SMART overall, e.g. "PASSED"
	TempC        float64 `json:"tempC,omitempty"`
	PowerOnHours int     `json:"powerOnHours,omitempty"`
}

// NICInfo is one physical network interface.
type NICInfo struct {
	Name      string `json:"name,omitempty"` // interface, e.g. "enp5s0"
	Model     string `json:"model,omitempty"`
	MAC       string `json:"mac,omitempty"`
	SpeedMbps int    `json:"speedMbps,omitempty"`
	Driver    string `json:"driver,omitempty"`
	Link      string `json:"link,omitempty"` // "up"/"down" (operstate)
}

// Info is the full hardware snapshot returned to callers.
type Info struct {
	Hostname string     `json:"hostname,omitempty"`
	CPU      CPUInfo    `json:"cpu"`
	Memory   MemoryInfo `json:"memory"`
	Board    BoardInfo  `json:"board"`
	GPUs     []GPUInfo  `json:"gpus,omitempty"`
	Disk     DiskHWInfo `json:"disk"` // system disk
	NICs     []NICInfo  `json:"nics,omitempty"`
}

// dynamic holds the frequently-refreshed live values, kept separate from the
// static probe so the cheap 2s ticker never has to redo the expensive probing.
type dynamic struct {
	perCoreMHz []float64
	curClock   float64
	cpuTemp    float64
	gpu        []gpuDynamic
}

type gpuDynamic struct {
	curClock float64
	memClock float64
	tempC    float64
	powerW   float64
}

// Collector caches a static hardware probe (~10 min) and live dynamic values
// (~2 s) behind a single RWMutex. Get() only reads caches; it never shells out.
type Collector struct {
	mu    sync.RWMutex
	st    Info                 // static probe
	dyn   dynamic              // live values, merged into Get()
	smart map[string]SmartData // per-disk SMART (keyed by base name), ~30s refresh
}

// New returns an idle collector. Call Start to begin background probing.
func New() *Collector { return &Collector{} }

// Start runs an initial static probe and dynamic sample synchronously (so the
// first Get() after Start() is populated), then launches the two refresh loops.
func (c *Collector) Start() {
	c.probeStatic()
	c.probeDynamic()
	c.probeSmart()
	go func() {
		t := time.NewTicker(staticInterval)
		defer t.Stop()
		for range t.C {
			c.probeStatic()
		}
	}()
	go func() {
		t := time.NewTicker(dynamicInterval)
		defer t.Stop()
		for range t.C {
			c.probeDynamic()
		}
	}()
	go func() {
		t := time.NewTicker(smartInterval)
		defer t.Stop()
		for range t.C {
			c.probeSmart()
		}
	}()
}

// probeSmart refreshes the SMART cache for every whole disk via the privileged wrapper.
func (c *Collector) probeSmart() {
	m := map[string]SmartData{}
	for _, name := range wholeDiskNames() {
		if out, ok := sudoHwinfo(cmdTimeout, "smart", "/dev/"+name); ok {
			m[name] = parseSmartData(out)
		}
	}
	c.mu.Lock()
	c.smart = m
	c.mu.Unlock()
}

// Get returns the cached static Info with the cached dynamic fields merged in.
// It performs no I/O beyond reading the two caches.
func (c *Collector) Get() Info {
	c.mu.RLock()
	defer c.mu.RUnlock()
	info := c.st // value copy; slices are shared but we don't mutate the static ones
	info.CPU.PerCoreMHz = append([]float64(nil), c.dyn.perCoreMHz...)
	info.CPU.CurClockMHz = c.dyn.curClock
	info.CPU.TempC = c.dyn.cpuTemp
	// Merge per-GPU dynamics by row index against the static GPU slice.
	if len(c.dyn.gpu) > 0 {
		gpus := make([]GPUInfo, len(info.GPUs))
		copy(gpus, info.GPUs)
		for i := range gpus {
			if i < len(c.dyn.gpu) {
				d := c.dyn.gpu[i]
				gpus[i].CurClockMHz = d.curClock
				gpus[i].MemClockMHz = d.memClock
				gpus[i].TempC = d.tempC
				gpus[i].PowerW = d.powerW
			}
		}
		info.GPUs = gpus
	}
	return info
}

// probeStatic runs the full (expensive, possibly privileged) probe and swaps in
// the result atomically.
func (c *Collector) probeStatic() {
	info := Info{}
	if h, err := host.Info(); err == nil {
		info.Hostname = h.Hostname
	} else if hn, err := os.Hostname(); err == nil {
		info.Hostname = hn
	}
	info.CPU = probeCPU()
	info.Memory = probeMemory()
	info.Board = probeBoard()
	info.GPUs = probeGPUStatic()
	info.Disk = probeSystemDisk()
	info.NICs = probeNICs()

	c.mu.Lock()
	c.st = info
	c.mu.Unlock()
}

// probeDynamic samples the cheap live values.
func (c *Collector) probeDynamic() {
	d := dynamic{}
	d.perCoreMHz = readPerCoreMHz()
	if len(d.perCoreMHz) > 0 {
		var sum float64
		for _, v := range d.perCoreMHz {
			sum += v
		}
		d.curClock = roundMHz(sum / float64(len(d.perCoreMHz)))
	}
	d.cpuTemp = readCPUTemp()
	d.gpu = readGPUDynamic()

	c.mu.Lock()
	c.dyn = d
	c.mu.Unlock()
}

// --- CPU ---------------------------------------------------------------------

func probeCPU() CPUInfo {
	var ci CPUInfo

	// lscpu is unprivileged and the richest single source.
	if out, ok := runCmd(cmdTimeout, "lscpu"); ok {
		kv := parseColonKV(out)
		ci.Model = kv["Model name"]
		ci.Vendor = kv["Vendor ID"]
		ci.Socket = kv["Socket(s)"]
		ci.Family = kv["CPU family"]
		ci.Threads = atoi(kv["CPU(s)"])
		// Cores = cores-per-socket × sockets.
		if cps, sk := atoi(kv["Core(s) per socket"]), atoi(kv["Socket(s)"]); cps > 0 {
			if sk <= 0 {
				sk = 1
			}
			ci.Cores = cps * sk
		}
		ci.MaxClockMHz = roundMHz(atof(kv["CPU max MHz"]))
		// Atomic, simplified cache figures: drop lscpu's "(N instances)" notes and
		// fold the split L1 data+instruction caches into one total.
		ci.CacheL1 = sumCache(kv["L1d cache"], kv["L1i cache"])
		ci.CacheL2 = cleanCache(kv["L2 cache"])
		ci.CacheL3 = cleanCache(kv["L3 cache"])
	}

	// Base clock: cpufreq base_frequency (kHz) is the most accurate when present.
	if runtime.GOOS == "linux" {
		if b, err := os.ReadFile("/sys/devices/system/cpu/cpu0/cpufreq/base_frequency"); err == nil {
			if khz := atof(strings.TrimSpace(string(b))); khz > 0 {
				ci.BaseClockMHz = roundMHz(khz / 1000)
			}
		}
	}

	// Fill gaps from dmidecode (Socket/Family/base clock) if still missing.
	if ci.Socket == "" || ci.Family == "" || ci.BaseClockMHz == 0 {
		if out, ok := sudoHwinfo(cmdTimeout, "dmidecode-processor"); ok {
			for _, b := range parseDMI(out) {
				if b.typ != "Processor Information" {
					continue
				}
				if ci.Socket == "" {
					ci.Socket = b.v["Socket Designation"]
				}
				if ci.Family == "" {
					ci.Family = b.v["Family"]
				}
				if ci.BaseClockMHz == 0 {
					ci.BaseClockMHz = roundMHz(parseMHzField(b.v["Current Speed"]))
				}
				break
			}
		}
	}
	return ci
}

var cacheParenRe = regexp.MustCompile(`\s*\(.*\)\s*$`)

// cleanCache strips lscpu's "(N instances)" suffix, leaving an atomic "<n> <unit>".
func cleanCache(s string) string {
	return strings.TrimSpace(cacheParenRe.ReplaceAllString(s, ""))
}

// sumCache folds the split L1 data + instruction caches into one total. When both
// sides share a unit it sums them (e.g. "256 KiB" + "256 KiB" → "512 KiB");
// otherwise it falls back to a cleaned "a + b".
func sumCache(l1d, l1i string) string {
	a, b := cleanCache(l1d), cleanCache(l1i)
	na, ua := parseCacheSize(a)
	nb, ub := parseCacheSize(b)
	if ua != "" && ua == ub {
		return strconv.FormatFloat(na+nb, 'f', -1, 64) + " " + ua
	}
	switch {
	case a != "" && b != "":
		return a + " + " + b
	case a != "":
		return a
	default:
		return b
	}
}

func parseCacheSize(s string) (float64, string) {
	f := strings.Fields(s)
	if len(f) < 2 {
		return 0, ""
	}
	return atof(f[0]), f[1]
}

// readPerCoreMHz reads every CPU's scaling_cur_freq (kHz→MHz). Falls back to
// /proc/cpuinfo "cpu MHz" lines when cpufreq is unavailable.
func readPerCoreMHz() []float64 {
	if runtime.GOOS != "linux" {
		return nil
	}
	paths, _ := filepath.Glob("/sys/devices/system/cpu/cpu[0-9]*/cpufreq/scaling_cur_freq")
	if len(paths) > 0 {
		// Glob ordering is lexical (cpu10 before cpu2); sort numerically so the
		// slice index lines up with the logical CPU number.
		sortCPUFreqPaths(paths)
		out := make([]float64, 0, len(paths))
		for _, p := range paths {
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			if khz := atof(strings.TrimSpace(string(b))); khz > 0 {
				out = append(out, roundMHz(khz/1000))
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return cpuinfoMHz()
}

var cpuNumRe = regexp.MustCompile(`/cpu(\d+)/`)

func sortCPUFreqPaths(paths []string) {
	num := func(p string) int {
		if m := cpuNumRe.FindStringSubmatch(p); m != nil {
			return atoi(m[1])
		}
		return 0
	}
	// Simple insertion-style sort via sort-free swap is overkill; use stdlib.
	sortStrings(paths, func(a, b string) bool { return num(a) < num(b) })
}

func cpuinfoMHz() []float64 {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []float64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "cpu MHz") {
			if i := strings.IndexByte(line, ':'); i >= 0 {
				if v := atof(strings.TrimSpace(line[i+1:])); v > 0 {
					out = append(out, roundMHz(v))
				}
			}
		}
	}
	return out
}

// cpuTempChips lists the hwmon chip names that report CPU temperature, in
// preference order (AMD k10temp/zenpower, Intel coretemp, ARM cpu_thermal).
var cpuTempChips = []string{"k10temp", "zenpower", "coretemp", "cpu_thermal"}

// cpuTempLabels are the per-chip sensor labels to prefer (package/die temp first).
var cpuTempLabels = []string{"tdie", "tctl", "package id 0", "package", "tccd1"}

// readCPUTemp returns the package/Tctl CPU temperature in °C from sysfs hwmon,
// or 0 when no CPU temperature sensor is present.
func readCPUTemp() float64 {
	if runtime.GOOS != "linux" {
		return 0
	}
	names, _ := filepath.Glob("/sys/class/hwmon/hwmon*/name")
	chipDir, chipRank := "", len(cpuTempChips)
	for _, np := range names {
		name := strings.ToLower(readSysStr(np))
		for i, w := range cpuTempChips {
			if name == w && i < chipRank {
				chipRank, chipDir = i, filepath.Dir(np)
			}
		}
	}
	if chipDir == "" {
		return 0
	}
	// Prefer the package/die label; fall back to temp1_input.
	input, rank := "", len(cpuTempLabels)
	labels, _ := filepath.Glob(filepath.Join(chipDir, "temp*_label"))
	for _, lp := range labels {
		lbl := strings.ToLower(readSysStr(lp))
		for i, p := range cpuTempLabels {
			if strings.Contains(lbl, p) && i < rank {
				rank = i
				input = strings.TrimSuffix(lp, "_label") + "_input"
			}
		}
	}
	if input == "" {
		input = filepath.Join(chipDir, "temp1_input")
	}
	if v := atof(readSysStr(input)); v > 0 {
		return math.Round(v / 1000) // milli-°C → °C
	}
	return 0
}

// --- Memory ------------------------------------------------------------------

func probeMemory() MemoryInfo {
	var mi MemoryInfo
	if vm, err := mem.VirtualMemory(); err == nil {
		mi.TotalBytes = vm.Total
	}
	out, ok := sudoHwinfo(cmdTimeout, "dmidecode-memory")
	if !ok {
		return mi
	}
	timings := decodeDimmTimings() // best-effort, applied to all modules of matching kind
	for _, b := range parseDMI(out) {
		if b.typ != "Memory Device" {
			continue
		}
		size := b.v["Size"]
		if size == "" || strings.EqualFold(size, "No Module Installed") || strings.EqualFold(size, "Unknown") {
			continue
		}
		m := MemoryModule{
			Slot:         b.v["Locator"],
			SizeBytes:    parseSize(size),
			Type:         b.v["Type"],
			SpeedMHz:     int(parseMHzField(b.v["Speed"])),
			Manufacturer: cleanDMI(b.v["Manufacturer"]),
			PartNumber:   cleanDMI(b.v["Part Number"]),
			Rank:         b.v["Rank"],
		}
		// Configured running speed lives under one of two key spellings.
		conf := b.v["Configured Memory Speed"]
		if conf == "" {
			conf = b.v["Configured Clock Speed"]
		}
		m.ConfiguredMHz = int(parseMHzField(conf))
		if m.SizeBytes == 0 {
			continue
		}
		if timings != "" {
			m.Timings = timings
		}
		mi.Modules = append(mi.Modules, m)
	}
	return mi
}

// decodeDimmTimings parses decode-dimms for the primary CAS timing group and
// formats "CLxx-yy-zz-ww". Returns "" if the tool is missing or unparseable.
func decodeDimmTimings() string {
	out, ok := sudoHwinfo(cmdTimeout, "decode-dimms")
	if !ok {
		return ""
	}
	// decode-dimms prints rows like "tCL 16 clocks" / "Minimum CAS Latency Time (tAA) ...".
	// We look for the four classic JEDEC numbers tCL, tRCD, tRP, tRAS.
	find := func(tag string) int {
		re := regexp.MustCompile(`(?mi)\b` + regexp.QuoteMeta(tag) + `\b[^\d]*(\d+)`)
		if m := re.FindStringSubmatch(out); m != nil {
			return atoi(m[1])
		}
		return 0
	}
	cl, rcd, rp, ras := find("tCL"), find("tRCD"), find("tRP"), find("tRAS")
	if cl == 0 || rcd == 0 || rp == 0 || ras == 0 {
		return ""
	}
	return "CL" + strconv.Itoa(cl) + "-" + strconv.Itoa(rcd) + "-" + strconv.Itoa(rp) + "-" + strconv.Itoa(ras)
}

// --- Board / BIOS ------------------------------------------------------------

func probeBoard() BoardInfo {
	var bi BoardInfo
	if out, ok := sudoHwinfo(cmdTimeout, "dmidecode-baseboard"); ok {
		for _, b := range parseDMI(out) {
			if b.typ == "Base Board Information" {
				bi.Manufacturer = b.v["Manufacturer"]
				bi.Model = b.v["Product Name"]
				bi.Version = b.v["Version"]
				break
			}
		}
	}
	if out, ok := sudoHwinfo(cmdTimeout, "dmidecode-bios"); ok {
		for _, b := range parseDMI(out) {
			if b.typ == "BIOS Information" {
				bi.BiosVendor = b.v["Vendor"]
				bi.BiosVersion = b.v["Version"]
				bi.BiosDate = b.v["Release Date"]
				break
			}
		}
	}
	return bi
}

// --- GPU ---------------------------------------------------------------------

var cudaRe = regexp.MustCompile(`CUDA Version:\s*([0-9.]+)`)

func probeGPUStatic() []GPUInfo {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return nil
	}
	out, ok := runCmd(cmdTimeout, "nvidia-smi",
		"--query-gpu=name,memory.total,driver_version,clocks.max.graphics,clocks.default_applications.graphics,clocks.max.memory,power.default_limit",
		"--format=csv,noheader,nounits")
	if !ok {
		return nil
	}
	// CUDA toolkit version is only in the plain banner output, best-effort.
	cuda := ""
	if banner, ok := runCmd(cmdTimeout, "nvidia-smi"); ok {
		if m := cudaRe.FindStringSubmatch(banner); m != nil {
			cuda = m[1]
		}
	}
	var gpus []GPUInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := splitCSV(line)
		if len(f) < 7 {
			continue
		}
		g := GPUInfo{
			Name:           f[0],
			MemTotalBytes:  uint64(atof(f[1])) * 1024 * 1024, // MiB → bytes
			Driver:         f[2],
			CUDA:           cuda,
			BoostClockMHz:  roundMHz(atof(f[3])),
			BaseClockMHz:   roundMHz(atof(f[4])),
			MemMaxClockMHz: roundMHz(atof(f[5])),
			PowerLimitW:    atof(f[6]),
		}
		gpus = append(gpus, g)
	}
	return gpus
}

func readGPUDynamic() []gpuDynamic {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return nil
	}
	// NB: the valid field names are clocks.current.*, not clocks.graphics/clocks.memory
	// (the latter make nvidia-smi reject the whole query → no live clocks/temp/power).
	out, ok := runCmd(cmdTimeout, "nvidia-smi",
		"--query-gpu=clocks.current.graphics,clocks.current.memory,temperature.gpu,power.draw",
		"--format=csv,noheader,nounits")
	if !ok {
		return nil
	}
	var dyn []gpuDynamic
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := splitCSV(line)
		if len(f) < 4 {
			continue
		}
		dyn = append(dyn, gpuDynamic{
			curClock: roundMHz(atof(f[0])),
			memClock: roundMHz(atof(f[1])),
			tempC:    atof(f[2]),
			powerW:   atof(f[3]),
		})
	}
	return dyn
}

// --- System disk -------------------------------------------------------------

func probeSystemDisk() DiskHWInfo {
	var d DiskHWInfo
	root := diskutil.RootDevice()
	if root == "" {
		return d
	}
	dev := "/dev/" + root
	d.Device = root // base name ("sda"); the UI prepends /dev/

	// lsblk gives model/serial/transport/size/rotational for the whole disk.
	if devs, ok := lsblkDevices(dev); ok && len(devs) > 0 {
		ld := devs[0]
		d.Model = ld.Model
		d.Serial = ld.Serial
		d.SizeBytes = ld.Size
		d.Type = diskTypeString(ld.Tran, ld.Rota)
	}

	// SMART for firmware/health/temp/hours (handles ATA and NVMe output).
	if out, ok := sudoHwinfo(cmdTimeout, "smart", dev); ok {
		parseSMART(out, &d)
	}
	return d
}

// diskTypeString maps transport + rotational into the System-tab label.
func diskTypeString(tran string, rota bool) string {
	if strings.EqualFold(tran, "nvme") {
		return "NVMe SSD"
	}
	if rota {
		return "HDD"
	}
	if strings.EqualFold(tran, "sata") {
		return "SATA SSD"
	}
	return "SSD"
}

var (
	smartFirmwareRe = regexp.MustCompile(`(?mi)^Firmware Version:\s*(.+)$`)
	smartHealthRe   = regexp.MustCompile(`(?mi)self-assessment test result:\s*(\S+)`)
	smartHealthNVMe = regexp.MustCompile(`(?mi)^SMART Health Status:\s*(\S+)`)
	// ATA attribute table row: id name flags ... raw_value (POH is the last col).
	smartPOHAttr = regexp.MustCompile(`(?mi)^\s*\d+\s+Power_On_Hours\b.*\s(\d+)\s*$`)
	// NVMe "Power On Hours: 1,234".
	smartPOHNVMe   = regexp.MustCompile(`(?mi)^Power On Hours:\s*([\d,]+)`)
	smartTempAttr  = regexp.MustCompile(`(?mi)^\s*\d+\s+Temperature_\w+\b.*\s(\d+)\s*(?:\(|$)`)
	smartTempNVMe  = regexp.MustCompile(`(?mi)^Temperature:\s*(\d+)\s*Celsius`)
	smartTempCurRe = regexp.MustCompile(`(?mi)^Current Drive Temperature:\s*(\d+)`)
)

// SmartData is the SMART subset surfaced for any disk (ATA + NVMe output handled).
type SmartData struct {
	Health       string
	TempC        float64
	Firmware     string
	PowerOnHours int
}

func parseSmartData(out string) SmartData {
	var sd SmartData
	if m := smartFirmwareRe.FindStringSubmatch(out); m != nil {
		sd.Firmware = strings.TrimSpace(m[1])
	}
	if m := smartHealthRe.FindStringSubmatch(out); m != nil {
		sd.Health = strings.TrimSpace(m[1])
	} else if m := smartHealthNVMe.FindStringSubmatch(out); m != nil {
		sd.Health = strings.TrimSpace(m[1])
	}
	switch {
	case smartPOHAttr.MatchString(out):
		sd.PowerOnHours = atoi(smartPOHAttr.FindStringSubmatch(out)[1])
	case smartPOHNVMe.MatchString(out):
		sd.PowerOnHours = atoi(strings.ReplaceAll(smartPOHNVMe.FindStringSubmatch(out)[1], ",", ""))
	}
	switch {
	case smartTempNVMe.MatchString(out):
		sd.TempC = atof(smartTempNVMe.FindStringSubmatch(out)[1])
	case smartTempCurRe.MatchString(out):
		sd.TempC = atof(smartTempCurRe.FindStringSubmatch(out)[1])
	case smartTempAttr.MatchString(out):
		sd.TempC = atof(smartTempAttr.FindStringSubmatch(out)[1])
	}
	return sd
}

func parseSMART(out string, d *DiskHWInfo) {
	sd := parseSmartData(out)
	d.Firmware, d.Health, d.TempC, d.PowerOnHours = sd.Firmware, sd.Health, sd.TempC, sd.PowerOnHours
}

// --- NICs --------------------------------------------------------------------

func probeNICs() []NICInfo {
	if runtime.GOOS != "linux" {
		return nil
	}
	ifaces, err := filepath.Glob("/sys/class/net/*")
	if err != nil {
		return nil
	}
	var nics []NICInfo
	for _, path := range ifaces {
		name := filepath.Base(path)
		if name == "lo" {
			continue
		}
		// A real PCI/USB NIC has a device/ link; veth/docker/bridge interfaces don't.
		if _, err := os.Stat(filepath.Join(path, "device")); err != nil {
			continue
		}
		n := NICInfo{Name: name}
		n.MAC = readSysStr(filepath.Join(path, "address"))
		n.Link = readSysStr(filepath.Join(path, "operstate"))
		if sp := atoi(readSysStr(filepath.Join(path, "speed"))); sp > 0 {
			n.SpeedMbps = sp
		}
		// Driver = basename of the device/driver symlink target.
		if drv, err := os.Readlink(filepath.Join(path, "device", "driver")); err == nil {
			n.Driver = filepath.Base(drv)
		}
		nics = append(nics, n)
	}
	return nics
}

// --- privileged / command helpers -------------------------------------------

// sudoHwinfo invokes the privileged wrapper for one keyword. Any failure
// (missing wrapper, sudo denial, missing tool, nonzero exit) reports !ok.
func sudoHwinfo(timeout time.Duration, args ...string) (string, bool) {
	full := append([]string{"-n", hwinfoWrapper}, args...)
	return runCmd(timeout, "sudo", full...)
}

// runCmd runs a command with a context timeout and returns trimmed stdout.
// Anything other than a clean zero exit reports !ok.
func runCmd(timeout time.Duration, name string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// --- parsing helpers ---------------------------------------------------------

// parseColonKV parses "Key: Value" lines (lscpu style) into a map, keeping the
// first occurrence of each key.
func parseColonKV(out string) map[string]string {
	kv := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		i := strings.IndexByte(line, ':')
		if i < 0 {
			continue
		}
		key := strings.TrimSpace(line[:i])
		if _, dup := kv[key]; dup {
			continue
		}
		kv[key] = strings.TrimSpace(line[i+1:])
	}
	return kv
}

// dmiBlock is one dmidecode handle: its type name and its indented key/values.
type dmiBlock struct {
	typ string
	v   map[string]string
}

// parseDMI splits dmidecode output into blocks. Each block begins at a "Handle "
// line; the next non-indented line is the block type name, and subsequent
// indented "Key: Value" lines are its fields. Multi-line list values are ignored.
func parseDMI(out string) []dmiBlock {
	var blocks []dmiBlock
	var cur *dmiBlock
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "Handle "):
			blocks = append(blocks, dmiBlock{v: map[string]string{}})
			cur = &blocks[len(blocks)-1]
		case cur == nil:
			// before first handle
		case strings.TrimSpace(line) == "":
			cur = nil // blank line ends the block
		case cur.typ == "" && !isIndented(line):
			cur.typ = strings.TrimSpace(line) // first non-indented line names the block
		case isIndented(line):
			if i := strings.IndexByte(line, ':'); i >= 0 {
				key := strings.TrimSpace(line[:i])
				val := strings.TrimSpace(line[i+1:])
				if val != "" { // skip list headers like "Characteristics:"
					cur.v[key] = val
				}
			}
		}
	}
	return blocks
}

func isIndented(s string) bool {
	return len(s) > 0 && (s[0] == '\t' || s[0] == ' ')
}

// cleanDMI blanks out dmidecode placeholder strings.
func cleanDMI(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "unknown", "not specified", "none", "to be filled by o.e.m.", "<out of spec>":
		return ""
	}
	return strings.TrimSpace(s)
}

// splitCSV splits a CSV row and trims each field (nvidia-smi pads with spaces).
func splitCSV(line string) []string {
	parts := strings.Split(line, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func readSysStr(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// parseSize turns a dmidecode size string ("16 GB", "8192 MB") into bytes.
func parseSize(s string) uint64 {
	fields := strings.Fields(s)
	if len(fields) < 2 {
		return 0
	}
	n := atof(fields[0])
	if n <= 0 {
		return 0
	}
	switch strings.ToUpper(fields[1]) {
	case "TB":
		return uint64(n * 1024 * 1024 * 1024 * 1024)
	case "GB":
		return uint64(n * 1024 * 1024 * 1024)
	case "MB":
		return uint64(n * 1024 * 1024)
	case "KB":
		return uint64(n * 1024)
	}
	return 0
}

// parseMHzField pulls the leading number out of a "3200 MT/s" / "2400 MHz" field.
func parseMHzField(s string) float64 {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0
	}
	return atof(fields[0])
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func atof(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

// roundMHz rounds a clock to whole MHz, neutralising NaN/Inf.
func roundMHz(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return math.Round(f)
}

// sortStrings is a tiny insertion sort so we avoid importing "sort" just for a
// handful of cpufreq paths; less is a strict-ordering predicate.
func sortStrings(s []string, less func(a, b string) bool) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && less(s[j], s[j-1]); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
