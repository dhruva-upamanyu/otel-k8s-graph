// SPDX-License-Identifier: Apache-2.0

package builder

// Record is the per-datapoint input the builder consumes. The OTLP
// receiver synthesizes one Record per metric datapoint, splitting the
// pmetric.ResourceMetrics attributes into:
//
//   - Attrs       — resource-level attributes (k8s.*, container.image.*,
//     telemetry.sdk.*, service.name, etc.) describing the
//     emitting process
//   - SeriesAttrs — datapoint-level attributes (span.kind, http.*,
//     url.*, db.system, server.address, etc.) describing
//     the particular observation
type Record struct {
	Attrs       map[string]string
	SeriesAttrs map[string]string
}
