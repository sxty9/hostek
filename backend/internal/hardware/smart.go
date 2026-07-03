package hardware

// SMART health parsing and derivation. Fed by `smartctl -aj` (JSON) via the
// privileged wrapper — see hostek's write_hwinfo_wrapper. Everything is
// best-effort: a missing tool, an unparseable payload, or an absent field
// simply leaves the corresponding value zero/nil. We match ATA attributes by
// numeric ID (names vary by vendor) and derive three user-facing signals:
//
//   - HealthStatus/HealthReason — a Healthy/Warning/Critical verdict.
//   - LifePercent               — remaining endurance %, SSD/NVMe only (HWInfo-style).
//   - AgePercent                — power-on age proxy, HDD only (no wear budget exists).
//
// The raw SMART counters live in SmartRaw, gated behind the techinfo right by the API.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Health verdict values (kept lowercase; the UI maps them to a badge + i18n label).
const (
	HealthHealthy  = "healthy"
	HealthWarning  = "warning"
	HealthCritical = "critical"
)

// hddLifeRefHours is the reference lifetime for the HDD age proxy: 5 years of
// continuous operation. It is NOT a wear metric — just "how old is this drive".
const hddLifeRefHours = 5 * 365 * 24 // 43800

// nvmeDataUnitBytes is the fixed size of one NVMe "data unit" written/read:
// 1000 * 512 bytes per the NVMe spec, independent of the logical block size.
const nvmeDataUnitBytes = 512000

// SmartHealth is the SMART-derived surface shared by the Disks tab (DiskDevice)
// and the System tab (DiskHWInfo); it is embedded into both so the JSON stays flat.
type SmartHealth struct {
	Health       string  `json:"health,omitempty"` // SMART overall self-assessment: "PASSED"/"FAILED"
	TempC        float64 `json:"tempC,omitempty"`
	Firmware     string  `json:"firmware,omitempty"`
	PowerOnHours int     `json:"powerOnHours,omitempty"`
	PowerCycles  int     `json:"powerCycles,omitempty"`

	// Derived, shown to everyone.
	HealthStatus string `json:"healthStatus,omitempty"` // healthy | warning | critical
	HealthReason string `json:"healthReason,omitempty"` // short trigger, e.g. "3 reallocated sectors"
	LifePercent  *int   `json:"lifePercent,omitempty"`  // remaining endurance %, SSD/NVMe only
	AgePercent   *int   `json:"agePercent,omitempty"`   // power-on age %, HDD only

	// Raw diagnostic counters — gated behind techinfo (API nils the whole pointer).
	Raw *SmartRaw `json:"smart,omitempty"`
}

// SmartRaw holds raw SMART/NVMe counters for the technical drill-down. A field is
// nil when the underlying attribute is absent; the whole struct is nilled by the
// API for users without the techinfo right.
type SmartRaw struct {
	// ATA attributes (nil on NVMe).
	ReallocatedSectors   *int `json:"reallocatedSectors,omitempty"`   // 5
	PendingSectors       *int `json:"pendingSectors,omitempty"`       // 197
	OfflineUncorrectable *int `json:"offlineUncorrectable,omitempty"` // 198
	ReportedUncorrect    *int `json:"reportedUncorrect,omitempty"`    // 187
	CommandTimeout       *int `json:"commandTimeout,omitempty"`       // 188
	SpinRetry            *int `json:"spinRetry,omitempty"`            // 10
	UdmaCrc              *int `json:"udmaCrc,omitempty"`              // 199 (cabling; never gates)

	// NVMe health-log fields (nil on ATA).
	PercentageUsed          *int `json:"percentageUsed,omitempty"`
	AvailableSpare          *int `json:"availableSpare,omitempty"`
	AvailableSpareThreshold *int `json:"availableSpareThreshold,omitempty"`
	CriticalWarning         *int `json:"criticalWarning,omitempty"`
	MediaErrors             *int `json:"mediaErrors,omitempty"`
	UnsafeShutdowns         *int `json:"unsafeShutdowns,omitempty"`

	// Endurance, either transport.
	TbwBytes *uint64 `json:"tbwBytes,omitempty"` // total bytes written
}

