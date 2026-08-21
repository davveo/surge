package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/davveo/surge/pkg/conv"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

type groupInviteTok struct {
	Token   string
	CID     string
	FromUID string
	Exp     int64
}

type reportRow struct {
	UID    string
	CID    string
	MsgID  string
	Reason string
	At     int64
}

func inviteToken() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func reactionBuckets(byUID map[string]string) []*imv1.ReactionBucket {
	order := []string{}
	seen := map[string]*imv1.ReactionBucket{}
	for uid, emoji := range byUID {
		if emoji == "" {
			continue
		}
		b := seen[emoji]
		if b == nil {
			b = &imv1.ReactionBucket{Emoji: emoji}
			seen[emoji] = b
			order = append(order, emoji)
		}
		b.Uids = append(b.Uids, uid)
	}
	out := make([]*imv1.ReactionBucket, 0, len(order))
	for _, e := range order {
		out = append(out, seen[e])
	}
	return out
}

func (s *memoryStore) attachReactionsLocked(msgs []*imv1.TimelineMessage) {
	for _, m := range msgs {
		if m == nil {
			continue
		}
		m.Reactions = reactionBuckets(s.reactions[m.MsgId])
	}
}

func (s *memoryStore) LookupMessage(_ context.Context, uid, cid, msgID string) (*timelineRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok, err := s.canReadLocked(uid, cid); !ok {
		return nil, err
	}
	row := s.byID[msgID]
	if row == nil {
		return nil, fmt.Errorf("%w: message not found", errInvalid)
	}
	return row, nil
}

