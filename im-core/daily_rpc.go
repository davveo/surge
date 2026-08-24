package main

import (
	"context"
	"strings"

	"github.com/davveo/surge/pkg/conv"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

func (s *server) ReactMessage(ctx context.Context, req *imv1.ReactMessageRequest) (*imv1.ReactionList, error) {
	buckets, err := s.store.ReactMessage(ctx, req.GetUid(), req.GetCid(), req.GetMsgId(), req.GetEmoji())
	if err != nil {
		return nil, mapErr(err)
	}
	extra := req.GetCid() + " " + req.GetMsgId() + " " + req.GetEmoji()
	if conv.IsGroup(req.GetCid()) {
		if convMembers, err := s.store.GroupMembers(ctx, req.GetCid()); err == nil {
			for _, uid := range convMembers {
				if uid != req.GetUid() {
					s.notifyRoster(ctx, uid, req.GetUid(), "reaction", extra)
				}
			}
		}
	} else {
		s.notifyRoster(ctx, otherP2P(req.GetCid(), req.GetUid()), req.GetUid(), "reaction", extra)
	}
	return &imv1.ReactionList{MsgId: req.GetMsgId(), Reactions: buckets}, nil
}

func otherP2P(cid, uid string) string {
	parts := strings.Split(strings.TrimPrefix(cid, "p2p:"), ":")
	if len(parts) != 2 {
		return ""
	}
	if parts[0] == uid {
		return parts[1]
	}
	if parts[1] == uid {
		return parts[0]
	}
	return ""
}

func (s *server) AddFavorite(ctx context.Context, req *imv1.FavoriteRequest) (*imv1.Favorite, error) {
	fav, err := s.store.AddFavorite(ctx, req.GetUid(), req.GetCid(), req.GetMsgId())
	if err != nil {
		return nil, mapErr(err)
	}
	return fav, nil
}

func (s *server) ListFavorites(ctx context.Context, req *imv1.ListFavoritesRequest) (*imv1.ListFavoritesResponse, error) {
	list, err := s.store.ListFavorites(ctx, req.GetUid(), req.GetQuery())
	if err != nil {
		return nil, mapErr(err)
	}
	return &imv1.ListFavoritesResponse{Favorites: list}, nil
}

func (s *server) DeleteFavorite(ctx context.Context, req *imv1.FavoriteRequest) (*imv1.HideConversationResponse, error) {
	if err := s.store.DeleteFavorite(ctx, req.GetUid(), req.GetFavId()); err != nil {
		return nil, mapErr(err)
	}
	return &imv1.HideConversationResponse{}, nil
}

func (s *server) CreateGroupInvite(ctx context.Context, req *imv1.GetGroupRequest) (*imv1.GroupInvite, error) {
	inv, err := s.store.CreateGroupInvite(ctx, req.GetUid(), req.GetCid())
	if err != nil {
		return nil, mapErr(err)
	}
	return inv, nil
}

func (s *server) JoinByInvite(ctx context.Context, req *imv1.JoinInviteRequest) (*imv1.GroupResponse, error) {
	g, err := s.store.JoinByInvite(ctx, req.GetUid(), req.GetToken())
	if err != nil {
		return nil, mapErr(err)
	}
	return protoGroup(g), nil
}

func (s *server) SetDraft(ctx context.Context, req *imv1.SetDraftRequest) (*imv1.HideConversationResponse, error) {
	if err := s.store.SetDraft(ctx, req.GetUid(), req.GetCid(), req.GetText()); err != nil {
		return nil, mapErr(err)
	}
	return &imv1.HideConversationResponse{Cid: req.GetCid()}, nil
}

func (s *server) PinChatMessage(ctx context.Context, req *imv1.PinChatMessageRequest) (*imv1.PinnedMessage, error) {
	pin, err := s.store.PinChatMessage(ctx, req.GetUid(), req.GetCid(), req.GetMsgId())
	if err != nil {
		return nil, mapErr(err)
	}
	return pin, nil
}

func (s *server) GetPinnedMessage(ctx context.Context, req *imv1.GetGroupRequest) (*imv1.PinnedMessage, error) {
	pin, err := s.store.GetPinnedMessage(ctx, req.GetUid(), req.GetCid())
	if err != nil {
		return nil, mapErr(err)
	}
	return pin, nil
}

func (s *server) ReportMessage(ctx context.Context, req *imv1.ReportRequest) (*imv1.HideConversationResponse, error) {
	if err := s.store.ReportMessage(ctx, req.GetUid(), req.GetCid(), req.GetMsgId(), req.GetReason()); err != nil {
		return nil, mapErr(err)
	}
	return &imv1.HideConversationResponse{Cid: req.GetCid()}, nil
}

func (s *server) GetSettings(ctx context.Context, req *imv1.ListFriendsRequest) (*imv1.UserSettings, error) {
	st, err := s.store.GetSettings(ctx, req.GetUid())
	if err != nil {
		return nil, mapErr(err)
	}
	return st, nil
}

func (s *server) GetSettingsBatch(ctx context.Context, req *imv1.GetProfilesRequest) (*imv1.SettingsBatchResponse, error) {
	hide, err := s.store.HideLastSeenMap(ctx, req.GetUids())
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]*imv1.UserSettings, 0, len(req.GetUids()))
	for _, uid := range uniqueUIDs(req.GetUids()) {
		out = append(out, &imv1.UserSettings{Uid: uid, HideLastSeen: hide[uid]})
	}
	return &imv1.SettingsBatchResponse{Settings: out}, nil
}

func (s *server) SetSettings(ctx context.Context, req *imv1.UserSettings) (*imv1.UserSettings, error) {
	st, err := s.store.SetSettings(ctx, req)
	if err != nil {
		return nil, mapErr(err)
	}
	return st, nil
}