// --- smartctl -aj JSON decoding ---------------------------------------------

type smartctlJSON struct {
	JSONFormatVersion []int `json:"json_format_version"`
	Smartctl          struct {
		Version    []int `json:"version"`
		ExitStatus int   `json:"exit_status"`
	} `json:"smartctl"`
	Device struct {
		Name     string `json:"name"`
		Protocol string `json:"protocol"` // "ATA" | "NVMe" | "SCSI"
	} `json:"device"`
	ModelName        string `json:"model_name"`
	FirmwareVersion  string `json:"firmware_version"`
	SerialNumber     string `json:"serial_number"`
	RotationRate     int    `json:"rotation_rate"` // 0 for SSD/NVMe, spindle rpm for HDD
	LogicalBlockSize uint64 `json:"logical_block_size"`
	SmartStatus      *struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	Temperature *struct {
		Current int `json:"current"`
	} `json:"temperature"`
	PowerOnTime *struct {
		Hours int `json:"hours"`
	} `json:"power_on_time"`
	PowerCycleCount    int `json:"power_cycle_count"`
	ATASmartAttributes *struct {
		Table []ataAttr `json:"table"`
	} `json:"ata_smart_attributes"`
	NVMeLog *nvmeHealthLog `json:"nvme_smart_health_information_log"`
}

type ataAttr struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Value      int    `json:"value"` // normalized current (0..255)
	Worst      int    `json:"worst"`
	Thresh     int    `json:"thresh"`
	WhenFailed string `json:"when_failed"`
	Raw        struct {
		Value  uint64 `json:"value"`
		String string `json:"string"`
	} `json:"raw"`
}

type nvmeHealthLog struct {
	CriticalWarning         int    `json:"critical_warning"`
	Temperature             int    `json:"temperature"`
	AvailableSpare          int    `json:"available_spare"`
	AvailableSpareThreshold int    `json:"available_spare_threshold"`
	PercentageUsed          int    `json:"percentage_used"`
	DataUnitsWritten        uint64 `json:"data_units_written"`
	PowerCycles             uint64 `json:"power_cycles"`
	PowerOnHours            uint64 `json:"power_on_hours"`
	UnsafeShutdowns         uint64 `json:"unsafe_shutdowns"`
	MediaErrors             uint64 `json:"media_errors"`
	NumErrLogEntries        uint64 `json:"num_err_log_entries"`
	WarningTempTime         int    `json:"warning_temp_time"`
	CriticalCompTime        int    `json:"critical_comp_time"`
}

// drive class, derived from the JSON itself.
const (
	classNVMe = "nvme"
	classHDD  = "hdd"
	classSSD  = "ssd"
)

func (d *smartctlJSON) class() string {
	if strings.EqualFold(d.Device.Protocol, "NVMe") || d.NVMeLog != nil {
		return classNVMe
	}
	if d.RotationRate > 0 {
		return classHDD
	}
	return classSSD
}

// parseSmartData turns one `smartctl -aj` payload into the surfaced SmartHealth.
// An unparseable or wrong-schema payload yields the zero value (no data).
func parseSmartData(out string) SmartHealth {
	var h SmartHealth
	var d smartctlJSON
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		return h
	}
	// Only trust format v1. Minor bumps only add fields, but guard defensively.
	if len(d.JSONFormatVersion) > 0 && d.JSONFormatVersion[0] != 1 {
		return h
	}

	h.Firmware = strings.TrimSpace(d.FirmwareVersion)
	if d.SmartStatus != nil {
		if d.SmartStatus.Passed {
			h.Health = "PASSED"
		} else {
			h.Health = "FAILED"
		}
	}
	if d.Temperature != nil {
		h.TempC = float64(d.Temperature.Current)
	}
	if d.PowerOnTime != nil {
		h.PowerOnHours = d.PowerOnTime.Hours
	}
	h.PowerCycles = d.PowerCycleCount

	class := d.class()
	switch class {
	case classNVMe:
		h.parseNVMe(&d)
	default: // ATA: ssd or hdd
		h.parseATA(&d, class)
	}
	h.deriveVerdict(&d, class)
	return h
}

