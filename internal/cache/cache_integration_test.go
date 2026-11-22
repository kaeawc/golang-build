//go:build integration

package cache_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/kaeawc/golang-build/internal/cache"
	"github.com/kaeawc/golang-build/internal/testinfra"
)

var stack *testinfra.Stack

func TestMain(m *testing.M) {
	os.Exit(testinfra.Run(m, func(ctx context.Context, s *testinfra.Stack) error {
		stack = s
		_, err := s.StartValkey(ctx)
		return err
	}))
}

func newCache(t *testing.T) cache.Cache {
	t.Helper()
	c, err := cache.New(stack.Valkey.Addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func TestSetGet(t *testing.T) {
	t.Parallel()
	c := newCache(t)
	ctx := t.Context()
	key := t.Name()

	if err := c.Set(ctx, key, `{"id":1}`, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err := c.Get(ctx, key)
	if err != nil || got != `{"id":1}` {
		t.Errorf("Get = %q, %v", got, err)
	}

	if err := c.Set(ctx, key, "v2", time.Minute); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Get(ctx, key); got != "v2" {
		t.Errorf("Get after overwrite = %q, want v2", got)
	}
}

func TestGetMissingIsValkeyNil(t *testing.T) {
	t.Parallel()
	_, err := newCache(t).Get(t.Context(), t.Name())
	// Callers (handlers, admin probe) treat a nil reply as a cache miss,
	// not a failure.
	if !valkey.IsValkeyNil(err) {
		t.Errorf("Get missing key: err = %v, want valkey nil", err)
	}
}

func TestSetExpires(t *testing.T) {
	t.Parallel()
	c := newCache(t)
	ctx := t.Context()
	key := t.Name()

	if err := c.Set(ctx, key, "v", time.Second); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := c.Get(ctx, key)
		if valkey.IsValkeyNil(err) {
			return
		}
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("key did not expire")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Regression: Set used EX, which truncates to whole seconds, so Valkey
// rejected sub-second TTLs ("invalid expire time") and shortened others.
func TestSetKeepsMillisecondTTL(t *testing.T) {
	t.Parallel()
	c := newCache(t)
	ctx := t.Context()

	if err := c.Set(ctx, t.Name()+"/sub", "v", 500*time.Millisecond); err != nil {
		t.Fatalf("Set(500ms): %v", err)
	}

	key := t.Name() + "/frac"
	if err := c.Set(ctx, key, "v", 1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{stack.Valkey.Addr}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	pttl, err := client.Do(ctx, client.B().Pttl().Key(key).Build()).AsInt64()
	if err != nil {
		t.Fatal(err)
	}
	// EX would have stored 1000ms.
	if pttl <= 1000 || pttl > 1500 {
		t.Errorf("PTTL after Set(1.5s) = %dms, want (1000, 1500]", pttl)
	}

	if err := c.Set(ctx, t.Name()+"/zero", "v", 0); err == nil {
		t.Error("Set(ttl=0): want error")
	}
}

func TestNewUnreachable(t *testing.T) {
	t.Parallel()
	// valkey-go dials eagerly, so a dead address fails at construction
	// rather than on first use.
	if _, err := cache.New("127.0.0.1:1"); err == nil {
		t.Error("New(dead addr): want error")
	}
}

func TestClosedCacheErrors(t *testing.T) {
	t.Parallel()
	c, err := cache.New(stack.Valkey.Addr)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	ctx := t.Context()
	if err := c.Set(ctx, t.Name(), "v", time.Minute); err == nil {
		t.Error("Set after Close: want error")
	}
	if _, err := c.Get(ctx, t.Name()); err == nil || valkey.IsValkeyNil(err) {
		t.Errorf("Get after Close: err = %v, want a non-nil-reply error", err)
	}
}
