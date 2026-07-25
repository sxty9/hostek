package hardware

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

// storeStatic must publish the static inventory (st/ctrl) and its derived thermal
// component list (thermCrit) as ONE atomic write — the Atomare-Zugriffe guarantee: a
// reader may never catch st from one probe generation together with thermCrit from
// another, because there is no observable intermediate state between them.
//
// We tag each generation with a matching marker in both fields, hammer the publisher
// from one goroutine, and have several readers grab the lock and assert the two fields
// always agree. Run under -race it also proves the access is data-race free. Were the
// publish ever split back into two critical sections, a reader could acquire the lock in
// the gap and see the new inventory beside the old component list — this test catches it.
func TestStoreStaticPublishesAtomically(t *testing.T) {
	c := New()
	const gens = 5000
	const readers = 4

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				c.mu.RLock()
				host := c.st.Hostname
				var label string
				if len(c.thermCrit) > 0 {
					label = c.thermCrit[0].Label
				}
				c.mu.RUnlock()
				// Both fields carry the same per-generation tag once anything is published;
				// a mismatch means a reader observed a torn (non-atomic) publish.
				if label != "" && label != host {
					t.Errorf("torn static publish: st.Hostname=%q but thermCrit[0].Label=%q", host, label)
					return
				}
			}
		}()
	}

	for i := 0; i < gens; i++ {
		tag := "gen-" + strconv.Itoa(i)
		c.storeStatic(Info{Hostname: tag}, nil, []ThermalMeta{{Key: "cpu", Label: tag}})
	}
	close(stop)
	wg.Wait()
}

// storeSmart must publish the SMART health map, the per-disk liveness times, and the
// "probe has run" set as ONE atomic write — the same Atomare-Zugriffe guarantee: a reader
// (Disks) may never catch these three maps from different probe generations. Each
// generation tags all three maps with a single matching key; readers grab the lock and
// assert the three keys always agree. Under -race it also proves the access is data-race
// free. Split the publish back into separate critical sections and a reader could land in
// the gap and see mismatched generations — this test catches it.
func TestStoreSmartPublishesAtomically(t *testing.T) {
	c := New()
	const gens = 5000
	const readers = 4

	// soleKey returns the one key of a single-entry map (each generation writes exactly
	// one), or "" before anything is published.
	soleKey := func() (sh, ok, tr string) {
		for k := range c.smart {
			sh = k
		}
		for k := range c.smartOK {
			ok = k
		}
		for k := range c.smartTried {
			tr = k
		}
		return
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				c.mu.RLock()
				sh, ok, tr := soleKey()
				c.mu.RUnlock()
				// Once anything is published the three maps carry the same per-generation
				// key; a mismatch means a reader observed a torn (non-atomic) publish.
				if sh != "" && (sh != ok || sh != tr) {
					t.Errorf("torn SMART publish: smart=%q smartOK=%q smartTried=%q", sh, ok, tr)
					return
				}
			}
		}()
	}

	for i := 0; i < gens; i++ {
		tag := "gen-" + strconv.Itoa(i)
		c.storeSmart(
			map[string]SmartHealth{tag: {}},
			map[string]time.Time{tag: {}},
			map[string]bool{tag: true},
		)
	}
	close(stop)
	wg.Wait()
}

// thermalSample is the temperature-selection logic lifted out of the pool write so it can
// be exercised directly. It records a component only when it reports a real (>0) reading:
// CPU from the dynamic package temp, each GPU by row index, each disk from its SMART temp.
func TestThermalSample(t *testing.T) {
	crit := []ThermalMeta{{Key: "cpu"}, {Key: "gpu0"}, {Key: "gpu1"}, {Key: "sda"}}
	smart := map[string]SmartHealth{"sda": {TempC: 41}}
	d := dynamic{
		cpuTemp: 55,
		gpu:     []gpuDynamic{{tempC: 60}, {tempC: 0}}, // gpu1 reads 0 → no sensor → skipped
	}

	s, ok := thermalSample(100, d, crit, smart)
	if !ok {
		t.Fatal("thermalSample returned ok=false, want a sample")
	}
	if s.Time != 100 {
		t.Errorf("Time = %d, want 100", s.Time)
	}
	want := map[string]float64{"cpu": 55, "gpu0": 60, "sda": 41}
	if len(s.Temps) != len(want) {
		t.Errorf("Temps = %v, want %v", s.Temps, want)
	}
	for k, v := range want {
		if s.Temps[k] != v {
			t.Errorf("Temps[%q] = %v, want %v", k, s.Temps[k], v)
		}
	}
	// A 0 °C reading is "no sensor", not a data point — gpu1 must not appear.
	if _, present := s.Temps["gpu1"]; present {
		t.Errorf("gpu1 (0 °C) leaked into Temps: %v", s.Temps)
	}
}

// With no component list, or when nothing reports a temperature, there is nothing to
// record and thermalSample returns ok=false so the pool append is skipped.
func TestThermalSampleEmpty(t *testing.T) {
	if _, ok := thermalSample(1, dynamic{cpuTemp: 50}, nil, nil); ok {
		t.Error("no components returned ok=true, want false")
	}
	crit := []ThermalMeta{{Key: "cpu"}, {Key: "sda"}}
	// cpuTemp 0 (no sensor) and sda absent from SMART → temps empty → ok=false.
	if _, ok := thermalSample(1, dynamic{}, crit, nil); ok {
		t.Error("all-zero readings returned ok=true, want false")
	}
}
