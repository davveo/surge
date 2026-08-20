package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/davveo/surge/pkg/conv"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

type locker struct {
	mu sync.Mutex
	m  map[string]*sync.Mutex
}

func newLocker() *locker {
	return &locker{m: map[string]*sync.Mutex{}}
}

func (l *locker) lock(id string) func() {
	l.mu.Lock()
	m, ok := l.m[id]
	if !ok {
		m = &sync.Mutex{}
		l.m[id] = m
	}
	l.mu.Unlock()
	m.Lock()
	return m.Unlock
}

type server struct {
	imv1.UnimplementedIMCoreServer
	store  Store
	router Router
	locks  *locker
}

func newServer(store Store, router Router) *server {
	return &server{store: store, router: router, locks: newLocker()}
}

func (s *server) Send(ctx context.Context, req *imv1.SendMessageRequest) (*imv1.SendMessageResponse, error) {
	canonical, peer, err := conv.ResolveCID(req.GetFromUid(), req.GetCid(), req.GetPeerUid())
	if err != nil {
		return nil, mapErr(fmt.Errorf("%w: %v", errInvalid, err))
	}
	unlock := s.locks.lock(canonical)
	defer unlock()

	ok, err := s.store.AreFriends(ctx, req.GetFromUid(), peer)
	if err != nil {
		return nil, mapErr(err)
	}
	if !ok {
		return nil, mapErr(fmt.Errorf("%w: add friend first", errNotFriends))
	}

	res, err := s.store.Send(ctx, req.GetFromUid(), req.GetClientMsgId(), req.GetCid(), req.GetPeerUid(), req.GetPayload())
	if err != nil {
		return nil, mapErr(err)
	}

	out := &imv1.SendMessageResponse{Ack: res.ack, PeerUid: res.peerUID, PeerPush: res.peerPush}
	if res.peerPush != nil && s.router != nil {
		rt, err := s.router.Lookup(ctx, res.peerUID)
		if err != nil {
			log.Printf("route lookup %s: %v", res.peerUID, err)
		} else if rt != nil {
			out.PeerRoute = &imv1.RecipientHint{Uid: res.peerUID, GatewayId: rt.GatewayID, ConnId: rt.ConnID}
			push := &imv1.GatewayPush{Uid: res.peerUID, ConnId: rt.ConnID, Push: res.peerPush}
			if err := s.router.Publish(ctx, rt.GatewayID, push); err != nil {
				log.Printf("publish push %s: %v", res.peerUID, err)
			}
		}
	}
	return out, nil
}

func (s *server) Sync(ctx context.Context, req *imv1.SyncInboxRequest) (*imv1.SyncInboxResponse, error) {
	sync, err := s.store.Sync(ctx, req.GetUid(), req.GetLastSyncSeq(), int(req.GetLimit()))
	if err != nil {
		return nil, mapErr(err)
	}
	return &imv1.SyncInboxResponse{Sync: sync}, nil
}

func (s *server) Watermark(ctx context.Context, req *imv1.WatermarkRequest) (*imv1.WatermarkResponse, error) {
	seq, err := s.store.Watermark(ctx, req.GetUid())
	if err != nil {
		return nil, mapErr(err)
	}
	return &imv1.WatermarkResponse{LastSyncSeq: seq}, nil
}

func (s *server) ListConversations(ctx context.Context, req *imv1.ListConversationsRequest) (*imv1.ListConversationsResponse, error) {
	list, err := s.store.ListConversations(ctx, req.GetUid())
	if err != nil {
		return nil, mapErr(err)
	}
	return &imv1.ListConversationsResponse{Conversations: list}, nil
}

func (s *server) GetTimeline(ctx context.Context, req *imv1.GetTimelineRequest) (*imv1.GetTimelineResponse, error) {
	cid, msgs, err := s.store.Timeline(ctx, req.GetUid(), req.GetCid(), req.GetAfterConvSeq(), int(req.GetLimit()))
	if err != nil {
		return nil, mapErr(err)
	}
	return &imv1.GetTimelineResponse{Cid: cid, Messages: msgs}, nil
}

func (s *server) AddFriend(ctx context.Context, req *imv1.AddFriendRequest) (*imv1.AddFriendResponse, error) {
	already, err := s.store.AddFriend(ctx, req.GetUid(), req.GetPeerUid())
	if err != nil {
		return nil, mapErr(err)
	}
	return &imv1.AddFriendResponse{PeerUid: req.GetPeerUid(), Already: already}, nil
}

func (s *server) ListFriends(ctx context.Context, req *imv1.ListFriendsRequest) (*imv1.ListFriendsResponse, error) {
	ids, err := s.store.ListFriends(ctx, req.GetUid())
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]*imv1.Friend, 0, len(ids))
	for _, id := range ids {
		out = append(out, &imv1.Friend{Uid: id})
	}
	return &imv1.ListFriendsResponse{Friends: out}, nil
}

func (s *server) LookupUser(_ context.Context, req *imv1.LookupUserRequest) (*imv1.LookupUserResponse, error) {
	q := strings.TrimSpace(req.GetQuery())
	if q == "" {
		return nil, mapErr(fmt.Errorf("%w: query required", errInvalid))
	}
	return &imv1.LookupUserResponse{Uid: q, Found: true}, nil
}

func mapErr(err error) error {
	if errors.Is(err, errInvalid) {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if errors.Is(err, errNotFriends) {
		return status.Error(codes.PermissionDenied, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}
