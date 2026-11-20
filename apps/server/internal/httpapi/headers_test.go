package httpapi_test

import "testing"

func TestEveryResponseCarriesTheBaselineSecurityHeaders(t *testing.T) {
	e := newEnv(t, nil)
	for _, path := range []string{"/health", "/api/nope", "/app"} {
		h := e.do("GET", path, "").Header
		if h.Get("x-content-type-options") != "nosniff" || h.Get("x-frame-options") != "SAMEORIGIN" || h.Get("referrer-policy") != "strict-origin-when-cross-origin" {
			t.Fatalf("%s: %v", path, h)
		}
	}
}
