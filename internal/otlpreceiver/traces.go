// SPDX-License-Identifier: Apache-2.0

package otlpreceiver

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"

	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/builder"
	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/flows"
	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/graph"
)

// TraceServer implements the OTLP TracesService. It is the trace-native twin of
// Server: instead of reading attribute sets off metric datapoints, it sources
// the same builder.Record shape directly from OTLP spans — span.kind from
// span.Kind(), span.name from span.Name(), and everything else from the span's
// own attributes — so the builder, graph, and flusher downstream are reused
// unchanged. Like Server, it accumulates discovered relationships in an
// in-memory BatchWriteSet guarded by mu (gRPC dispatches Export concurrently).
type TraceServer struct {
	ptraceotlp.UnimplementedGRPCServer
	mu     sync.Mutex
	set    *graph.BatchWriteSet
	logger *slog.Logger
	// flowSink, when non-nil, receives a deep-copied SpanRecord per span for the
	// trace-flow assembler. nil keeps Export to the entity-graph path only.
	flowSink func(flows.SpanRecord)
}

func NewTrace(logger *slog.Logger) *TraceServer {
	if logger == nil {
		logger = slog.Default()
	}
	return &TraceServer{
		set:    graph.NewBatchWriteSet(),
		logger: logger,
	}
}

// SetFlowSink attaches a secondary consumer that receives a SpanRecord per span.
// Not safe to call concurrently with Export; set it once at startup.
func (s *TraceServer) SetFlowSink(fn func(flows.SpanRecord)) { s.flowSink = fn }

// ExpireAndSnapshot drops entities/edges last seen before cutoff, then returns
// a deep copy of what remains plus the expired counts — all under the lock so
// it is consistent with concurrent ingestion. Mirrors Server.ExpireAndSnapshot
// so the background flusher can drive either receiver identically.
func (s *TraceServer) ExpireAndSnapshot(cutoff time.Time) (snap *graph.BatchWriteSet, expiredEntities, expiredEdges int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ee, ed := s.set.Expire(cutoff)
	return s.set.Snapshot(), ee, ed
}

// Export receives traces as pdata directly (the gRPC codec decodes the wire
// bytes into req), so there is no re-marshal/unmarshal round trip.
func (s *TraceServer) Export(_ context.Context, req ptraceotlp.ExportRequest) (ptraceotlp.ExportResponse, error) {
	start := time.Now()
	resp := ptraceotlp.NewExportResponse()

	// Derive into a request-local set in a single streaming pass (no lock),
	// then merge it into the shared graph set under the mutex.
	local, records := s.deriveAll(req.Traces())
	if records == 0 {
		return resp, nil
	}

	s.mu.Lock()
	s.set.Merge(local, time.Now())
	entities, edges, metas := s.set.Counts()
	s.mu.Unlock()

	s.logger.Info("graph relationships discovered",
		slog.Int("records", records),
		slog.Int("entities", entities),
		slog.Int("edges", edges),
		slog.Int("metas", metas),
		slog.Int64("duration_ms", time.Since(start).Milliseconds()))
	return resp, nil
}

// deriveAll walks every span once, deriving it straight into a request-local
// set. Span attributes are read into a single reused buffer (cleared per span)
// rather than a fresh map each — builder.Derive retains no reference to it.
// span.kind and span.name are not span attributes; they are top-level span
// fields, so we inject them into the buffer to reconstruct the same Record a
// span-metric datapoint would carry. Returns the set and the span count.
func (s *TraceServer) deriveAll(td ptrace.Traces) (*graph.BatchWriteSet, int) {
	local := graph.NewBatchWriteSet()
	buf := make(map[string]string, 16) // reused across all spans
	records := 0

	rss := td.ResourceSpans()
	for i := 0; i < rss.Len(); i++ {
		rs := rss.At(i)
		// Resource attrs are stable for every span under this resource (and
		// carry service.name, which the builder falls back to), so stringify
		// them once.
		resourceAttrs := stringifyMap(rs.Resource().Attributes())
		sss := rs.ScopeSpans()
		for j := 0; j < sss.Len(); j++ {
			spans := sss.At(j).Spans()
			for k := 0; k < spans.Len(); k++ {
				span := spans.At(k)
				clear(buf)
				fillStringMap(buf, span.Attributes())
				// span.kind/span.name are span fields, not attributes; inject
				// them in the metric-datapoint attribute form the builder
				// expects (normalizeSpanKind upper-cases "Server" -> "SERVER").
				buf["span.kind"] = span.Kind().String()
				buf["span.name"] = span.Name()
				builder.TemplatizeAttrs(buf)
				builder.Derive(local, builder.Record{Attrs: resourceAttrs, SeriesAttrs: buf})
				records++

				if s.flowSink != nil {
					s.flowSink(spanRecord(span, resourceAttrs, buf["span.name"]))
				}
			}
		}
	}
	return local, records
}

// spanRecord deep-copies the fields the flow assembler needs from an OTLP span.
// pdata is freed after Export returns, so every field is copied. templatizedName
// is buf["span.name"] (already run through builder.TemplatizeAttrs). Deployment
// is k8s.deployment.name, falling back to service.name.
func spanRecord(span ptrace.Span, resourceAttrs map[string]string, templatizedName string) flows.SpanRecord {
	deployment := resourceAttrs["k8s.deployment.name"]
	if deployment == "" {
		deployment = resourceAttrs["service.name"]
	}
	parentID := ""
	if pid := span.ParentSpanID(); !pid.IsEmpty() {
		parentID = pid.String()
	}
	return flows.SpanRecord{
		TraceID:    span.TraceID().String(),
		SpanID:     span.SpanID().String(),
		ParentID:   parentID,
		Deployment: deployment,
		Namespace:  resourceAttrs["k8s.namespace.name"],
		Name:       templatizedName,
		StartNano:  uint64(span.StartTimestamp()),
		EndNano:    uint64(span.EndTimestamp()),
		Attrs:      stringifyMap(span.Attributes()),
	}
}
