package hardware

import (
	"os"
	"testing"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

// Real Crucial MX500 (SATA SSD): 202 Percent_Lifetime_Remain value=80/raw=20 →
// 80% remaining; the trap attribute 180 Unused_Reserve_NAND_Blk has value=0 and
// must NOT be mistaken for life. Healthy, no errors.
func TestParseMX500(t *testing.T) {
	h := parseSmartData(readFixture(t, "mx500_ssd.json"))

	if h.LifePercent == nil || *h.LifePercent != 80 {
		t.Errorf("LifePercent = %v, want 80", h.LifePercent)
	}
	if h.AgePercent != nil {
		t.Errorf("AgePercent = %v, want nil (SSD has no age proxy)", h.AgePercent)
	}
	if h.HealthStatus != HealthHealthy {
		t.Errorf("HealthStatus = %q, want healthy (reason=%q)", h.HealthStatus, h.HealthReason)
	}
	if h.Health != "PASSED" {
		t.Errorf("Health = %q, want PASSED", h.Health)
	}
	if h.Firmware != "M3CR043" {
		t.Errorf("Firmware = %q, want M3CR043", h.Firmware)
	}
	if h.TempC != 26 {
		t.Errorf("TempC = %v, want 26", h.TempC)
	}
	if h.PowerCycles != 164 {
		t.Errorf("PowerCycles = %d, want 164", h.PowerCycles)
	}
	if h.Raw == nil {
		t.Fatal("Raw = nil, want counters present")
	}
	if got := derefInt(h.Raw.ReallocatedSectors); got != 0 {
		t.Errorf("ReallocatedSectors = %d, want 0", got)
	}
	if h.Raw.TbwBytes == nil || *h.Raw.TbwBytes == 0 {
		t.Errorf("TbwBytes = %v, want > 0", h.Raw.TbwBytes)
	}
}

// Real Seagate ST2000DM001 (7200rpm HDD): no wear budget → no LifePercent, but an
// age proxy; all Backblaze-five counters zero → healthy. SMART 1 raw is huge
// (Seagate-packed) and must not leak into any percentage.
func TestParseSeagate(t *testing.T) {
	h := parseSmartData(readFixture(t, "seagate_hdd.json"))

	if h.LifePercent != nil {
		t.Errorf("LifePercent = %v, want nil (HDD)", h.LifePercent)
	}
	if h.AgePercent == nil {
		t.Fatal("AgePercent = nil, want a power-on age proxy for an HDD")
	}
	if *h.AgePercent < 0 || *h.AgePercent > 100 {
		t.Errorf("AgePercent = %d, want 0..100", *h.AgePercent)
	}
	if h.HealthStatus != HealthHealthy {
		t.Errorf("HealthStatus = %q, want healthy (reason=%q)", h.HealthStatus, h.HealthReason)
	}
	if got := derefInt(h.Raw.ReportedUncorrect); got != 0 {
		t.Errorf("ReportedUncorrect = %d, want 0", got)
	}
	if h.Raw.SpinRetry == nil {
		t.Error("SpinRetry = nil, want the attribute present on this HDD")
	}
}

func nvmeFixture(percentageUsed, availSpare, availThresh, criticalWarning, mediaErrors int) string {
	return `{
	  "json_format_version": [1, 0],
	  "smartctl": {"version": [7, 5], "exit_status": 0},
	  "device": {"name": "/dev/nvme0", "protocol": "NVMe"},
	  "model_name": "Samsung SSD 980 PRO 1TB",
	  "firmware_version": "5B2QGXA7",
	  "rotation_rate": 0,
	  "smart_status": {"passed": true},
	  "temperature": {"current": 40},
	  "power_cycle_count": 120,
	  "nvme_smart_health_information_log": {
	    "critical_warning": ` + itoa(criticalWarning) + `,
	    "temperature": 40,
	    "available_spare": ` + itoa(availSpare) + `,
	    "available_spare_threshold": ` + itoa(availThresh) + `,
	    "percentage_used": ` + itoa(percentageUsed) + `,
	    "data_units_written": 200000000,
	    "power_on_hours": 5000,
	    "power_cycles": 120,
	    "media_errors": ` + itoa(mediaErrors) + `,
	    "unsafe_shutdowns": 8
	  }
	}`
}

// itoa avoids importing strconv just for the fixture builder.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func TestParseNVMeHealthy(t *testing.T) {
	h := parseSmartData(nvmeFixture(5, 100, 10, 0, 0))
	if h.LifePercent == nil || *h.LifePercent != 95 {
		t.Errorf("LifePercent = %v, want 95 (100-5)", h.LifePercent)
	}
	if h.HealthStatus != HealthHealthy {
		t.Errorf("HealthStatus = %q, want healthy", h.HealthStatus)
	}
	if h.AgePercent != nil {
		t.Errorf("AgePercent = %v, want nil (NVMe uses life %%, not age)", h.AgePercent)
	}
	if h.Raw == nil || h.Raw.TbwBytes == nil || *h.Raw.TbwBytes != 200000000*nvmeDataUnitBytes {
		t.Errorf("TbwBytes = %v, want %d", h.Raw.TbwBytes, uint64(200000000)*nvmeDataUnitBytes)
	}
}

