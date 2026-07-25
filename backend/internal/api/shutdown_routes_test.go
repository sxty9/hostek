package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hostek/internal/auth"
	"hostek/internal/shutdown"
)

// testHandler builds the routed handler with a real verifier + shutdown manager. The
// metric/hardware collectors are nil: the shutdown routes exercised here never touch
// them. With no state file configured, the manager reports disarmed, so these tests can
// never trigger an actual poweroff.
func testHandler() http.Handler {
	v := auth.NewVerifier([]byte("test-secret-please-ignore-0123456789"), "")
	return New(v, nil, nil, shutdown.New()).Handler()
}

func req(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// The two bare /shutdown routes must be reachable with NO session (that is the whole
// point — the login page hits them before anyone is logged in).
func TestShutdownRoutesArePublic(t *testing.T) {
	h := testHandler()

	if w := req(t, h, "GET", "/api/services/hostek/shutdown", ""); w.Code != http.StatusOK {
		t.Fatalf("GET shutdown (no cookie) = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if w := req(t, h, "POST", "/api/services/hostek/shutdown", "{}"); w.Code != http.StatusBadRequest {
		t.Fatalf("POST shutdown (empty body) = %d, want 400", w.Code)
	}
	// A password while disarmed is refused as "not enabled" — not a 401, and never a poweroff.
	if w := req(t, h, "POST", "/api/services/hostek/shutdown", `{"password":"whatever"}`); w.Code != http.StatusForbidden {
		t.Fatalf("POST shutdown (disarmed) = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}

// The admin config routes must be gated: without a valid session they are 401. This is
// the guard that keeps a random visitor from arming/disarming or changing the password.
func TestShutdownConfigRoutesRequireAuth(t *testing.T) {
	h := testHandler()

	if w := req(t, h, "GET", "/api/services/hostek/config/shutdown", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("GET config/shutdown (no cookie) = %d, want 401", w.Code)
	}
	if w := req(t, h, "POST", "/api/services/hostek/config/shutdown", `{"armed":true}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("POST config/shutdown (no cookie) = %d, want 401", w.Code)
	}
}
