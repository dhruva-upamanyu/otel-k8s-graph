// SPDX-License-Identifier: Apache-2.0

package flows

import (
	"context"
	"encoding/json"

	"github.com/redis/go-redis/v9"
)

// WriteFlowsToRedis upserts each flow as a hash under <prefix>:flow:<roothash>
// and registers its id in <prefix>:flow:ids. The canonical structure (with
// per-node metadata) is stored as one JSON blob; edges are implicit nesting and
// carry no data.
func WriteFlowsToRedis(ctx context.Context, rdb *redis.Client, prefix string, flows []FlowSnapshot) error {
	if len(flows) == 0 {
		return nil
	}
	idsKey := prefix + ":flow:ids"
	pipe := rdb.Pipeline()
	for _, f := range flows {
		structureJSON, err := json.Marshal(f.Structure)
		if err != nil {
			return err
		}
		key := prefix + ":flow:" + f.RootHash
		pipe.HSet(ctx, key,
			"root_hash", f.RootHash,
			"count", f.Count,
			"first_seen_ms", f.FirstSeenMs,
			"last_seen_ms", f.LastSeenMs,
			"node_count", f.NodeCount,
			"structure", string(structureJSON),
		)
		pipe.SAdd(ctx, idsKey, f.RootHash)
	}
	_, err := pipe.Exec(ctx)
	return err
}
