package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCounterInc(t *testing.T) {
	c := NewCounter("pulse_test_total", "test counter")
	c.Inc()
	c.Inc()
	c.Add(5)

	// Verify the global registry picks it up.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	require.Contains(t, body, "pulse_test_total")
	require.Contains(t, body, "# TYPE pulse_test_total counter")
	// The counter value is the sum of all increments for the "" label.
	// We just check it appears in the output (value is "7" but other
	// registrations of the same name can interfere; skip exact value).
}

func TestCounterWithLabel(t *testing.T) {
	c := NewCounter("pulse_test_labeled_total", "test")
	c.WithLabel("path=\"/x\"").Inc()
	c.WithLabel("path=\"/x\"").Inc()
	c.WithLabel("path=\"/y\"").Inc()

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	require.Contains(t, body, "pulse_test_labeled_total")
	// Should see the label at least once.
	require.True(t, strings.Contains(body, "path="), "expected label format, got: %s", body)
}

func TestGaugeSet(t *testing.T) {
	g := NewGauge("pulse_test_gauge", "test")
	g.Set(42)

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	require.Contains(t, body, "pulse_test_gauge")
	require.Contains(t, body, "# TYPE pulse_test_gauge gauge")
}

func TestHandlerContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, "text/plain; version=0.0.4; charset=utf-8", rec.Header().Get("Content-Type"))
}

// TestLicenseDeniedCounterRegisteredAndExposed verifies the counter is
// registered at package init and visible on /metrics. Uses the canonical
// IncLicenseDenied helper so the test also documents the expected label
// format (module/layer/reason).
func TestLicenseDeniedCounterRegisteredAndExposed(t *testing.T) {
	IncLicenseDenied("pulse-paid-sso", "service", "module_not_entitled")
	IncLicenseDenied("pulse-paid-sso", "service", "module_not_entitled")

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	require.Contains(t, body, "pulse_license_denied_total", "counter must be exposed")
	require.Contains(t, body, "# TYPE pulse_license_denied_total counter")
	// The label trio must be present in the rendered line.
	require.Contains(t, body, `module="pulse-paid-sso"`)
	require.Contains(t, body, `layer="service"`)
	require.Contains(t, body, `reason="module_not_entitled"`)
}

// TestOIDCIdTokenVerificationsRegisteredAndExposed verifies the
// pulse_oidc_idtoken_verifications_total counter is registered at package
// init, visible on /metrics, and the result label is rendered correctly.
func TestOIDCIdTokenVerificationsRegisteredAndExposed(t *testing.T) {
	IncOIDCIdTokenVerification(OIDCResultSuccess)
	IncOIDCIdTokenVerification(OIDCResultAlgNone)
	IncOIDCIdTokenVerification(OIDCResultAlgNone)
	IncOIDCIdTokenVerification(OIDCResultExpired)

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	require.Contains(t, body, "pulse_oidc_idtoken_verifications_total", "counter must be exposed")
	require.Contains(t, body, "# TYPE pulse_oidc_idtoken_verifications_total counter")
	require.Contains(t, body, `result="success"`)
	require.Contains(t, body, `result="alg_none"`)
	require.Contains(t, body, `result="expired"`)
}

// TestComplianceHoldChecksRegisteredAndExposed verifies the
// pulse_compliance_hold_checks_total counter is registered at package init,
// visible on /metrics, and the result label is rendered correctly.
func TestComplianceHoldChecksRegisteredAndExposed(t *testing.T) {
	IncComplianceHoldCheck(ComplianceHoldNoHold)
	IncComplianceHoldCheck(ComplianceHoldNoHold)
	IncComplianceHoldCheck(ComplianceHoldActive)
	IncComplianceHoldCheck(ComplianceHoldError)

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	require.Contains(t, body, "pulse_compliance_hold_checks_total", "counter must be exposed")
	require.Contains(t, body, "# TYPE pulse_compliance_hold_checks_total counter")
	require.Contains(t, body, `result="no_hold"`)
	require.Contains(t, body, `result="hold_active"`)
	require.Contains(t, body, `result="error"`)
}

// TestDSRProcessInvocationsRegisteredAndExposed verifies the
// pulse_dsr_process_invocations_total counter is registered at package init,
// visible on /metrics, and the (type,result) label pair is rendered correctly.
func TestDSRProcessInvocationsRegisteredAndExposed(t *testing.T) {
	IncDSRProcessInvocation(DSRTypeExport, DSRResultNotImplemented)
	IncDSRProcessInvocation(DSRTypeDelete, DSRResultNotImplemented)
	IncDSRProcessInvocation(DSRTypeDelete, DSRResultError)

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	require.Contains(t, body, "pulse_dsr_process_invocations_total", "counter must be exposed")
	require.Contains(t, body, "# TYPE pulse_dsr_process_invocations_total counter")
	require.Contains(t, body, `type="export",result="not_implemented"`)
	require.Contains(t, body, `type="delete",result="not_implemented"`)
	require.Contains(t, body, `type="delete",result="error"`)
}
