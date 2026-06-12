// SPDX-License-Identifier: Apache-2.0

package graph

// Redis key builders for the graph schema. These MUST match the formats
// RedisGraph reads:
//
//	<prefix>:entity:<id>            HASH
//	<prefix>:entity:<id>:metadata   HASH
//	<prefix>:entity:<id>:edges      SET
//	<prefix>:by_kind:<kind>         SET
//	<prefix>:ids                    SET
func keyEntity(prefix, id string) string     { return prefix + ":entity:" + id }
func keyMetadata(prefix, id string) string   { return prefix + ":entity:" + id + ":metadata" }
func keyEdges(prefix, id string) string      { return prefix + ":entity:" + id + ":edges" }
func keyByKind(prefix string, k Kind) string { return prefix + ":by_kind:" + string(k) }
func keyIDs(prefix string) string            { return prefix + ":ids" }
