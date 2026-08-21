package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

func init() {
	if raw := strings.TrimSpace(os.Getenv("SENSITIVE_WORDS")); raw != "" {
		sensitiveWords = strings.Split(raw, ",")
	}
}

func defaultSettings(uid string) *imv1.UserSettings {
	return &imv1.UserSettings{
		Uid: uid, NotifySound: true, NotifyPreview: true,
		NotifyAtMuted: true, AddMe: "verify", BurnSec: 5,
	}
}

func fillSettingsDefaults(st *imv1.UserSettings) *imv1.UserSettings {
	if st == nil {
		return defaultSettings("")
	}
	if st.AddMe == "" {
		st.AddMe = "verify"
	}
	if st.BurnSec <= 0 {
		st.BurnSec = 5
	}
	return st
}

func (s *server) ResetPassword(ctx context.Context, req *imv1.LoginRequest) (*imv1.UserProfile, error) {
	uid, err := s.store.ResolveLogin(ctx, req.GetUid())
	if err != nil {
		return nil, mapErr(err)
	}
	if err := s.store.ResetPassword(ctx, uid, req.GetPassword()); err != nil {
		return nil, mapErr(err)
	}
	p, err := s.store.GetProfile(ctx, uid)
	if err != nil {
		return nil, mapErr(err)
	}
	return p, nil
}

func (s *server) DeleteAccount(ctx context.Context, req *imv1.GetProfileRequest) (*imv1.HideConversationResponse, error) {
	if err := s.store.DeleteAccount(ctx, req.GetUid()); err != nil {
		return nil, mapErr(err)
	}
	return &imv1.HideConversationResponse{}, nil
}

func (s *server) RevokeGroupInvite(ctx context.Context, req *imv1.JoinInviteRequest) (*imv1.HideConversationResponse, error) {
	if err := s.store.RevokeGroupInvite(ctx, req.GetUid(), req.GetToken()); err != nil {
		return nil, mapErr(err)
	}
	return &imv1.HideConversationResponse{}, nil
}

func (s *memoryStore) ResetPassword(_ context.Context, uid, newPassword string) error {
	newPassword = strings.TrimSpace(newPassword)
	if len(newPassword) < 6 {
		return fmt.Errorf("%w: password too short", errInvalid)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[uid]
	if u == nil {
		return fmt.Errorf("%w: user not found", errInvalid)
	}
	u.PasswordHash = string(hash)
	return nil
}

func (s *memoryStore) DeleteAccount(_ context.Context, uid string) error {
	if err := validUID(uid); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.users, uid)
	delete(s.friends, uid)
	for peer, set := range s.friends {
		delete(set, uid)
		_ = peer
	}
	delete(s.convs, uid)
	delete(s.settings, uid)
	return nil
}

func (s *memoryStore) RevokeGroupInvite(_ context.Context, uid, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, ok := s.invites[strings.TrimSpace(token)]
	if !ok {
		return fmt.Errorf("%w: invite not found", errInvalid)
	}
	g := s.groups[inv.CID]
	if g == nil || !isManager(g, uid) {
		return errNotAdmin
	}
	delete(s.invites, inv.Token)
	return nil
}

func (s *memoryStore) DeleteMemberMessages(_ context.Context, cid, memberUID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := s.byCID[cid]
	for _, row := range rows {
		if row != nil && row.fromUID == memberUID && row.msgID != "" {
			if s.deletedMsgs[memberUID] == nil {
				s.deletedMsgs[memberUID] = map[string]struct{}{}
			}
			s.deletedMsgs[memberUID][row.msgID] = struct{}{}
		}
	}
	return nil
}

func (s *memoryStore) PatchGroup(_ context.Context, operatorUID, cid, mode string, historyDays int32, announceAck bool, setMode, setHistory, setAck bool) (*groupInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.groups[cid]
	if g == nil {
		return nil, fmt.Errorf("%w: group not found", errInvalid)
	}
	if !isOwner(g, operatorUID) {
		return nil, errNotOwner
	}
	if setMode {
		applyGroupMode(g, mode)
	}
	if setHistory {
		if historyDays < 0 {
			historyDays = 0
		}
		g.HistoryDays = historyDays
	}
	if setAck {
		g.AnnounceAck = announceAck
	}
	cp := *g
	cp.Members = append([]groupMember{}, g.Members...)
	return &cp, nil
}

func memberMuted(m *groupMember) bool {
	if m == nil {
		return false
	}
	if m.MutedUntilMs > 0 {
		return time.Now().UnixMilli() < m.MutedUntilMs
	}
	return m.Muted
}

func filterTimelineHistory(g *groupInfo, uid string, msgs []*imv1.TimelineMessage) []*imv1.TimelineMessage {
	if g == nil || g.HistoryDays <= 0 || len(msgs) == 0 {
		return msgs
	}
	m := memberOf(g, uid)
	if m == nil || m.Role == "owner" {
		return msgs
	}
	joined := m.JoinedAtMs
	if joined == 0 {
		return msgs
	}
	cutoff := joined - int64(g.HistoryDays)*86400000
	out := msgs[:0]
	for _, msg := range msgs {
		if msg != nil && msg.GetCreatedAtMs() >= cutoff {
			out = append(out, msg)
		}
	}
	return out
}
