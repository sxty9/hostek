package hardware

import (
	"strconv"
	"sync"
	"testing"
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