// parseSMART is the System-tab adapter (root disk only).
func parseSMART(out string, d *DiskHWInfo) {
	d.SmartHealth = parseSmartData(out)
}

// --- NVMe --------------------------------------------------------------------

func (h *SmartHealth) parseNVMe(d *smartctlJSON) {
	n := d.NVMeLog
	if n == nil {
		return
	}
	raw := &SmartRaw{
		PercentageUsed:          intPtr(n.PercentageUsed),
		AvailableSpare:          intPtr(n.AvailableSpare),
		AvailableSpareThreshold: intPtr(n.AvailableSpareThreshold),
		CriticalWarning:         intPtr(n.CriticalWarning),
		MediaErrors:             intPtr(int(n.MediaErrors)),
		UnsafeShutdowns:         intPtr(int(n.UnsafeShutdowns)),
		TbwBytes:                u64Ptr(n.DataUnitsWritten * nvmeDataUnitBytes),
	}
	h.Raw = raw

	// Remaining endurance = 100 − percentage_used. percentage_used is a vendor
	// estimate (0..255) that MAY exceed 100 once the rated life is consumed.
	h.LifePercent = intPtr(clampInt(100-n.PercentageUsed, 0, 100))

	// The NVMe log is authoritative for these when the top-level fields were absent.
	if h.TempC == 0 && n.Temperature != 0 {
		h.TempC = float64(n.Temperature)
	}
	if h.PowerOnHours == 0 && n.PowerOnHours != 0 {
		h.PowerOnHours = int(n.PowerOnHours)
	}
	if h.PowerCycles == 0 && n.PowerCycles != 0 {
		h.PowerCycles = int(n.PowerCycles)
	}
}

// --- ATA (SATA/SAS SSD + HDD) ------------------------------------------------

func (h *SmartHealth) parseATA(d *smartctlJSON, class string) {
	if d.ATASmartAttributes == nil {
		return
	}
	byID := make(map[int]ataAttr, len(d.ATASmartAttributes.Table))
	for _, a := range d.ATASmartAttributes.Table {
		byID[a.ID] = a
	}

	raw := &SmartRaw{
		ReallocatedSectors:   rawCount(byID, 5),
		PendingSectors:       rawCount(byID, 197),
		OfflineUncorrectable: rawCount(byID, 198),
		ReportedUncorrect:    rawCount(byID, 187),
		CommandTimeout:       rawCount(byID, 188),
		SpinRetry:            rawCount(byID, 10),
		UdmaCrc:              rawCount(byID, 199),
	}
	if a, ok := lbaWritten(byID); ok {
		lbs := d.LogicalBlockSize
		if lbs == 0 {
			lbs = 512
		}
		raw.TbwBytes = u64Ptr(a.Raw.Value * lbs)
	}
	h.Raw = raw

	// Remaining endurance only exists for SSDs. HDDs have no wear budget.
	if class == classSSD {
		if life, ok := ssdLifePercent(byID); ok {
			h.LifePercent = intPtr(life)
		}
	}
}

// rawCount returns the meaningful count of an ATA attribute, or nil if absent.
// 0 is a real, reassuring value — hence a pointer. Many drives pack sub-values
// into raw.value (e.g. Seagate ES reports raw.value=0x2_0000_0002 for a true
// Reallocated count of 2); smartctl decodes the real count as the leading token
// of raw.string ("2 (2 0)"), which we trust over the packed raw.value.
func rawCount(byID map[int]ataAttr, id int) *int {
	a, ok := byID[id]
	if !ok {
		return nil
	}
	return intPtr(rawLeadingInt(a))
}