// percentage_used can exceed 100 once rated life is consumed → clamp to 0% remaining.
func TestParseNVMeWorn(t *testing.T) {
	h := parseSmartData(nvmeFixture(110, 100, 10, 0, 0))
	if h.LifePercent == nil || *h.LifePercent != 0 {
		t.Errorf("LifePercent = %v, want 0 (clamped)", h.LifePercent)
	}
	if h.HealthStatus != HealthCritical {
		t.Errorf("HealthStatus = %q, want critical at 0%% endurance", h.HealthStatus)
	}
}

// Spare at/under threshold is a distinct CRITICAL signal independent of wear.
func TestParseNVMeSpareExhausted(t *testing.T) {
	h := parseSmartData(nvmeFixture(20, 8, 10, 0, 0)) // 80% life left but spare 8% ≤ 10%
	if h.LifePercent == nil || *h.LifePercent != 80 {
		t.Errorf("LifePercent = %v, want 80", h.LifePercent)
	}
	if h.HealthStatus != HealthCritical {
		t.Errorf("HealthStatus = %q, want critical (spare below threshold)", h.HealthStatus)
	}
}

func TestParseNVMeCriticalWarning(t *testing.T) {
	h := parseSmartData(nvmeFixture(20, 100, 10, 0x04, 0)) // reliability-degraded bit
	if h.HealthStatus != HealthCritical {
		t.Errorf("HealthStatus = %q, want critical (critical_warning set)", h.HealthStatus)
	}
}

// A failed overall SMART self-assessment forces critical regardless of counters.
func TestParseFailingDisk(t *testing.T) {
	in := `{
	  "json_format_version": [1, 0],
	  "smartctl": {"version": [7, 5], "exit_status": 8},
	  "device": {"name": "/dev/sdz", "protocol": "ATA"},
	  "rotation_rate": 7200,
	  "smart_status": {"passed": false},
	  "temperature": {"current": 44},
	  "power_on_time": {"hours": 40000},
	  "ata_smart_attributes": {"table": [
	    {"id": 5,  "name": "Reallocated_Sector_Ct", "value": 60, "worst": 60, "thresh": 10, "raw": {"value": 128, "string": "128"}},
	    {"id": 197,"name": "Current_Pending_Sector","value": 100,"worst": 100,"thresh": 0, "raw": {"value": 8,  "string": "8"}},
	    {"id": 198,"name": "Offline_Uncorrectable", "value": 100,"worst": 100,"thresh": 0, "raw": {"value": 4,  "string": "4"}}
	  ]}
	}`
	h := parseSmartData(in)
	if h.HealthStatus != HealthCritical {
		t.Errorf("HealthStatus = %q, want critical", h.HealthStatus)
	}
	if h.LifePercent != nil {
		t.Errorf("LifePercent = %v, want nil (HDD)", h.LifePercent)
	}
	if got := derefInt(h.Raw.OfflineUncorrectable); got != 4 {
		t.Errorf("OfflineUncorrectable = %d, want 4", got)
	}
	if h.HealthReason == "" {
		t.Error("HealthReason empty, want a stated trigger")
	}
}

