// Package metrics provides Prometheus metrics for Pulse services.
//
// All metrics live in a global Registry. Each role (cell, gateway, admin)
// registers its own counters/histograms. Exposed at /metrics on the API
// server (and on each role's HTTP listener).
//
// Convention: every metric name is prefixed with `pulse_`.
package metrics

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Registry is a thread-safe collection of metrics.
type Registry struct {
	mu         sync.RWMutex
	counters   map[string]*Counter
	gauges     map[string]*Gauge
	histograms map[string]*Histogram
}

// global is the default registry used by Pulse services.
var global = &Registry{
	counters:   make(map[string]*Counter),
	gauges:     make(map[string]*Gauge),
	histograms: make(map[string]*Histogram),
}

// Counter is a monotonically increasing value with optional labels.
type Counter struct {
	name   string
	help   string
	mu     sync.RWMutex
	values map[string]*uint64
}

// Gauge is a value that can go up/down.
type Gauge struct {
	name  string
	help  string
	value int64
}

// Histogram is a Prometheus-style bucketed observation tracker. Tracks
// count + sum + per-bucket counts so WriteTo can render _bucket{le="N"}
// lines plus _count + _sum.
type Histogram struct {
	name   string
	help   string
	mu     sync.RWMutex
	bounds []float64
	values map[string]*histogramCells
}

// histogramCells holds the per-label-set observation state.
type histogramCells struct {
	count   uint64
	sum     float64
	buckets []uint64 // len == len(bounds); cumulative count <= bound
}

// NewCounter registers (or returns existing) counter.
func NewCounter(name, help string) *Counter {
	global.mu.Lock()
	defer global.mu.Unlock()
	if c, ok := global.counters[name]; ok {
		return c
	}
	c := &Counter{name: name, help: help, values: make(map[string]*uint64)}
	global.counters[name] = c
	return c
}

// NewGauge registers (or returns existing) gauge.
func NewGauge(name, help string) *Gauge {
	global.mu.Lock()
	defer global.mu.Unlock()
	if g, ok := global.gauges[name]; ok {
		return g
	}
	g := &Gauge{name: name, help: help}
	global.gauges[name] = g
	return g
}

// NewHistogram registers (or returns existing) histogram. Bounds are
// upper-inclusive (le="N"); +Inf is added implicitly on render.
func NewHistogram(name, help string, bounds ...float64) *Histogram {
	global.mu.Lock()
	defer global.mu.Unlock()
	if h, ok := global.histograms[name]; ok {
		return h
	}
	cp := make([]float64, len(bounds))
	copy(cp, bounds)
	h := &Histogram{
		name:   name,
		help:   help,
		bounds: cp,
		values: make(map[string]*histogramCells),
	}
	global.histograms[name] = h
	return h
}

// Inc increments the counter by 1 (no labels).
func (c *Counter) Inc() { c.Add(1) }

// Add increments the counter by n (no labels).
func (c *Counter) Add(n uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.values[""]
	if v == nil {
		v = new(uint64)
		c.values[""] = v
	}
	atomic.AddUint64(v, n)
}

// WithLabel returns a sub-counter for a specific label string.
// Labels are formatted as `key="value",key="value"`.
func (c *Counter) WithLabel(label string) *CounterHandle {
	return &CounterHandle{c: c, label: label}
}

// CounterHandle is a label-scoped counter.
type CounterHandle struct {
	c     *Counter
	label string
}

// Inc increments with the configured label.
func (h *CounterHandle) Inc() { h.Add(1) }

// Add increments with the configured label.
func (h *CounterHandle) Add(n uint64) {
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	v := h.c.values[h.label]
	if v == nil {
		v = new(uint64)
		h.c.values[h.label] = v
	}
	atomic.AddUint64(v, n)
}

// Set updates the gauge to n.
func (g *Gauge) Set(n int64) { atomic.StoreInt64(&g.value, n) }

// Inc increments by 1.
func (g *Gauge) Inc() { g.Add(1) }