// rawLeadingInt reads the leading integer of raw.string, falling back to raw.value
// (masked to 32 bits, since packed encodings carry the count in the low word).
func rawLeadingInt(a ataAttr) int {
	s := strings.TrimSpace(a.Raw.String)
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > 0 {
		return atoi(s[:i])
	}
	v := a.Raw.Value
	if v > 0xFFFFFFFF {
		v &= 0xFFFFFFFF
	}
	return int(v)
}

// lbaWritten finds the "Total_LBAs_Written" attribute (241 on most drives, 246 on
// some Micron/Crucial), used to derive total bytes written.
func lbaWritten(byID map[int]ataAttr) (ataAttr, bool) {
	for _, id := range []int{241, 246} {
		if a, ok := byID[id]; ok && strings.Contains(strings.ToUpper(a.Name), "LBA") {
			return a, true
		}
	}
	return ataAttr{}, false
}

// ssdLifePercent derives remaining endurance % from the first wear attribute that
// resolves, in priority order. It matches by ID (never "lowest normalized value" —
// e.g. 180 Unused_Reserve_NAND_Blk legitimately reports value=0 and would fool that).
func ssdLifePercent(byID map[int]ataAttr) (int, bool) {
	// Priority: vendor-specific "life left" gauges first, generic wear last.
	for _, id := range []int{231, 233, 177, 169, 202, 173} {
		a, ok := byID[id]
		if !ok {
			continue
		}
		if p, ok := wearPercent(a); ok {
			return p, true
		}
	}
	return 0, false
}

// wearPercent interprets one wear attribute's remaining-life %. The normalized
// VALUE counts down from 100 toward the threshold for most gauges, but vendors
// vary — attribute 202 in particular can carry the meaningful number in RAW.
func wearPercent(a ataAttr) (int, bool) {
	name := strings.ToUpper(a.Name)
	rawv := int(a.Raw.Value)
	switch a.ID {
	case 231:
		// 231 is "SSD_Life_Left" on some drives but "Temperature" on others —
		// only trust it when the name confirms it is a life gauge.
		if !strings.Contains(name, "LIFE") {
			return 0, false
		}
		return normalizedDown(a.Value, rawv)
	case 202:
		// Percent_Lifetime_Remain — vendor-inconsistent:
		//  • MX500/most: VALUE = remaining, RAW = used, VALUE+RAW ≈ 100.
		//  • others: RAW is "used %" with VALUE pinned at 100 → remaining = 100−RAW.
		if rawv >= 0 && rawv <= 100 {
			if absInt(a.Value+rawv-100) <= 2 {
				return clampInt(a.Value, 0, 100), true
			}
			return clampInt(100-rawv, 0, 100), true
		}
		if a.Value >= 0 && a.Value <= 100 {
			return a.Value, true
		}
		return 0, false
	default: // 233, 177, 169, 173 — normalized VALUE counts down.
		return normalizedDown(a.Value, rawv)
	}
}

// normalizedDown reads a "counts down from 100" wear gauge. A VALUE above 100 means
// the firmware is reporting *used* rather than remaining, so fall back to 100−RAW.
func normalizedDown(value, rawv int) (int, bool) {
	if value > 100 {
		if rawv >= 0 && rawv <= 100 {
			return clampInt(100-rawv, 0, 100), true
		}
		return 0, false
	}
	if value < 0 {
		return 0, false
	}
	return clampInt(value, 0, 100), true
}

// --- Health verdict ----------------------------------------------------------

