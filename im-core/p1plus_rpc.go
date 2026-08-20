package main

import (
	"context"
	"strings"

	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

func (s *server) DeleteMessage(ctx context.Context, req *imv1.RecallMessageRequest) (*imv1.HideConversationResponse, error) {
	if err := s.store.DeleteMessage(ctx, req.GetUid(), req.GetCid(), req.GetMsgId()); err != nil {
		return nil, mapErr(err)
	}
	return &imv1.HideConversationResponse{Cid: req.GetCid()}, nil
}

func (s *server) ClearConversation(ctx context.Context, req *imv1.HideConversationRequest) (*imv1.HideConversationResponse, error) {
	if err := s.store.ClearConversation(ctx, req.GetUid(), req.GetCid()); err != nil {
		return nil, mapErr(err)
	}
	return &imv1.HideConversationResponse{Cid: req.GetCid()}, nil
}

func (s *server) SetMember(ctx context.Context, req *imv1.SetMemberRequest) (*imv1.GroupResponse, error) {
	g, err := s.store.SetMember(ctx, req.GetOperatorUid(), req.GetCid(), req.GetMemberUid(), req.GetNickname(), req.GetRole(), req.GetMuted(), req.GetSetNickname(), req.GetSetRole(), req.GetSetMuted())
	if err != nil {
		return nil, mapErr(err)
	}
	if req.GetSetMuted() {
		text := req.GetMemberUid() + " 已被解除禁言"
		if req.GetMuted() {
			text = req.GetMemberUid() + " 已被禁言"
		}
		s.notifyGroup(ctx, req.GetOperatorUid(), g.CID, text)
	}
	if req.GetSetRole() && req.GetRole() == "admin" {
		s.notifyGroup(ctx, req.GetOperatorUid(), g.CID, req.GetOperatorUid()+" 将 "+req.GetMemberUid()+" 设为管理员")
	}
	return protoGroup(g), nil
}

func (s *server) ListJoinRequests(ctx context.Context, req *imv1.GetGroupRequest) (*imv1.ListJoinRequestsResponse, error) {
	list, err := s.store.ListJoinRequests(ctx, req.GetUid(), req.GetCid())
	if err != nil {
		return nil, mapErr(err)
	}
	out := &imv1.ListJoinRequestsResponse{Cid: req.GetCid()}
	for _, r := range list {
		out.Requests = append(out.Requests, &imv1.JoinRequest{Uid: r.UID, FromUid: r.FromUID, CreatedAtMs: r.CreatedAtMs})
	}
	return out, nil
}

func (s *server) RequestJoin(ctx context.Context, req *imv1.LeaveGroupRequest) (*imv1.GroupResponse, error) {
	g, err := s.store.RequestJoin(ctx, req.GetUid(), req.GetCid())
	if err != nil {
		return nil, mapErr(err)
	}
	s.notifyRoster(ctx, g.OwnerUID, req.GetUid(), "group_join_request", g.CID)
	return protoGroup(g), nil
}

func (s *server) DecideJoin(ctx context.Context, req *imv1.DecideJoinRequest) (*imv1.GroupResponse, error) {
	before, _ := s.store.GetGroup(ctx, req.GetOperatorUid(), req.GetCid())
	g, err := s.store.DecideJoin(ctx, req.GetOperatorUid(), req.GetCid(), req.GetMemberUid(), req.GetAccept())
	if err != nil {
		return nil, mapErr(err)
	}
	if req.GetAccept() {
		added := []string{req.GetMemberUid()}
		if before != nil {
			added = memberDiff(before, g)
		}
		if len(added) > 0 {
			s.notifyGroup(ctx, req.GetOperatorUid(), g.CID, strings.Join(added, "、")+" 加入群聊")
			for _, uid := range added {
				s.notifyRoster(ctx, uid, req.GetOperatorUid(), "group_invite", g.CID)
			}
		}
	}
	return protoGroup(g), nil
}
