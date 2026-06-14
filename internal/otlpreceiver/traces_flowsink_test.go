// SPDX-License-Identifier: Apache-2.0

package otlpreceiver

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"

	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/flows"
)

func TestTraceServer_TeesSpanRecordsToFlowSink(t *testing.T) {
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	ra := rs.Resource().Attributes()
	ra.PutStr("k8s.namespace.name", "default")
	ra.PutStr("k8s.deployment.name", "gateway")
	ra.PutStr("service.name", "gateway-svc")
	span := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span.SetName("GET /orders/123")
	span.SetKind(ptrace.SpanKindServer)
	span.Attributes().PutStr("http.route", "/orders/{id}")
	var spanID pcommon.SpanID
	spanID[0] = 0x01
	span.SetSpanID(spanID)

	var got []flows.SpanRecord
	srv := NewTrace(nil)
	srv.SetFlowSink(func(r flows.SpanRecord) { got = append(got, r) })

	if _, err := srv.Export(context.Background(), ptraceotlp.NewExportRequestFromTraces(td)); err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 span record, got %d", len(got))
	}
	r := got[0]
	if r.Deployment != "gateway" || r.Namespace != "default" {
		t.Fatalf("identity wrong: dep=%q ns=%q", r.Deployment, r.Namespace)
	}
	if r.Name != "GET /orders/{n}" {
		t.Fatalf("name should be templatized, got %q", r.Name)
	}
	if r.ParentID != "" || r.SpanID == "" {
		t.Fatalf("root span should have empty ParentID and a SpanID, got parent=%q span=%q", r.ParentID, r.SpanID)
	}
	if r.Attrs["http.route"] != "/orders/{id}" {
		t.Fatalf("attrs not copied: %+v", r.Attrs)
	}
}

func TestTraceServer_DeploymentFallsBackToServiceName(t *testing.T) {
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", "only-svc")
	rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("op")

	var got []flows.SpanRecord
	srv := NewTrace(nil)
	srv.SetFlowSink(func(r flows.SpanRecord) { got = append(got, r) })
	if _, err := srv.Export(context.Background(), ptraceotlp.NewExportRequestFromTraces(td)); err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(got) != 1 || got[0].Deployment != "only-svc" {
		t.Fatalf("deployment should fall back to service.name, got %+v", got)
	}
}