// Add increments by n.
func (g *Gauge) Add(n int64) { atomic.AddInt64(&g.value, n) }

// Observe records v in the histogram. Buckets are upper-inclusive.
func (h *Histogram) Observe(v float64) { h.ObserveWithLabel(v, "") }

// ObserveWithLabel records v under the given label string.
func (h *Histogram) ObserveWithLabel(v float64, label string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cells := h.values[label]
	if cells == nil {
		cells = &histogramCells{buckets: make([]uint64, len(h.bounds))}
		h.values[label] = cells
	}
	cells.count++
	cells.sum += v
	for i, b := range h.bounds {
		if v <= b {
			cells.buckets[i]++
		}
	}
}

// Handler returns an http.Handler that serves the registry in
// Prometheus text exposition format.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		WriteTo(w)
	})
}

// WriteTo renders the registry to w in Prometheus text exposition format.
func WriteTo(w io.Writer) {
	global.mu.RLock()
	defer global.mu.RUnlock()

	var b strings.Builder
	for _, c := range sortedKeys(global.counters) {
		counter := global.counters[c]
		b.WriteString("# HELP ")
		b.WriteString(counter.name)
		b.WriteByte(' ')
		b.WriteString(counter.help)
		b.WriteByte('\n')
		b.WriteString("# TYPE ")
		b.WriteString(counter.name)
		b.WriteString(" counter\n")
		counter.mu.RLock()
		keys := make([]string, 0, len(counter.values))
		for k := range counter.values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(counter.name)
			if k != "" {
				b.WriteString(fmt.Sprintf("{%s}", k))
			}
			b.WriteByte(' ')
			b.WriteString(fmt.Sprintf("%d", atomic.LoadUint64(counter.values[k])))
			b.WriteByte('\n')
		}
		counter.mu.RUnlock()
	}
	for _, c := range sortedKeys(global.gauges) {
		gauge := global.gauges[c]
		b.WriteString("# HELP ")
		b.WriteString(gauge.name)
		b.WriteByte(' ')
		b.WriteString(gauge.help)
		b.WriteByte('\n')
		b.WriteString("# TYPE ")
		b.WriteString(gauge.name)
		b.WriteString(" gauge\n")
		b.WriteString(gauge.name)
		b.WriteByte(' ')
		b.WriteString(fmt.Sprintf("%d", atomic.LoadInt64(&gauge.value)))
		b.WriteByte('\n')
	}
	for _, c := range sortedKeys(global.histograms) {
		hist := global.histograms[c]
		b.WriteString("# HELP ")
		b.WriteString(hist.name)
		b.WriteByte(' ')
		b.WriteString(hist.help)
		b.WriteByte('\n')
		b.WriteString("# TYPE ")
		b.WriteString(hist.name)
		b.WriteString(" histogram\n")
		hist.mu.RLock()
		keys := make([]string, 0, len(hist.values))
		for k := range hist.values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			cells := hist.values[k]
			baseLabel := k
			if baseLabel != "" {
				baseLabel += ","
			}
			for i, bound := range hist.bounds {
				writeHistogramBucket(&b, hist.name, baseLabel, bound, cells.buckets[i])
			}
			writeHistogramBucket(&b, hist.name, baseLabel, -1, cells.count) // +Inf
			b.WriteString(hist.name)
			b.WriteString("_count")
			if k != "" {
				b.WriteString(fmt.Sprintf("{%s}", k))
			}
			b.WriteByte(' ')
			b.WriteString(fmt.Sprintf("%d", cells.count))
			b.WriteByte('\n')
			b.WriteString(hist.name)
			b.WriteString("_sum")
			if k != "" {
				b.WriteString(fmt.Sprintf("{%s}", k))
			}
			b.WriteByte(' ')
			b.WriteString(fmt.Sprintf("%g", cells.sum))
			b.WriteByte('\n')
		}
		hist.mu.RUnlock()
	}
	_, _ = io.WriteString(w, b.String())
}

