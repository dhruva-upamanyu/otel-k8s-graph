// SPDX-License-Identifier: Apache-2.0

package otlpreceiver

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

// A raw SERVER HTTP span and the equivalent span-metric datapoint must derive
// the identical graph. The trace receiver only changes how the builder.Record
// is sourced (span fields + span attrs vs datapoint attrs); the downstream
// derivation is untouched, so both paths must yield the same entities/edges.
func TestTraceServer_MatchesMetricsForServerEndpoint(t *testing.T) {
	setResource := func(m pcommon.Map) {
		m.PutStr("k8s.namespace.name", "default")
		m.PutStr("k8s.pod.name", "auth-1")
		m.PutStr("k8s.container.name", "app")
		m.PutStr("service.name", "auth-service")
	}
	setSeries := func(m pcommon.Map) {
		m.PutStr("http.request.method", "POST")
		m.PutStr("http.route", "/api/auth/validate")
	}

	// --- metrics path: span.kind is an explicit datapoint attribute ---
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	setResource(rm.Resource().Attributes())
	dp := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty().SetEmptyGauge().DataPoints().AppendEmpty()
	setSeries(dp.Attributes())
	dp.Attributes().PutStr("span.kind", "SPAN_KIND_SERVER")

	metricsSrv := New(nil)
	if _, err := metricsSrv.Export(context.Background(), pmetricotlp.NewExportRequestFromMetrics(md)); err != nil {
		t.Fatalf("metrics export: %v", err)
	}
	mSnap, _, _ := metricsSrv.ExpireAndSnapshot(time.Time{})
	wantE, wantEd, wantM := mSnap.Counts()
	if wantE == 0 || wantEd == 0 {
		t.Fatalf("metrics path derived nothing: entities=%d edges=%d", wantE, wantEd)
	}

	// --- traces path: span.kind comes from span.Kind(), service.name from the
	// resource. Same logical observation, raw OTLP trace shape. ---
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	setResource(rs.Resource().Attributes())
	span := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span.SetKind(ptrace.SpanKindServer)
	setSeries(span.Attributes())

	traceSrv := NewTrace(nil)
	if _, err := traceSrv.Export(context.Background(), ptraceotlp.NewExportRequestFromTraces(td)); err != nil {
		t.Fatalf("traces export: %v", err)
	}
	tSnap, _, _ := traceSrv.ExpireAndSnapshot(time.Time{})
	gotE, gotEd, gotM := tSnap.Counts()

	if gotE != wantE || gotEd != wantEd || gotM != wantM {
		t.Errorf("trace graph differs from metrics graph:\n  metrics: entities=%d edges=%d metas=%d\n  traces:  entities=%d edges=%d metas=%d",
			wantE, wantEd, wantM, gotE, gotEd, gotM)
	}
}

// A CLIENT database span carries its operation in span.name (a top-level span
// field, not an attribute). The trace receiver must inject span.Name() so the
// builder derives the QUERIES edge action identically to the metrics path,
// where span.name is a datapoint attribute.
func TestTraceServer_MatchesMetricsForDatabaseQuery(t *testing.T) {
	setResource := func(m pcommon.Map) {
		m.PutStr("k8s.namespace.name", "default")
		m.PutStr("k8s.pod.name", "auth-1")
		m.PutStr("k8s.container.name", "app")
		m.PutStr("service.name", "auth-service")
	}
	setSeries := func(m pcommon.Map) {
		m.PutStr("db.system", "mysql")
		m.PutStr("server.address", "10.0.0.3")
		m.PutStr("server.port", "3306")
	}

	// --- metrics path: span.kind and span.name are datapoint attributes ---
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	setResource(rm.Resource().Attributes())
	dp := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty().SetEmptyGauge().DataPoints().AppendEmpty()
	setSeries(dp.Attributes())
	dp.Attributes().PutStr("span.kind", "SPAN_KIND_CLIENT")
	dp.Attributes().PutStr("span.name", "SELECT auth.users")

	metricsSrv := New(nil)
	if _, err := metricsSrv.Export(context.Background(), pmetricotlp.NewExportRequestFromMetrics(md)); err != nil {
		t.Fatalf("metrics export: %v", err)
	}
	mSnap, _, _ := metricsSrv.ExpireAndSnapshot(time.Time{})
	wantE, wantEd, wantM := mSnap.Counts()
	if wantE == 0 || wantEd == 0 {
		t.Fatalf("metrics path derived nothing: entities=%d edges=%d", wantE, wantEd)
	}

	// --- traces path: span.kind from span.Kind(), span.name from span.Name() ---
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	setResource(rs.Resource().Attributes())
	span := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span.SetKind(ptrace.SpanKindClient)
	span.SetName("SELECT auth.users")
	setSeries(span.Attributes())

	traceSrv := NewTrace(nil)
	if _, err := traceSrv.Export(context.Background(), ptraceotlp.NewExportRequestFromTraces(td)); err != nil {
		t.Fatalf("traces export: %v", err)
	}
	tSnap, _, _ := traceSrv.ExpireAndSnapshot(time.Time{})
	gotE, gotEd, gotM := tSnap.Counts()

	if gotE != wantE || gotEd != wantEd || gotM != wantM {
		t.Errorf("trace graph differs from metrics graph:\n  metrics: entities=%d edges=%d metas=%d\n  traces:  entities=%d edges=%d metas=%d",
			wantE, wantEd, wantM, gotE, gotEd, gotM)
	}
}
