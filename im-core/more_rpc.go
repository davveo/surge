package main

import (
	"context"

	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

func (s *server) SearchMessages(ctx context.Context, req *imv1.SearchMessagesRequest) (*imv1.SearchMessagesResponse, error) {
	hits, err := s.store.SearchMessages(ctx, req.GetUid(), req.GetQuery(), int(req.GetLimit()))
	if err != nil {
		return nil, mapErr(err)
	}
	return &imv1.SearchMessagesResponse{Hits: hits}, nil
}

func (s *server) SetGroupMuteAll(ctx context.Context, req *imv1.SetMuteRequest) (*imv1.GroupResponse, error) {
	g, err := s.store.SetGroupMuteAll(ctx, req.GetUid(), req.GetCid(), req.GetMuted())
	if err != nil {
		return nil, mapErr(err)
	}
	text := req.GetUid() + " 已关闭全员禁言"
	if req.GetMuted() {
		text = req.GetUid() + " 已开启全员禁言"
	}
	s.notifyGroup(ctx, req.GetUid(), g.CID, text)
	return protoGroup(g), nil
}

func (s *server) SetFriendTags(ctx context.Context, req *imv1.SetFriendTagsRequest) (*imv1.Friend, error) {
	if err := s.store.SetFriendTags(ctx, req.GetUid(), req.GetPeerUid(), req.GetTags()); err != nil {
		return nil, mapErr(err)
	}
	tags, _ := s.store.FriendTagsOf(ctx, req.GetUid(), req.GetPeerUid())
	return &imv1.Friend{Uid: req.GetPeerUid(), Tags: tags}, nil
}

func (s *server) ListFriendTags(ctx context.Context, req *imv1.ListFriendsRequest) (*imv1.ListFriendTagsResponse, error) {
	tags, err := s.store.ListFriendTags(ctx, req.GetUid())
	if err != nil {
		return nil, mapErr(err)
	}
	return &imv1.ListFriendTagsResponse{Tags: tags}, nil
}

func (s *server) SetPublicKey(ctx context.Context, req *imv1.SetPublicKeyRequest) (*imv1.UserProfile, error) {
	if err := s.store.SetPublicKey(ctx, req.GetUid(), req.GetPublicKey()); err != nil {
		return nil, mapErr(err)
	}
	p, err := s.store.GetProfile(ctx, req.GetUid())
	if err != nil {
		return nil, mapErr(err)
	}
	return p, nil
}

func (s *server) GetPublicKeys(ctx context.Context, req *imv1.GetProfilesRequest) (*imv1.GetProfilesResponse, error) {
	users, err := s.store.GetProfiles(ctx, req.GetUids())
	if err != nil {
		return nil, mapErr(err)
	}
	return &imv1.GetProfilesResponse{Users: users}, nil
}

func (s *server) ConsumeEphemeral(ctx context.Context, req *imv1.RecallMessageRequest) (*imv1.RecallNotify, error) {
	n, members, err := s.store.ConsumeEphemeral(ctx, req.GetUid(), req.GetCid(), req.GetMsgId())
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

func (s *server) AddSticker(ctx context.Context, req *imv1.AddStickerRequest) (*imv1.Sticker, error) {
	st, err := s.store.AddSticker(ctx, req.GetUid(), req.GetUrl(), req.GetPack())
	if err != nil {
		return nil, mapErr(err)
	}
	return st, nil
}

func (s *server) ListStickers(ctx context.Context, req *imv1.ListFriendsRequest) (*imv1.ListStickersResponse, error) {
	list, err := s.store.ListStickers(ctx, req.GetUid())
	if err != nil {
		return nil, mapErr(err)
	}
	return &imv1.ListStickersResponse{Stickers: list}, nil
}
