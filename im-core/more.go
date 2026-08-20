package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/davveo/surge/pkg/conv"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

func (s *memoryStore) ListReadCursors(_ context.Context, uid, cid string) (map[string]uint64, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.canReadLocked(uid, cid); err != nil {
		return nil, 0, err
	}
	members := 2
	if conv.IsGroup(cid) {
		if g := s.groups[cid]; g != nil {
			members = len(g.Members)
		}
	}
	out := map[string]uint64{}
	for u, byCID := range s.reads {
		if seq := byCID[cid]; seq > 0 {
			out[u] = seq
		}
	}
	return out, members, nil
}

func (s *memoryStore) ResolveLogin(_ context.Context, identifier string) (string, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return "", fmt.Errorf("%w: login required", errInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if u := s.users[identifier]; u != nil {
		return u.UID, nil
	}
	low := strings.ToLower(identifier)
	for _, u := range s.users {
		if u.Email != "" && strings.ToLower(u.Email) == low {
			return u.UID, nil
		}
		if u.Phone != "" && u.Phone == identifier {
			return u.UID, nil
		}
	}
	return "", fmt.Errorf("%w: user not found", errAuth)
}

func (s *memoryStore) SetContacts(_ context.Context, uid, email, phone string) error {
	if err := validUID(uid); err != nil {
		return err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	phone = strings.TrimSpace(phone)
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[uid]
	if u == nil {
		u = &userRec{UID: uid, DisplayName: uid}
		s.users[uid] = u
	}
	if email != "" {
		u.Email = clipText(email, 128)
	}
	if phone != "" {
		u.Phone = clipText(phone, 32)
	}
	return nil
}

func (s *memoryStore) SetPublicKey(_ context.Context, uid, publicKey string) error {
	if err := validUID(uid); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[uid]
	if u == nil {
		u = &userRec{UID: uid, DisplayName: uid}
		s.users[uid] = u
	}
	u.PublicKey = strings.TrimSpace(publicKey)
	return nil
}

func (s *memoryStore) SearchMessages(_ context.Context, uid, query string, limit int) ([]*imv1.SearchHit, error) {
	query = strings.TrimSpace(query)
	if uid == "" || query == "" {
		return nil, fmt.Errorf("%w: uid and query required", errInvalid)
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	q := strings.ToLower(query)
	var hits []*imv1.SearchHit
	for cid, convRow := range s.convs[uid] {
		rows := s.byCID[cid]
		title := convRow.Title
		for i := len(rows) - 1; i >= 0 && len(hits) < limit; i-- {
			row := rows[i]
			if row.recalled || row.payload == nil || row.payload.E2Ee {
				continue
			}
			if !strings.Contains(strings.ToLower(row.payload.GetText()), q) {
				continue
			}
			hits = append(hits, &imv1.SearchHit{
				Cid:   cid,
				Title: title,
				Message: &imv1.TimelineMessage{
					MsgId: row.msgID, ConvSeq: row.convSeq, FromUid: row.fromUID,
					Payload: row.payload, CreatedAtMs: row.createdAt,
				},
			})
		}
		if len(hits) >= limit {
			break
		}
	}
	return hits, nil
}

func (s *memoryStore) SetGroupMuteAll(_ context.Context, operatorUID, cid string, muted bool) (*groupInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.groups[cid]
	if g == nil {
		return nil, fmt.Errorf("%w: group not found", errInvalid)
	}
	if !isManager(g, operatorUID) {
		return nil, errNotAdmin
	}
	g.MutedAll = muted
	cp := *g
	cp.Members = append([]groupMember{}, g.Members...)
	return &cp, nil
}

func (s *memoryStore) SetFriendTags(_ context.Context, uid, peerUID string, tags []string) error {
	uid, peerUID, err := normalizePair(uid, peerUID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tags[uid] == nil {
		s.tags[uid] = map[string][]string{}
	}
	s.tags[uid][peerUID] = uniqueTags(tags)
	return nil
}

func (s *memoryStore) ListFriendTags(_ context.Context, uid string) ([]*imv1.TagGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	by := map[string][]string{}
	for peer, tags := range s.tags[uid] {
		for _, t := range tags {
			by[t] = append(by[t], peer)
		}
	}
	var out []*imv1.TagGroup
	for name, uids := range by {
		out = append(out, &imv1.TagGroup{Name: name, Uids: uids})
	}
	return out, nil
}

func (s *memoryStore) FriendTagsOf(_ context.Context, uid, peerUID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.tags[uid][peerUID]...), nil
}

func (s *memoryStore) ConsumeEphemeral(_ context.Context, uid, cid, msgID string) (*imv1.RecallNotify, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.byID[msgID]
	if row == nil {
		return nil, nil, fmt.Errorf("%w: message not found", errInvalid)
	}
	foundCID := ""
	for c, rows := range s.byCID {
		for _, r := range rows {
			if r.msgID == msgID {
				foundCID = c
			}
		}
	}
	if cid != "" && foundCID != cid {
		return nil, nil, fmt.Errorf("%w: message not found", errInvalid)
	}
	cid = foundCID
	if _, err := s.canReadLocked(uid, cid); err != nil {
		return nil, nil, err
	}
	if row.payload == nil || !row.payload.Ephemeral {
		return nil, nil, fmt.Errorf("%w: not ephemeral", errInvalid)
	}
	if row.fromUID == uid {
		return nil, nil, fmt.Errorf("%w: sender cannot burn", errInvalid)
	}
	row.recalled = true
	row.payload = &imv1.Payload{Type: imv1.Payload_RECALL, Text: "已销毁"}
	var members []string
	if conv.IsGroup(cid) {
		if g := s.groups[cid]; g != nil {
			for _, m := range g.Members {
				members = append(members, m.UID)
			}
		}
	} else if peer, err := conv.PeerUID(cid, uid); err == nil {
		members = []string{uid, peer}
	}
	return &imv1.RecallNotify{Cid: cid, MsgId: msgID, FromUid: uid}, members, nil
}

func (s *memoryStore) AddSticker(_ context.Context, uid, url, pack string) (*imv1.Sticker, error) {
	url = strings.TrimSpace(url)
	if uid == "" || url == "" {
		return nil, fmt.Errorf("%w: url required", errInvalid)
	}
	st := &imv1.Sticker{Id: uuid.NewString(), Url: url, Pack: strings.TrimSpace(pack)}
	if st.Pack == "" {
		st.Pack = "mine"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stickers[uid] = append(s.stickers[uid], st)
	return st, nil
}

func (s *memoryStore) ListStickers(_ context.Context, uid string) ([]*imv1.Sticker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*imv1.Sticker{}, s.stickers[uid]...), nil
}

func uniqueTags(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" || len(t) > 32 {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}
