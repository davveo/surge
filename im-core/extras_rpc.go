package main

import (
	"context"
	"strings"

	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

func (s *server) GetProfiles(ctx context.Context, req *imv1.GetProfilesRequest) (*imv1.GetProfilesResponse, error) {
	users, err := s.store.GetProfiles(ctx, req.GetUids())
	if err != nil {
		return nil, mapErr(err)
	}
	return &imv1.GetProfilesResponse{Users: users}, nil
}

func (s *server) RemoveFriend(ctx context.Context, req *imv1.RemoveFriendRequest) (*imv1.RemoveFriendResponse, error) {
	if err := s.store.RemoveFriend(ctx, req.GetUid(), req.GetPeerUid()); err != nil {
		return nil, mapErr(err)
	}
	return &imv1.RemoveFriendResponse{PeerUid: req.GetPeerUid()}, nil
}

func (s *server) RequestFriend(ctx context.Context, req *imv1.AddFriendRequest) (*imv1.FriendRequestState, error) {
	st, err := s.store.RequestFriend(ctx, req.GetUid(), req.GetPeerUid())
	if err != nil {
		return nil, mapErr(err)
	}
	_ = s.store.EnsureUser(ctx, req.GetUid())
	_ = s.store.EnsureUser(ctx, req.GetPeerUid())
	return &imv1.FriendRequestState{FromUid: req.GetUid(), ToUid: req.GetPeerUid(), Status: st}, nil
}

func (s *server) AcceptFriend(ctx context.Context, req *imv1.AddFriendRequest) (*imv1.AddFriendResponse, error) {
	if err := s.store.AcceptFriend(ctx, req.GetPeerUid(), req.GetUid()); err != nil {
		return nil, mapErr(err)
	}
	return &imv1.AddFriendResponse{PeerUid: req.GetPeerUid()}, nil
}

func (s *server) DeclineFriend(ctx context.Context, req *imv1.AddFriendRequest) (*imv1.FriendRequestState, error) {
	if err := s.store.DeclineFriend(ctx, req.GetPeerUid(), req.GetUid()); err != nil {
		return nil, mapErr(err)
	}
	return &imv1.FriendRequestState{FromUid: req.GetPeerUid(), ToUid: req.GetUid(), Status: "declined"}, nil
}

func (s *server) ListFriendRequests(ctx context.Context, req *imv1.ListFriendsRequest) (*imv1.ListFriendRequestsResponse, error) {
	in, out, err := s.store.ListFriendRequests(ctx, req.GetUid())
	if err != nil {
		return nil, mapErr(err)
	}
	resp := &imv1.ListFriendRequestsResponse{}
	for _, p := range in {
		resp.Incoming = append(resp.Incoming, &imv1.FriendRequestState{FromUid: p[0], ToUid: p[1], Status: "pending"})
	}
	for _, p := range out {
		resp.Outgoing = append(resp.Outgoing, &imv1.FriendRequestState{FromUid: p[0], ToUid: p[1], Status: "pending"})
	}
	return resp, nil
}

func (s *server) BlockUser(ctx context.Context, req *imv1.BlockUserRequest) (*imv1.BlockUserResponse, error) {
	if err := s.store.BlockUser(ctx, req.GetUid(), req.GetPeerUid()); err != nil {
		return nil, mapErr(err)
	}
	return &imv1.BlockUserResponse{PeerUid: req.GetPeerUid(), Blocked: true}, nil
}

func (s *server) UnblockUser(ctx context.Context, req *imv1.BlockUserRequest) (*imv1.BlockUserResponse, error) {
	if err := s.store.UnblockUser(ctx, req.GetUid(), req.GetPeerUid()); err != nil {
		return nil, mapErr(err)
	}
	return &imv1.BlockUserResponse{PeerUid: req.GetPeerUid(), Blocked: false}, nil
}

func (s *server) ListBlocks(ctx context.Context, req *imv1.ListFriendsRequest) (*imv1.ListBlocksResponse, error) {
	uids, err := s.store.ListBlocks(ctx, req.GetUid())
	if err != nil {
		return nil, mapErr(err)
	}
	return &imv1.ListBlocksResponse{Uids: uids}, nil
}

func (s *server) SetRemark(ctx context.Context, req *imv1.SetRemarkRequest) (*imv1.Friend, error) {
	if err := s.store.SetRemark(ctx, req.GetUid(), req.GetPeerUid(), req.GetRemark()); err != nil {
		return nil, mapErr(err)
	}
	p, _ := s.store.GetProfile(ctx, req.GetPeerUid())
	f := &imv1.Friend{Uid: req.GetPeerUid(), Remark: req.GetRemark(), DisplayName: req.GetRemark()}
	if p != nil {
		f.AvatarUrl = p.AvatarUrl
		if strings.TrimSpace(f.DisplayName) == "" {
			f.DisplayName = p.DisplayName
		}
	}
	return f, nil
}

func (s *server) LeaveGroup(ctx context.Context, req *imv1.LeaveGroupRequest) (*imv1.GroupResponse, error) {
	s.notifyGroup(ctx, req.GetUid(), req.GetCid(), req.GetUid()+" 退出了群聊")
	g, err := s.store.LeaveGroup(ctx, req.GetUid(), req.GetCid())
	if err != nil {
		return nil, mapErr(err)
	}
	return protoGroup(g), nil
}

func (s *server) DissolveGroup(ctx context.Context, req *imv1.LeaveGroupRequest) (*imv1.GroupResponse, error) {
	if err := s.store.DissolveGroup(ctx, req.GetUid(), req.GetCid()); err != nil {
		return nil, mapErr(err)
	}
	return &imv1.GroupResponse{Cid: req.GetCid()}, nil
}

func (s *server) TransferOwner(ctx context.Context, req *imv1.TransferOwnerRequest) (*imv1.GroupResponse, error) {
	g, err := s.store.TransferOwner(ctx, req.GetOperatorUid(), req.GetCid(), req.GetMemberUid())
	if err != nil {
		return nil, mapErr(err)
	}
	s.notifyGroup(ctx, req.GetOperatorUid(), g.CID, req.GetOperatorUid()+" 将群主转让给 "+req.GetMemberUid())
	return protoGroup(g), nil
}

func (s *server) HideConversation(ctx context.Context, req *imv1.HideConversationRequest) (*imv1.HideConversationResponse, error) {
	if err := s.store.HideConversation(ctx, req.GetUid(), req.GetCid()); err != nil {
		return nil, mapErr(err)
	}
	return &imv1.HideConversationResponse{Cid: req.GetCid()}, nil
}

func (s *server) SetPin(ctx context.Context, req *imv1.SetMuteRequest) (*imv1.MuteState, error) {
	if err := s.store.SetPin(ctx, req.GetUid(), req.GetCid(), req.GetMuted()); err != nil {
		return nil, mapErr(err)
	}
	return &imv1.MuteState{Cid: req.GetCid(), Muted: req.GetMuted()}, nil
}

func (s *server) GetReadState(ctx context.Context, req *imv1.GetReadStateRequest) (*imv1.GetReadStateResponse, error) {
	n, members, readers, err := s.store.GetReadState(ctx, req.GetUid(), req.GetCid(), req.GetConvSeq())
	if err != nil {
		return nil, mapErr(err)
	}
	resp := &imv1.GetReadStateResponse{
		Cid: req.GetCid(), ReadCount: uint32(n), ReaderUids: readers, MemberCount: uint32(members),
	}
	if cursors, _, err := s.store.ListReadCursors(ctx, req.GetUid(), req.GetCid()); err == nil {
		for uid, seq := range cursors {
			resp.Cursors = append(resp.Cursors, &imv1.ReadCursor{Uid: uid, ConvSeq: seq})
		}
	}
	return resp, nil
}