// UDMA CRC errors are a cabling fault and must never flip the drive verdict.
func TestUdmaCrcDoesNotGate(t *testing.T) {
	in := `{
	  "json_format_version": [1, 0],
	  "device": {"protocol": "ATA"},
	  "rotation_rate": 0,
	  "smart_status": {"passed": true},
	  "temperature": {"current": 30},
	  "ata_smart_attributes": {"table": [
	    {"id": 199, "name": "UDMA_CRC_Error_Count", "value": 200, "worst": 200, "thresh": 0, "raw": {"value": 1500, "string": "1500"}},
	    {"id": 202, "name": "Percent_Lifetime_Remain", "value": 95, "worst": 95, "thresh": 1, "raw": {"value": 5, "string": "5"}}
	  ]}
	}`
	h := parseSmartData(in)
	if h.HealthStatus != HealthHealthy {
		t.Errorf("HealthStatus = %q, want healthy (CRC must not gate)", h.HealthStatus)
	}
	if got := derefInt(h.Raw.UdmaCrc); got != 1500 {
		t.Errorf("UdmaCrc = %d, want 1500 (still reported)", got)
	}
}

// Seagate ES drives pack sub-values into raw.value: a true reallocated count of 2
// appears as raw.value=8589934594 with raw.string "2 (2 0)". The count (and the
// verdict reason) must reflect the real 2, not the packed value.
func TestParsePackedRawCount(t *testing.T) {
	in := `{
	  "json_format_version":[1,0],
	  "device":{"protocol":"ATA"},
	  "rotation_rate":7200,
	  "smart_status":{"passed":true},
	  "power_on_time":{"hours":74473},
	  "ata_smart_attributes":{"table":[
	    {"id":5,"name":"Reallocated_Sector_Ct","value":100,"worst":100,"thresh":36,"raw":{"value":8589934594,"string":"2 (2 0)"}}
	  ]}
	}`
	h := parseSmartData(in)
	if got := derefInt(h.Raw.ReallocatedSectors); got != 2 {
		t.Errorf("ReallocatedSectors = %d, want 2 (unpacked from raw.string)", got)
	}
	if h.HealthStatus != HealthWarning {
		t.Errorf("HealthStatus = %q, want warning", h.HealthStatus)
	}
	if h.HealthReason != "2 reallocated sectors" {
		t.Errorf("HealthReason = %q, want '2 reallocated sectors'", h.HealthReason)
	}
}

// Valid JSON that carries no SMART signal (e.g. an unreadable device) must not be
// reported as "healthy" — HealthStatus stays empty so the UI shows nothing.
func TestParseNoSmartSignal(t *testing.T) {
	in := `{"json_format_version":[1,0],"device":{"name":"/dev/sdz","protocol":"ATA"},"rotation_rate":0}`
	h := parseSmartData(in)
	if h.HealthStatus != "" {
		t.Errorf("HealthStatus = %q, want empty (no data to assert)", h.HealthStatus)
	}
	if h.LifePercent != nil {
		t.Errorf("LifePercent = %v, want nil", h.LifePercent)
	}
}

// Garbage / wrong-schema input yields the zero value, never a panic.
func TestParseGarbage(t *testing.T) {
	for _, in := range []string{"", "not json", `{"json_format_version":[2,0]}`} {
		h := parseSmartData(in)
		if h.HealthStatus != "" || h.LifePercent != nil {
			t.Errorf("parseSmartData(%q) = %+v, want zero value", in, h)
		}
	}
}