// writeHistogramBucket emits one Prometheus _bucket line. bound == -1
// is the +Inf bucket.
func writeHistogramBucket(b *strings.Builder, name, baseLabel string, bound float64, count uint64) {
	b.WriteString(name)
	b.WriteString("_bucket{")
	b.WriteString(baseLabel)
	if bound == -1 {
		b.WriteString("le=\"+Inf\"")
	} else {
		b.WriteString(fmt.Sprintf("le=\"%g\"", bound))
	}
	b.WriteString("} ")
	b.WriteString(fmt.Sprintf("%d", count))
	b.WriteByte('\n')
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Compile-time interface guard.
var _ = http.Handler(nil)

// Standard Pulse metrics (registered at package init).
var (
	HTTPRequestsTotal = NewCounter("pulse_http_requests_total", "Total HTTP requests served")
	HTTPRequestErrors = NewCounter("pulse_http_request_errors_total", "Total HTTP request errors")
	MessagesSent      = NewCounter("pulse_messages_sent_total", "Total messages sent")
	MessagesDelivered = NewCounter("pulse_messages_delivered_total", "Total messages delivered to recipients")
	ConnectionsActive = NewGauge("pulse_connections_active", "Number of active WebSocket connections")
	UsersRegistered   = NewCounter("pulse_users_registered_total", "Total users registered")
	GroupsCreated     = NewCounter("pulse_groups_created_total", "Total groups created")

	// LicenseDeniedTotal counts every time the license gate denied a request,
	// instrumented at both enforcement layers so operators can distinguish a
	// denied HTTP request from a denied service call. Labels:
	//   module: paid module id (e.g. "pulse-paid-sso")
	//   layer:  "http" | "service" — where the denial originated
	//   reason: short tag like "module_not_entitled" | "license_expired" | "gate_nil"
	LicenseDeniedTotal = NewCounter(
		"pulse_license_denied_total",
		"Total license gate denials, labelled by module, layer, and reason",
	)

	// OIDCIdTokenVerificationsTotal counts every VerifyIDToken result.
	// Labels: result — see the OIDCResult* constants.
	OIDCIdTokenVerificationsTotal = NewCounter(
		"pulse_oidc_idtoken_verifications_total",
		"Total OIDC id_token verification results",
	)

	// ComplianceHoldChecksTotal counts every compliance.IsHeld check observed
	// via the message service. Labels: result — one of "no_hold", "hold_active", "error".
	ComplianceHoldChecksTotal = NewCounter(
		"pulse_compliance_hold_checks_total",
		"Total compliance hold-check results from the message service",
	)

	// DSRProcessInvocationsTotal counts every ProcessExport/ProcessDelete call.
	// Labels: type — "export" | "delete"; result — "in_progress" | "not_implemented" | "error".
	DSRProcessInvocationsTotal = NewCounter(
		"pulse_dsr_process_invocations_total",
		"Total DSR process invocations, labelled by type and result",
	)

	// BackupOperationsTotal counts every backup operation invocation.
	// Labels: kind — "pitr" | "nats_snapshot"; result — "ok" | "fail".
	BackupOperationsTotal = NewCounter(
		"pulse_backup_operations_total",
		"Total backup operations, labelled by kind and result",
	)

	// DSRProcessDuration is a histogram of wall-clock seconds spent in
	// DSRWorker.processJob for the async delete pipeline.
	DSRProcessDuration = NewHistogram(
		"pulse_dsr_process_duration_seconds",
		"Wall-clock seconds spent processing one async DSR delete job",
		0.5, 1, 2, 5, 10, 30, 60, 120, 300,
	)

	// WhitelabelRenderDuration is a histogram of wall-clock seconds spent
	// in TemplateStore.Render for the whitelabel admin endpoint.
	WhitelabelRenderDuration = NewHistogram(
		"pulse_whitelabel_render_duration_seconds",
		"Wall-clock seconds spent rendering one whitelabel template",
		0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0,
	)
)

// IncLicenseDenied is the canonical counter increment for license-gate denials.
// It exists so call sites do not need to construct the label string by hand
// (typos would silently produce new time series). Module IDs MUST be the
// canonical "pulse-paid-*" form; layer MUST be "http" or "service"; reason
// SHOULD be one of "module_not_entitled", "license_expired", "gate_nil".
func IncLicenseDenied(module, layer, reason string) {
	LicenseDeniedTotal.WithLabel(
		"module=\"" + module + "\",layer=\"" + layer + "\",reason=\"" + reason + "\"",
	).Inc()
}

// OIDC verification result label values.
const (
	OIDCResultSuccess            = "success"
	OIDCResultInvalidSignature   = "invalid_signature"
	OIDCResultExpired            = "expired"
	OIDCResultAlgNone            = "alg_none"
	OIDCResultHSRejected         = "hs_rejected"
	OIDCResultWrongIssuer        = "wrong_issuer"
	OIDCResultWrongAudience      = "wrong_audience"
	OIDCResultMissingNonce       = "missing_nonce"
	OIDCResultJWKSError          = "jwks_error"
	OIDCResultOther              = "other"
)

// Compliance hold-check result label values.
const (
	ComplianceHoldNoHold    = "no_hold"
	ComplianceHoldActive    = "hold_active"
	ComplianceHoldError     = "error"
)

// DSR invocation label values.
const (
	DSRTypeExport = "export"
	DSRTypeDelete = "delete"

	DSRResultInProgress     = "in_progress"
	DSRResultNotImplemented = "not_implemented"
	DSRResultError          = "error"
	DSRResultSuccess        = "success"
	DSRResultFailed         = "failed"
)

// IncOIDCIdTokenVerification increments pulse_oidc_idtoken_verifications_total
// with result=<result>. Callers MUST use the OIDCResult* constants; bare
// strings would silently produce new time series.
func IncOIDCIdTokenVerification(result string) {
	OIDCIdTokenVerificationsTotal.WithLabel("result=\"" + result + "\"").Inc()
}

// IncComplianceHoldCheck increments pulse_compliance_hold_checks_total with
// result=<result>. Callers MUST use the ComplianceHold* constants.
func IncComplianceHoldCheck(result string) {
	ComplianceHoldChecksTotal.WithLabel("result=\"" + result + "\"").Inc()
}

// IncDSRProcessInvocation increments pulse_dsr_process_invocations_total with
// type=<dsrType>, result=<result>. dsrType MUST be one of DSRTypeExport,
// DSRTypeDelete; result MUST be one of DSRResult*.
func IncDSRProcessInvocation(dsrType, result string) {
	DSRProcessInvocationsTotal.WithLabel(
		"type=\"" + dsrType + "\",result=\"" + result + "\"",
	).Inc()
}

// Backup operation label values.
//
// kind MUST be one of BackupKindPITR, BackupKindNATSSnapshot.
// result MUST be one of BackupResultOK, BackupResultFail.
const (
	BackupKindPITR         = "pitr"
	BackupKindNATSSnapshot = "nats_snapshot"

	BackupResultOK   = "ok"
	BackupResultFail = "fail"
)

// Whitelabel template label values.
//
// template MUST be one of "welcome", "password_reset", "invite".
const (
	WhitelabelTemplateWelcome       = "welcome"
	WhitelabelTemplatePasswordReset = "password_reset"
	WhitelabelTemplateInvite        = "invite"
)

// ObserveWhitelabelRender records a single render latency under
// template=<template>. Callers MUST use the WhitelabelTemplate*
// constants; bare strings would silently produce new time series.
func ObserveWhitelabelRender(seconds float64, template string) {
	WhitelabelRenderDuration.ObserveWithLabel(seconds, "template=\""+template+"\"")
}

// IncBackupOperation increments pulse_backup_operations_total with
// kind=<kind>, result=<result>. kind MUST be one of BackupKind*;
// result MUST be one of BackupResult*. Bare strings would silently
// produce new time series.
func IncBackupOperation(kind, result string) {
	BackupOperationsTotal.WithLabel(
		"kind=\"" + kind + "\",result=\"" + result + "\"",
	).Inc()
}
