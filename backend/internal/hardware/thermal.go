package hardware

import (
	"encoding/json"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

const thermalCap = 180 // ~6 min of history at the 2s dynamic tick

// ThermalMeta describes one temperature-measurable component and its critical limit.
type ThermalMeta struct {
	Key       string  `json:"key"`    // "cpu", "gpu0", "sda", …
	Label     string  `json:"label"`  // human label
	Source    string  `json:"source"` // "nvidia" | "smart" | "default"
	CriticalC float64 `json:"criticalC"`
}

// ThermalSample is one timestamped set of component temperatures (°C).
type ThermalSample struct {
	Time  int64              `json:"time"`
	Temps map[string]float64 `json:"temps"`
}

// ThermalResponse is the Thermal-tab payload: the component list (with critical
// thresholds) plus the recent temperature time-series.
type ThermalResponse struct {
	Components []ThermalMeta   `json:"components"`
	Samples    []ThermalSample `json:"samples"`
}

// Thermal returns the per-component temperature series and critical thresholds.
func (c *Collector) Thermal() ThermalResponse {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return ThermalResponse{
		Components: append([]ThermalMeta(nil), c.thermCrit...),
		Samples:    append([]ThermalSample(nil), c.thermRing...),
	}
}

// appendThermalLocked records one temperature sample for every known component.
// Must be called with c.mu held.
func (c *Collector) appendThermalLocked(now int64, d dynamic) {
	if len(c.thermCrit) == 0 {
		return
	}
	temps := make(map[string]float64)
	for _, m := range c.thermCrit {
		switch {
		case m.Key == "cpu":
			if d.cpuTemp > 0 {
				temps[m.Key] = d.cpuTemp
			}
		case strings.HasPrefix(m.Key, "gpu"):
			idx := atoi(strings.TrimPrefix(m.Key, "gpu"))
			if idx >= 0 && idx < len(d.gpu) && d.gpu[idx].tempC > 0 {
				temps[m.Key] = d.gpu[idx].tempC
			}
		default: // a disk
			if sd, ok := c.smart[m.Key]; ok && sd.TempC > 0 {
				temps[m.Key] = sd.TempC
			}
		}
	}
	if len(temps) == 0 {
		return
	}
	c.thermRing = append(c.thermRing, ThermalSample{Time: now, Temps: temps})
	if len(c.thermRing) > thermalCap {
		c.thermRing = append([]ThermalSample(nil), c.thermRing[len(c.thermRing)-thermalCap:]...)
	}
}

// computeThermalMeta builds the component list with critical limits — read from
// firmware/driver where possible (GPU slowdown via nvidia-smi), else the established
// standard default (AMD Tjmax 90 °C / Intel 100 °C; SSD 70 °C / HDD 60 °C / NVMe 80 °C).
func computeThermalMeta(info Info) []ThermalMeta {
	var out []ThermalMeta

	// CPU — no firmware/hwmon critical is exposed on most boards, so use the
	// vendor's junction-temperature standard.
	cpuCrit := 95.0
	v := strings.ToLower(info.CPU.Vendor)
	switch {
	case strings.Contains(v, "amd"):
		cpuCrit = 90
	case strings.Contains(v, "intel"):
		cpuCrit = 100
	}
	out = append(out, ThermalMeta{Key: "cpu", Label: "CPU", Source: "default", CriticalC: cpuCrit})

	// GPU(s) — slowdown (throttle) temperature from nvidia-smi, per device.
	slow := gpuSlowdownTemps()
	for i, g := range info.GPUs {
		crit, src := 95.0, "default"
		if i < len(slow) && slow[i] > 0 {
			crit, src = slow[i], "nvidia"
		}
		label := g.Name
		if label == "" {
			label = "GPU " + strconv.Itoa(i)
		}
		out = append(out, ThermalMeta{Key: "gpu" + strconv.Itoa(i), Label: label, Source: src, CriticalC: crit})
	}

	// Disks — default by media type.
	for _, d := range lsblkAllDisks() {
		crit := 70.0 // SATA/other SSD
		switch {
		case strings.EqualFold(d.Tran, "nvme"):
			crit = 80
		case d.Rota:
			crit = 60
		}
		label := d.Model
		if label == "" {
			label = d.Name
		}
		out = append(out, ThermalMeta{Key: d.Name, Label: label + " (" + d.Name + ")", Source: "default", CriticalC: crit})
	}
	return out
}

var gpuSlowRe = regexp.MustCompile(`(?mi)^\s*GPU Slowdown Temp\s*:\s*(\d+)`)

// gpuSlowdownTemps parses the per-GPU slowdown temperature from nvidia-smi.
func gpuSlowdownTemps() []float64 {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return nil
	}
	out, ok := runCmd(cmdTimeout, "nvidia-smi", "-q", "-d", "TEMPERATURE")
	if !ok {
		return nil
	}
	var res []float64
	for _, m := range gpuSlowRe.FindAllStringSubmatch(out, -1) {
		res = append(res, atof(m[1]))
	}
	return res
}

// lsblkAllDisks lists every whole physical disk with model/transport/rotational.
func lsblkAllDisks() []lsblkDev {
	out, ok := runCmd(cmdTimeout, "lsblk", "-J", "-d", "-o", "NAME,MODEL,TRAN,ROTA,TYPE")
	if !ok {
		return nil
	}
	var doc struct {
		Blockdevices []lsblkNode `json:"blockdevices"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return nil
	}
	var res []lsblkDev
	for _, n := range doc.Blockdevices {
		if n.Type != "disk" {
			continue
		}
		res = append(res, lsblkDev{Name: n.Name, Model: strings.TrimSpace(n.Model), Tran: n.Tran, Rota: bool(n.Rota)})
	}
	return res
}
