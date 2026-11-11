package cache

import (
	"context"
	"github.com/redis/go-redis/v9"
)

type ChatNotifier struct{ Client *redis.Client }

func ChatChannel(id string) string { return "qingye:chat:notify:" + id }
func (n ChatNotifier) Notify(ctx context.Context, ids ...string) {
	if n.Client == nil {
		return
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			_ = n.Client.Publish(ctx, ChatChannel(id), "changed").Err()
		}
	}
}
