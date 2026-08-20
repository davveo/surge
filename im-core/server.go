package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/google/uuid"
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

	if !conv.IsGroup(canonical) {
		ok, err := s.store.AreFriends(ctx, req.GetFromUid(), peer)
		if err != nil {
			return nil, mapErr(err)
		}
		if !ok {
			return nil, mapErr(fmt.Errorf("%w: add friend first", errNotFriends))
		}
	}

	res, err := s.store.Send(ctx, req.GetFromUid(), req.GetClientMsgId(), req.GetCid(), req.GetPeerUid(), req.GetPayload(), req.GetQuoteMsgId())
	if err != nil {
		return nil, mapErr(err)
	}

	out := &imv1.SendMessageResponse{Ack: res.ack, PeerUid: res.peerUID, PeerPush: res.peerPush}
	for _, d := range res.deliveries {
		rt := s.publish(ctx, d.uid, &imv1.GatewayPush{Uid: d.uid, Push: d.push})
		if rt != nil {
			out.FanoutRoutes = append(out.FanoutRoutes, rt)
			if out.PeerRoute == nil {
				out.PeerRoute = rt
			}
		}
	}
	return out, nil
}

func (s *server) publish(ctx context.Context, uid string, gp *imv1.GatewayPush) *imv1.RecipientHint {
	if s.router == nil || gp == nil {
		return nil
	}
	rt, err := s.router.Lookup(ctx, uid)
	if err != nil {
		log.Printf("route lookup %s: %v", uid, err)
		return nil
	}
	if rt == nil {
		return nil
	}
	gp.Uid = uid
	gp.ConnId = rt.ConnID
	if err := s.router.Publish(ctx, rt.GatewayID, gp); err != nil {
		log.Printf("publish %s: %v", uid, err)
		return nil
	}
	return &imv1.RecipientHint{Uid: uid, GatewayId: rt.GatewayID, ConnId: rt.ConnID}
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
	_ = s.store.EnsureUser(ctx, req.GetUid())
	_ = s.store.EnsureUser(ctx, req.GetPeerUid())
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

func (s *server) LookupUser(ctx context.Context, req *imv1.LookupUserRequest) (*imv1.LookupUserResponse, error) {
	q := strings.TrimSpace(req.GetQuery())
	if q == "" {
		return nil, mapErr(fmt.Errorf("%w: query required", errInvalid))
	}
	p, err := s.store.GetProfile(ctx, q)
	if err != nil {
		if errors.Is(err, errInvalid) {
			return &imv1.LookupUserResponse{Uid: q, Found: false}, nil
		}
		return nil, mapErr(err)
	}
	return &imv1.LookupUserResponse{Uid: p.Uid, Found: true}, nil
}

func (s *server) SearchUsers(ctx context.Context, req *imv1.SearchUsersRequest) (*imv1.SearchUsersResponse, error) {
	users, err := s.store.SearchUsers(ctx, req.GetQuery(), int(req.GetLimit()))
	if err != nil {
		return nil, mapErr(err)
	}
	return &imv1.SearchUsersResponse{Users: users}, nil
}

func (s *server) Register(ctx context.Context, req *imv1.RegisterRequest) (*imv1.UserProfile, error) {
	p, err := s.store.Register(ctx, req.GetUid(), req.GetPassword())
	if err != nil {
		return nil, mapErr(err)
	}
	return p, nil
}

func (s *server) VerifyPassword(ctx context.Context, req *imv1.LoginRequest) (*imv1.UserProfile, error) {
	p, err := s.store.VerifyPassword(ctx, req.GetUid(), req.GetPassword())
	if err != nil {
		return nil, mapErr(err)
	}
	return p, nil
}

func (s *server) GetProfile(ctx context.Context, req *imv1.GetProfileRequest) (*imv1.UserProfile, error) {
	p, err := s.store.GetProfile(ctx, req.GetUid())
	if err != nil {
		return nil, mapErr(err)
	}
	return p, nil
}

func (s *server) UpdateProfile(ctx context.Context, req *imv1.UpdateProfileRequest) (*imv1.UserProfile, error) {
	p, err := s.store.UpdateProfile(ctx, req.GetUid(), req.GetDisplayName(), req.GetAvatarUrl())
	if err != nil {
		return nil, mapErr(err)
	}
	return p, nil
}

func (s *server) UpdateGroup(ctx context.Context, req *imv1.UpdateGroupRequest) (*imv1.GroupResponse, error) {
	g, err := s.store.UpdateGroup(ctx, req.GetOperatorUid(), req.GetCid(), req.GetName(), req.GetAvatarUrl())
	if err != nil {
		return nil, mapErr(err)
	}
	if n := strings.TrimSpace(req.GetName()); n != "" {
		s.notifyGroup(ctx, req.GetOperatorUid(), g.CID, req.GetOperatorUid()+" 将群名改为「"+g.Name+"」")
	}
	return protoGroup(g), nil
}

func (s *server) SetMute(ctx context.Context, req *imv1.SetMuteRequest) (*imv1.MuteState, error) {
	if err := s.store.SetMute(ctx, req.GetUid(), req.GetCid(), req.GetMuted()); err != nil {
		return nil, mapErr(err)
	}
	return &imv1.MuteState{Cid: req.GetCid(), Muted: req.GetMuted()}, nil
}

func (s *server) ListMutes(ctx context.Context, req *imv1.ListMutesRequest) (*imv1.ListMutesResponse, error) {
	cids, err := s.store.ListMutes(ctx, req.GetUid())
	if err != nil {
		return nil, mapErr(err)
	}
	return &imv1.ListMutesResponse{Cids: cids}, nil
}

func (s *server) CreateGroup(ctx context.Context, req *imv1.CreateGroupRequest) (*imv1.CreateGroupResponse, error) {
	g, err := s.store.CreateGroup(ctx, req.GetOwnerUid(), req.GetName(), req.GetMemberUids())
	if err != nil {
		return nil, mapErr(err)
	}
	s.notifyGroup(ctx, req.GetOwnerUid(), g.CID, req.GetOwnerUid()+" 创建了群聊「"+g.Name+"」")
	return &imv1.CreateGroupResponse{Cid: g.CID, Name: g.Name}, nil
}

func (s *server) InviteGroup(ctx context.Context, req *imv1.InviteGroupRequest) (*imv1.GroupResponse, error) {
	before, err := s.store.GetGroup(ctx, req.GetOperatorUid(), req.GetCid())
	if err != nil {
		return nil, mapErr(err)
	}
	g, err := s.store.InviteGroup(ctx, req.GetOperatorUid(), req.GetCid(), req.GetMemberUids())
	if err != nil {
		return nil, mapErr(err)
	}
	if added := memberDiff(before, g); len(added) > 0 {
		s.notifyGroup(ctx, req.GetOperatorUid(), g.CID, req.GetOperatorUid()+" 邀请 "+strings.Join(added, "、")+" 加入群聊")
	}
	return protoGroup(g), nil
}

func (s *server) KickGroup(ctx context.Context, req *imv1.KickGroupRequest) (*imv1.GroupResponse, error) {
	cur, err := s.store.GetGroup(ctx, req.GetOperatorUid(), req.GetCid())
	if err != nil {
		return nil, mapErr(err)
	}
	if cur.OwnerUID != req.GetOperatorUid() {
		return nil, mapErr(errNotOwner)
	}
	s.notifyGroup(ctx, req.GetOperatorUid(), req.GetCid(), req.GetOperatorUid()+" 将 "+req.GetMemberUid()+" 移出群聊")
	g, err := s.store.KickGroup(ctx, req.GetOperatorUid(), req.GetCid(), req.GetMemberUid())
	if err != nil {
		return nil, mapErr(err)
	}
	return protoGroup(g), nil
}

func (s *server) notifyGroup(ctx context.Context, fromUID, cid, text string) {
	_, err := s.Send(ctx, &imv1.SendMessageRequest{
		FromUid:     fromUID,
		ClientMsgId: "sys-" + uuid.NewString(),
		Cid:         cid,
		Payload:     &imv1.Payload{Type: imv1.Payload_SYSTEM, Text: text},
	})
	if err != nil {
		log.Printf("group notify %s: %v", cid, err)
	}
}

func memberDiff(before, after *groupInfo) []string {
	seen := map[string]struct{}{}
	if before != nil {
		for _, m := range before.Members {
			seen[m.UID] = struct{}{}
		}
	}
	var added []string
	if after != nil {
		for _, m := range after.Members {
			if _, ok := seen[m.UID]; !ok {
				added = append(added, m.UID)
			}
		}
	}
	return added
}

func (s *server) GetGroup(ctx context.Context, req *imv1.GetGroupRequest) (*imv1.GroupResponse, error) {
	g, err := s.store.GetGroup(ctx, req.GetUid(), req.GetCid())
	if err != nil {
		return nil, mapErr(err)
	}
	return protoGroup(g), nil
}

func (s *server) Recall(ctx context.Context, req *imv1.RecallMessageRequest) (*imv1.RecallNotify, error) {
	n, members, err := s.store.Recall(ctx, req.GetUid(), req.GetCid(), req.GetMsgId())
	if err != nil {
		return nil, mapErr(err)
	}
	for _, uid := range members {
		if uid == req.GetUid() {
			continue
		}
		s.publish(ctx, uid, &imv1.GatewayPush{Uid: uid, Recalled: n})
	}
	return n, nil
}

func (s *server) MarkRead(ctx context.Context, req *imv1.MarkReadRequest) (*imv1.ReadReceipt, error) {
	if err := s.store.MarkRead(ctx, req.GetUid(), req.GetCid(), req.GetConvSeq()); err != nil {
		return nil, mapErr(err)
	}
	rc := &imv1.ReadReceipt{Cid: req.GetCid(), FromUid: req.GetUid(), ConvSeq: req.GetConvSeq()}
	if !conv.IsGroup(req.GetCid()) {
		if peer, err := conv.PeerUID(req.GetCid(), req.GetUid()); err == nil {
			s.publish(ctx, peer, &imv1.GatewayPush{Uid: peer, Read: rc})
		}
	}
	return rc, nil
}

func (s *server) FanoutTyping(ctx context.Context, req *imv1.Typing) (*imv1.Typing, error) {
	cid := req.GetCid()
	from := req.GetFromUid()
	if cid == "" || from == "" {
		return req, nil
	}
	if conv.IsGroup(cid) {
		members, err := s.store.GroupMembers(ctx, cid)
		if err != nil {
			return nil, mapErr(err)
		}
		ok := false
		for _, uid := range members {
			if uid == from {
				ok = true
				break
			}
		}
		if !ok {
			return nil, mapErr(errNotMember)
		}
		for _, uid := range members {
			if uid == from {
				continue
			}
			s.publish(ctx, uid, &imv1.GatewayPush{Uid: uid, Typing: req})
		}
	} else if peer, err := conv.PeerUID(cid, from); err == nil {
		s.publish(ctx, peer, &imv1.GatewayPush{Uid: peer, Typing: req})
	}
	return req, nil
}

func mapErr(err error) error {
	if errors.Is(err, errInvalid) {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if errors.Is(err, errNotFriends) {
		return status.Error(codes.PermissionDenied, err.Error())
	}
	if errors.Is(err, errNotMember) || errors.Is(err, errNotOwner) {
		return status.Error(codes.PermissionDenied, err.Error())
	}
	if errors.Is(err, errTooLarge) {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if errors.Is(err, errAuth) {
		return status.Error(codes.Unauthenticated, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}
