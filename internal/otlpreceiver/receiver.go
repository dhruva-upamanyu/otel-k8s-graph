// SPDX-License-Identifier: Apache-2.0

// Package otlpreceiver implements the OTLP ingest path for otel-k8s-graph,
// turning collector pushes into builder.Records that derive the graph.
//
// TraceServer (traces.go) is the live receiver: graph-otel registers it as the
// OTLP TracesService and derives relationships straight from spans.
//
// Server (this file) is the original OTLP MetricsService receiver, kept for
// reference and as the equivalence-test oracle (a span and its equivalent span
// metric must derive the identical graph). It is no longer wired into the
// graph-otel binary. Only attribute sets are extracted; the actual metric
// values (sums, gauges, histograms) are discarded because graph derivation
// depends on resource and datapoint attributes alone.
package otlpreceiver

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"

	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/builder"
	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/graph"
)

// Server implements the OTLP MetricsService. Each datapoint becomes a
// builder.Record, fed through the builder into an in-memory BatchWriteSet
// that accumulates the unique discovered graph relationships.
//
// Derivation happens against a request-local set (no lock), which is then
// merged into the shared set under mu (gRPC dispatches Export concurrently).
type Server struct {
	pmetricotlp.UnimplementedGRPCServer
	mu     sync.Mutex
	set    *graph.BatchWriteSet
	logger *slog.Logger
}

func New(logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		set:    graph.NewBatchWriteSet(),
		logger: logger,
	}
}

// ExpireAndSnapshot drops entities/edges last seen before cutoff, then
// returns a deep copy of what remains plus the expired counts — all under the
// lock so it is consistent with concurrent ingestion. The caller (the
// background flusher) writes the copy to Redis without holding the lock.
func (s *Server) ExpireAndSnapshot(cutoff time.Time) (snap *graph.BatchWriteSet, expiredEntities, expiredEdges int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ee, ed := s.set.Expire(cutoff)
	return s.set.Snapshot(), ee, ed
}

// Export receives metrics as pdata directly (the gRPC codec decodes the wire
// bytes into req), so there is no re-marshal/unmarshal round trip.
func (s *Server) Export(_ context.Context, req pmetricotlp.ExportRequest) (pmetricotlp.ExportResponse, error) {
	start := time.Now()
	resp := pmetricotlp.NewExportResponse()

	// Derive into a request-local set in a single streaming pass (no lock;
	// this is the CPU-heavy part), then merge it into the shared graph set
	// under the mutex (gRPC dispatches Export concurrently).
	local, records := s.deriveAll(req.Metrics())
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

// deriveAll walks every datapoint once, deriving it straight into a
// request-local set. Datapoint attributes are read into a single reused
// buffer (cleared per datapoint) rather than a fresh map each — builder.Derive
// retains no reference to it — so a 16k-datapoint request allocates one
// datapoint-attr map, not 16k. Returns the set and the datapoint count.
func (s *Server) deriveAll(md pmetric.Metrics) (*graph.BatchWriteSet, int) {
	local := graph.NewBatchWriteSet()
	buf := make(map[string]string, 16) // reused across all datapoints
	records := 0

	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		rm := rms.At(i)
		// Resource attrs are stable for every datapoint under this resource,
		// so stringify them once (not per datapoint).
		resourceAttrs := stringifyMap(rm.Resource().Attributes())
		sms := rm.ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			metrics := sms.At(j).Metrics()
			for k := 0; k < metrics.Len(); k++ {
				records += forEachDatapoint(metrics.At(k), func(attrs pcommon.Map) {
					clear(buf)
					fillStringMap(buf, attrs)
					// Templatize high-cardinality IDs (url.full, span.name) as
					// each datapoint enters, before derivation.
					builder.TemplatizeAttrs(buf)
					builder.Derive(local, builder.Record{Attrs: resourceAttrs, SeriesAttrs: buf})
				})
			}
		}
	}
	return local, records
}

// forEachDatapoint calls fn with each datapoint's attributes, regardless of
// metric type, and returns the datapoint count. Unknown metric types yield
// nothing (graph derivation depends on attributes, not values).
func forEachDatapoint(m pmetric.Metric, fn func(pcommon.Map)) int {
	switch m.Type() {
	case pmetric.MetricTypeGauge:
		return rangeNumberPoints(m.Gauge().DataPoints(), fn)
	case pmetric.MetricTypeSum:
		return rangeNumberPoints(m.Sum().DataPoints(), fn)
	case pmetric.MetricTypeHistogram:
		dps := m.Histogram().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			fn(dps.At(i).Attributes())
		}
		return dps.Len()
	case pmetric.MetricTypeExponentialHistogram:
		dps := m.ExponentialHistogram().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			fn(dps.At(i).Attributes())
		}
		return dps.Len()
	case pmetric.MetricTypeSummary:
		dps := m.Summary().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			fn(dps.At(i).Attributes())
		}
		return dps.Len()
	default:
		return 0
	}
}

func rangeNumberPoints(dps pmetric.NumberDataPointSlice, fn func(pcommon.Map)) int {
	for i := 0; i < dps.Len(); i++ {
		fn(dps.At(i).Attributes())
	}
	return dps.Len()
}

// fillStringMap writes every attribute of in into dst (coercing values via
// AsString). dst is expected to be cleared by the caller for reuse.
func fillStringMap(dst map[string]string, in pcommon.Map) {
	in.Range(func(k string, v pcommon.Value) bool {
		dst[k] = v.AsString()
		return true
	})
}

// stringifyMap flattens a pcommon.Map into a fresh string-string map.
func stringifyMap(in pcommon.Map) map[string]string {
	out := make(map[string]string, in.Len())
	fillStringMap(out, in)
	return out
}