func (s *memoryStore) ReactMessage(_ context.Context, uid, cid, msgID, emoji string) ([]*imv1.ReactionBucket, error) {
	uid = strings.TrimSpace(uid)
	msgID = strings.TrimSpace(msgID)
	emoji = clipText(strings.TrimSpace(emoji), 8)
	if uid == "" || msgID == "" {
		return nil, fmt.Errorf("%w: uid and msg_id required", errInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok, err := s.canReadLocked(uid, cid); !ok {
		return nil, err
	}
	if s.byID[msgID] == nil {
		return nil, fmt.Errorf("%w: message not found", errInvalid)
	}
	if s.reactions[msgID] == nil {
		s.reactions[msgID] = map[string]string{}
	}
	if emoji == "" || s.reactions[msgID][uid] == emoji {
		delete(s.reactions[msgID], uid)
	} else {
		s.reactions[msgID][uid] = emoji
	}
	return reactionBuckets(s.reactions[msgID]), nil
}

func (s *memoryStore) ReactionsOf(_ context.Context, msgIDs []string) (map[string][]*imv1.ReactionBucket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string][]*imv1.ReactionBucket{}
	for _, id := range msgIDs {
		if b := reactionBuckets(s.reactions[id]); len(b) > 0 {
			out[id] = b
		}
	}
	return out, nil
}

func (s *memoryStore) AddFavorite(_ context.Context, uid, cid, msgID string) (*imv1.Favorite, error) {
	uid, cid, msgID = strings.TrimSpace(uid), strings.TrimSpace(cid), strings.TrimSpace(msgID)
	if uid == "" || msgID == "" {
		return nil, fmt.Errorf("%w: msg_id required", errInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok, err := s.canReadLocked(uid, cid); !ok {
		return nil, err
	}
	row := s.byID[msgID]
	if row == nil {
		return nil, fmt.Errorf("%w: message not found", errInvalid)
	}
	for _, f := range s.favorites[uid] {
		if f.MsgId == msgID {
			return f, nil
		}
	}
	fav := &imv1.Favorite{
		FavId:       uuid.NewString(),
		Cid:         cid,
		MsgId:       msgID,
		FromUid:     row.fromUID,
		Preview:     previewOf(row.payload),
		CreatedAtMs: time.Now().UnixMilli(),
		Payload:     row.payload,
	}
	s.favorites[uid] = append([]*imv1.Favorite{fav}, s.favorites[uid]...)
	return fav, nil
}

func (s *memoryStore) ListFavorites(_ context.Context, uid, query string) ([]*imv1.Favorite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	query = strings.ToLower(strings.TrimSpace(query))
	var out []*imv1.Favorite
	for _, f := range s.favorites[uid] {
		if query != "" && !strings.Contains(strings.ToLower(f.Preview), query) && !strings.Contains(strings.ToLower(f.FromUid), query) {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

func (s *memoryStore) DeleteFavorite(_ context.Context, uid, favID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.favorites[uid]
	n := list[:0]
	for _, f := range list {
		if f.FavId != favID && f.MsgId != favID {
			n = append(n, f)
		}
	}
	s.favorites[uid] = n
	return nil
}

func (s *memoryStore) CreateGroupInvite(_ context.Context, uid, cid string) (*imv1.GroupInvite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.groups[cid]
	if g == nil || memberOf(g, uid) == nil {
		return nil, errNotMember
	}
	tok := inviteToken()
	s.invites[tok] = groupInviteTok{Token: tok, CID: cid, FromUID: uid, Exp: time.Now().Add(7 * 24 * time.Hour).UnixMilli()}
	return &imv1.GroupInvite{Token: tok, Cid: cid}, nil
}

func (s *memoryStore) JoinByInvite(ctx context.Context, uid, token string) (*groupInfo, error) {
	s.mu.Lock()
	inv, ok := s.invites[strings.TrimSpace(token)]
	if !ok || time.Now().UnixMilli() > inv.Exp {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: invite expired", errInvalid)
	}
	g := s.groups[inv.CID]
	if g == nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: group not found", errInvalid)
	}
	if memberOf(g, uid) != nil {
		out := g
		s.mu.Unlock()
		return out, nil
	}
	needApproval := g.JoinApproval
	cid := inv.CID
	s.mu.Unlock()
	if needApproval {
		return s.RequestJoin(ctx, uid, cid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g = s.groups[cid]
	if g == nil {
		return nil, fmt.Errorf("%w: group not found", errInvalid)
	}
	if memberOf(g, uid) != nil {
		return g, nil
	}
	if len(g.Members)+1 > maxGroupMembers {
		return nil, errTooLarge
	}
	now := time.Now().UnixMilli()
	g.Members = append(g.Members, groupMember{UID: uid, Role: "member"})
	row := &timelineRow{msgID: "", convSeq: 0, createdAt: now, payload: &imv1.Payload{Text: "加入群聊"}}
	s.upsertConv(uid, g.CID, "", g.Name, conv.KindGroup, row, "加入群聊", true)
	return g, nil
}

func (s *memoryStore) SetDraft(_ context.Context, uid, cid, text string) error {
	uid, cid = strings.TrimSpace(uid), strings.TrimSpace(cid)
	if uid == "" || cid == "" {
		return fmt.Errorf("%w: cid required", errInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.drafts[uid] == nil {
		s.drafts[uid] = map[string]string{}
	}
	text = clipText(text, 4000)
	if text == "" {
		delete(s.drafts[uid], cid)
	} else {
		s.drafts[uid][cid] = text
	}
	if c := s.convs[uid][cid]; c != nil {
		c.DraftText = text
	}
	return nil
}

func (s *memoryStore) PinChatMessage(_ context.Context, uid, cid, msgID string) (*imv1.PinnedMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok, err := s.canReadLocked(uid, cid); !ok {
		return nil, err
	}
	msgID = strings.TrimSpace(msgID)
	if msgID == "" {
		delete(s.msgPins, cid)
		return &imv1.PinnedMessage{Cid: cid}, nil
	}
	row := s.byID[msgID]
	if row == nil {
		return nil, fmt.Errorf("%w: message not found", errInvalid)
	}
	pin := &imv1.PinnedMessage{Cid: cid, MsgId: msgID, FromUid: row.fromUID, Text: previewOf(row.payload)}
	s.msgPins[cid] = pin
	return pin, nil
}

func (s *memoryStore) GetPinnedMessage(_ context.Context, uid, cid string) (*imv1.PinnedMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok, err := s.canReadLocked(uid, cid); !ok {
		return nil, err
	}
	if p := s.msgPins[cid]; p != nil {
		return p, nil
	}
	return &imv1.PinnedMessage{Cid: cid}, nil
}

func (s *memoryStore) ReportMessage(_ context.Context, uid, cid, msgID, reason string) error {
	uid, cid, msgID = strings.TrimSpace(uid), strings.TrimSpace(cid), strings.TrimSpace(msgID)
	if uid == "" || msgID == "" {
		return fmt.Errorf("%w: msg_id required", errInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports = append(s.reports, reportRow{UID: uid, CID: cid, MsgID: msgID, Reason: clipText(reason, 256), At: time.Now().UnixMilli()})
	return nil
}

func defaultSettings(uid string) *imv1.UserSettings {
	return &imv1.UserSettings{Uid: uid, NotifySound: true, NotifyPreview: true}
}

func (s *memoryStore) GetSettings(_ context.Context, uid string) (*imv1.UserSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st := s.settings[uid]; st != nil {
		cp := *st
		return &cp, nil
	}
	return defaultSettings(uid), nil
}

func (s *memoryStore) SetSettings(_ context.Context, st *imv1.UserSettings) (*imv1.UserSettings, error) {
	if st == nil || strings.TrimSpace(st.Uid) == "" {
		return nil, fmt.Errorf("%w: uid required", errInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *st
	cp.Wallpaper = clipText(cp.Wallpaper, 64)
	cp.DndStart = clipText(cp.DndStart, 8)
	cp.DndEnd = clipText(cp.DndEnd, 8)
	s.settings[st.Uid] = &cp
	return &cp, nil
}
