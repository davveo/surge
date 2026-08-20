package main

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"

	"github.com/davveo/surge/pkg/route"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

type Router interface {
	Lookup(ctx context.Context, uid string) (*route.Record, error)
	LookupAll(ctx context.Context, uid string) ([]route.Record, error)
	Publish(ctx context.Context, gatewayID string, push *imv1.GatewayPush) error
}

type redisRouter struct {
	rdb *redis.Client
}

func newRedisRouter(rdb *redis.Client) *redisRouter {
	return &redisRouter{rdb: rdb}
}

func (r *redisRouter) Lookup(ctx context.Context, uid string) (*route.Record, error) {
	rs, err := r.LookupAll(ctx, uid)
	if err != nil || len(rs) == 0 {
		return nil, err
	}
	r0 := rs[0]
	return &r0, nil
}

func (r *redisRouter) LookupAll(ctx context.Context, uid string) ([]route.Record, error) {
	raw, err := r.rdb.Get(ctx, route.Key(uid)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("route get: %w", err)
	}
	return route.DecodeAll(raw)
}

func (r *redisRouter) Publish(ctx context.Context, gatewayID string, push *imv1.GatewayPush) error {
	if gatewayID == "" || push == nil {
		return nil
	}
	b, err := proto.Marshal(push)
	if err != nil {
		return err
	}
	return r.rdb.Publish(ctx, route.Channel(gatewayID), b).Err()
}
