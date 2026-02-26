package cache

import (
	"context"
	"time"

	"github.com/valkey-io/valkey-go"
)

type Cache interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, ttl time.Duration) error
	Close()
}

type valkeyCache struct{ client valkey.Client }

func New(addr string) (Cache, error) {
	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{addr}})
	if err != nil {
		return nil, err
	}
	return &valkeyCache{client: client}, nil
}

func (c *valkeyCache) Get(ctx context.Context, key string) (string, error) {
	return c.client.Do(ctx, c.client.B().Get().Key(key).Build()).ToString()
}

func (c *valkeyCache) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	return c.client.Do(ctx, c.client.B().Set().Key(key).Value(value).Ex(ttl).Build()).Error()
}

func (c *valkeyCache) Close() { c.client.Close() }
