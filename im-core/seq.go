package main

import (
	"context"
	"fmt"
	"sort"

	"github.com/redis/go-redis/v9"
)

type Seq interface {
	Next(ctx context.Context, key string) (uint64, error)
}

type redisSeq struct {
	rdb *redis.Client
}

func newRedisSeq(rdb *redis.Client) *redisSeq {
	return &redisSeq{rdb: rdb}
}

func (s *redisSeq) Next(ctx context.Context, key string) (uint64, error) {
	n, err := s.rdb.Incr(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("seq incr %s: %w", key, err)
	}
	return uint64(n), nil
}

func convSeqKey(cid string) string { return "seq:conv:" + cid }
func syncSeqKey(uid string) string { return "seq:sync:" + uid }

func seqKeys(cid string, uids []string) []string {
	keys := make([]string, 0, 1+len(uids))
	keys = append(keys, convSeqKey(cid))
	for _, uid := range uids {
		if uid != "" {
			keys = append(keys, syncSeqKey(uid))
		}
	}
	return uniqueSorted(keys)
}

func uniqueSorted(keys []string) []string {
	if len(keys) == 0 {
		return keys
	}
	sort.Strings(keys)
	n := 0
	for _, k := range keys {
		if k == "" {
			continue
		}
		if n == 0 || keys[n-1] != k {
			keys[n] = k
			n++
		}
	}
	return keys[:n]
}

type memSeq struct {
	n map[string]uint64
}

func newMemSeq() *memSeq {
	return &memSeq{n: map[string]uint64{}}
}

func (s *memSeq) Next(_ context.Context, key string) (uint64, error) {
	s.n[key]++
	return s.n[key], nil
}