// deriveVerdict computes the Healthy/Warning/Critical status and a short reason.
// It matches by attribute ID, never lets UDMA CRC (cabling) gate the drive, and
// only flags temperature at genuine extremes.
func (h *SmartHealth) deriveVerdict(d *smartctlJSON, class string) {
	// Without any SMART signal (unreadable device that still emitted JSON) we must
	// not assert a verdict — leaving HealthStatus empty renders nothing, rather
	// than a falsely reassuring "healthy".
	if d.SmartStatus == nil && d.ATASmartAttributes == nil && d.NVMeLog == nil {
		return
	}
	status := HealthHealthy
	var critReason, warnReason string
	crit := func(r string) {
		status = HealthCritical
		if critReason == "" {
			critReason = r
		}
	}
	warn := func(r string) {
		if status != HealthCritical {
			status = HealthWarning
		}
		if warnReason == "" {
			warnReason = r
		}
	}

	if d.SmartStatus != nil && !d.SmartStatus.Passed {
		crit("SMART self-assessment failed")
	}

	switch {
	case class == classNVMe && d.NVMeLog != nil:
		n := d.NVMeLog
		if n.CriticalWarning != 0 {
			crit(fmt.Sprintf("NVMe critical warning (0x%02x)", n.CriticalWarning))
		}
		if n.AvailableSpareThreshold > 0 && n.AvailableSpare <= n.AvailableSpareThreshold {
			crit(fmt.Sprintf("available spare %d%% ≤ threshold %d%%", n.AvailableSpare, n.AvailableSpareThreshold))
		} else if n.AvailableSpareThreshold > 0 && n.AvailableSpare <= n.AvailableSpareThreshold+10 {
			warn(fmt.Sprintf("available spare low (%d%%)", n.AvailableSpare))
		}
		if n.MediaErrors > 0 {
			crit(fmt.Sprintf("%d media errors", n.MediaErrors))
		}
		if n.CriticalCompTime > 0 {
			warn("ran in critical temperature range")
		}
	case h.Raw != nil: // ATA (ssd or hdd)
		if v := derefInt(h.Raw.ReportedUncorrect); v > 0 {
			crit(fmt.Sprintf("%d reported-uncorrectable errors", v))
		}
		if v := derefInt(h.Raw.OfflineUncorrectable); v > 0 {
			crit(fmt.Sprintf("%d offline-uncorrectable sectors", v))
		}
		if v := derefInt(h.Raw.SpinRetry); v > 0 {
			crit(fmt.Sprintf("%d spin-retry events", v))
		}
		if v := derefInt(h.Raw.ReallocatedSectors); v > 0 {
			warn(fmt.Sprintf("%d reallocated sectors", v))
		}
		if v := derefInt(h.Raw.PendingSectors); v > 0 {
			warn(fmt.Sprintf("%d pending sectors", v))
		}
	}

	// Endurance thresholds (SSD/NVMe only).
	if h.LifePercent != nil {
		switch {
		case *h.LifePercent <= 5:
			crit(fmt.Sprintf("%d%% endurance remaining", *h.LifePercent))
		case *h.LifePercent <= 10:
			warn(fmt.Sprintf("%d%% endurance remaining", *h.LifePercent))
		}
	}

	// Temperature only at genuine extremes (Backblaze: no correlation in-range).
	tempLimit := 70.0
	if class == classHDD {
		tempLimit = 60.0
	}
	if h.TempC >= tempLimit {
		warn(fmt.Sprintf("high temperature (%d °C)", int(h.TempC)))
	}

	// HDD age proxy — power-on hours vs a 5-year reference. Explicitly NOT a wear %.
	if class == classHDD && h.PowerOnHours > 0 {
		h.AgePercent = intPtr(clampInt(h.PowerOnHours*100/hddLifeRefHours, 0, 100))
	}

	h.HealthStatus = status
	switch status {
	case HealthCritical:
		h.HealthReason = critReason
	case HealthWarning:
		h.HealthReason = warnReason
	}
}

// --- small helpers -----------------------------------------------------------

func intPtr(i int) *int       { return &i }
func u64Ptr(u uint64) *uint64 { return &u }

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func absInt(i int) int {
	if i < 0 {
		return -i
	}
	return i
}
