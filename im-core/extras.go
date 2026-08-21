package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/davveo/surge/pkg/conv"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

func (s *memoryStore) GetProfiles(_ context.Context, uids []string) ([]*imv1.UserProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*imv1.UserProfile
	for _, uid := range uniqueUIDs(uids) {
		if u := s.users[uid]; u != nil {
			out = append(out, profileOf(u))
			continue
		}
		out = append(out, &imv1.UserProfile{Uid: uid, DisplayName: uid})
	}
	return out, nil
}

func (s *memoryStore) RemoveFriend(_ context.Context, uid, peerUID string) error {
	uid, peerUID, err := normalizePair(uid, peerUID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.friends[uid], peerUID)
	delete(s.friends[peerUID], uid)
	return nil
}

type friendReqRow struct {
	FromUID string
	ToUID   string
	Hello   string
	Source  string
}

func (s *memoryStore) RequestFriend(_ context.Context, fromUID, toUID, hello, source string) (string, error) {
	fromUID, toUID, err := normalizePair(fromUID, toUID)
	if err != nil {
		return "", err
	}
	hello = clipText(hello, 200)
	source = clipText(source, 64)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.blockedLocked(toUID, fromUID) || s.blockedLocked(fromUID, toUID) {
		return "", errBlocked
	}
	if s.hasFriend(fromUID, toUID) {
		return "friends", nil
	}
	if _, ok := s.requests[fromUID][toUID]; ok {
		s.putFriend(fromUID, toUID)
		s.putFriend(toUID, fromUID)
		delete(s.requests[fromUID], toUID)
		return "friends", nil
	}
	if s.requests[toUID] == nil {
		s.requests[toUID] = map[string]friendReqRow{}
	}
	s.requests[toUID][fromUID] = friendReqRow{FromUID: fromUID, ToUID: toUID, Hello: hello, Source: source}
	return "pending", nil
}

func (s *memoryStore) AcceptFriend(_ context.Context, fromUID, toUID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.requests[toUID][fromUID]; !ok {
		return fmt.Errorf("%w: no request", errInvalid)
	}
	delete(s.requests[toUID], fromUID)
	s.putFriend(fromUID, toUID)
	s.putFriend(toUID, fromUID)
	return nil
}

func (s *memoryStore) DeclineFriend(_ context.Context, fromUID, toUID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.requests[toUID], fromUID)
	return nil
}

func (s *memoryStore) ListFriendRequests(_ context.Context, uid string) (incoming, outgoing []friendReqRow, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.requests[uid] {
		incoming = append(incoming, row)
	}
	for _, set := range s.requests {
		if row, ok := set[uid]; ok {
			outgoing = append(outgoing, row)
		}
	}
	return incoming, outgoing, nil
}

func (s *memoryStore) blockedLocked(uid, peer string) bool {
	_, ok := s.blocks[uid][peer]
	return ok
}

func (s *memoryStore) BlockUser(_ context.Context, uid, peerUID string) error {
	uid, peerUID, err := normalizePair(uid, peerUID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.blocks[uid] == nil {
		s.blocks[uid] = map[string]struct{}{}
	}
	s.blocks[uid][peerUID] = struct{}{}
	delete(s.friends[uid], peerUID)
	delete(s.friends[peerUID], uid)
	return nil
}

func (s *memoryStore) UnblockUser(_ context.Context, uid, peerUID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.blocks[uid], peerUID)
	return nil
}

func (s *memoryStore) ListBlocks(_ context.Context, uid string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for p := range s.blocks[uid] {
		out = append(out, p)
	}
	return out, nil
}

func (s *memoryStore) IsBlocked(_ context.Context, uid, peerUID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.blockedLocked(uid, peerUID) || s.blockedLocked(peerUID, uid), nil
}

func (s *memoryStore) SetRemark(_ context.Context, uid, peerUID, remark string) error {
	uid = strings.TrimSpace(uid)
	peerUID = strings.TrimSpace(peerUID)
	if uid == "" || peerUID == "" {
		return fmt.Errorf("%w: uid required", errInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remarks[uid] == nil {
		s.remarks[uid] = map[string]string{}
	}
	if remark == "" {
		delete(s.remarks[uid], peerUID)
	} else {
		s.remarks[uid][peerUID] = clipText(remark, 64)
	}
	return nil
}

func (s *memoryStore) GetRemark(_ context.Context, uid, peerUID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.remarks[uid][peerUID], nil
}

func (s *memoryStore) LeaveGroup(_ context.Context, uid, cid string) (*groupInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.groups[cid]
	if g == nil {
		return nil, fmt.Errorf("%w: group not found", errInvalid)
	}
	if g.OwnerUID == uid {
		return nil, fmt.Errorf("%w: transfer owner first", errInvalid)
	}
	if !s.isMemberLocked(g, uid) {
		return nil, errNotMember
	}
	kept := g.Members[:0]
	for _, m := range g.Members {
		if m.UID != uid {
			kept = append(kept, m)
		}
	}
	g.Members = kept
	if s.convs[uid] != nil {
		if c := s.convs[uid][cid]; c != nil {
			c.LastText = "你已退出群聊"
		}
	}
	cp := *g
	cp.Members = append([]groupMember{}, g.Members...)
	return &cp, nil
}

func (s *memoryStore) DissolveGroup(_ context.Context, uid, cid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.groups[cid]
	if g == nil {
		return fmt.Errorf("%w: group not found", errInvalid)
	}
	if g.OwnerUID != uid {
		return errNotOwner
	}
	for _, m := range g.Members {
		if s.convs[m.UID] != nil {
			if c := s.convs[m.UID][cid]; c != nil {
				c.LastText = "群聊已解散"
			}
		}
	}
	delete(s.groups, cid)
	return nil
}

func (s *memoryStore) TransferOwner(_ context.Context, operatorUID, cid, memberUID string) (*groupInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.groups[cid]
	if g == nil {
		return nil, fmt.Errorf("%w: group not found", errInvalid)
	}
	if g.OwnerUID != operatorUID {
		return nil, errNotOwner
	}
	if !s.isMemberLocked(g, memberUID) {
		return nil, errNotMember
	}
	g.OwnerUID = memberUID
	for i, m := range g.Members {
		if m.UID == operatorUID {
			g.Members[i].Role = "member"
		}
		if m.UID == memberUID {
			g.Members[i].Role = "owner"
		}
	}
	cp := *g
	cp.Members = append([]groupMember{}, g.Members...)
	return &cp, nil
}

func (s *memoryStore) HideConversation(_ context.Context, uid, cid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hidden[uid] == nil {
		s.hidden[uid] = map[string]bool{}
	}
	s.hidden[uid][cid] = true
	return nil
}

func (s *memoryStore) UnhideConversation(_ context.Context, uid, cid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hidden[uid] != nil {
		delete(s.hidden[uid], cid)
	}
	return nil
}

func (s *memoryStore) SetPin(_ context.Context, uid, cid string, pinned bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pins[uid] == nil {
		s.pins[uid] = map[string]bool{}
	}
	if pinned {
		s.pins[uid][cid] = true
	} else {
		delete(s.pins[uid], cid)
	}
	return nil
}

func (s *memoryStore) TimelineQuery(_ context.Context, uid, cid string, afterSeq, beforeSeq uint64, limit int, query string) (string, []*imv1.TimelineMessage, bool, error) {
	if limit <= 0 {
		limit = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if convNeed, err := s.canReadLocked(uid, cid); !convNeed {
		return "", nil, false, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	rows := s.byCID[cid]
	var matched []*timelineRow
	for _, row := range rows {
		if afterSeq > 0 && row.convSeq <= afterSeq {
			continue
		}
		if beforeSeq > 0 && row.convSeq >= beforeSeq {
			continue
		}
		if cut := s.cleared[uid][cid]; cut > 0 && row.convSeq <= cut {
			continue
		}
		if _, gone := s.deletedMsgs[uid][row.msgID]; gone {
			continue
		}
		if query != "" {
			text := ""
			if row.payload != nil {
				text = strings.ToLower(row.payload.GetText())
			}
			if !strings.Contains(text, query) {
				continue
			}
		}
		matched = append(matched, row)
	}
	hasMore := false
	if afterSeq > 0 && beforeSeq == 0 {
		if len(matched) > limit {
			hasMore = true
			matched = matched[:limit]
		}
	} else if len(matched) > limit {
		hasMore = true
		matched = matched[len(matched)-limit:]
	}
	out := make([]*imv1.TimelineMessage, 0, len(matched))
	for _, row := range matched {
		p := row.payload
		if row.recalled {
			text := ""
			if row.payload != nil {
				text = row.payload.Text
			}
			p = &imv1.Payload{Type: imv1.Payload_RECALL, Text: text}
		}
		out = append(out, &imv1.TimelineMessage{
			MsgId: row.msgID, ConvSeq: row.convSeq, FromUid: row.fromUID,
			Payload: p, CreatedAtMs: row.createdAt, Recalled: row.recalled, QuoteMsgId: row.quoteMsgID,
		})
	}
	if c := s.convs[uid][cid]; c != nil && query == "" && beforeSeq == 0 {
		c.Unread = 0
		c.UnreadMention = 0
	}
	s.attachReactionsLocked(out)
	return cid, out, hasMore, nil
}

func (s *memoryStore) canReadLocked(uid, cid string) (bool, error) {
	if conv.IsGroup(cid) {
		g := s.groups[cid]
		ok := false
		if g != nil {
			for _, m := range g.Members {
				if m.UID == uid {
					ok = true
					break
				}
			}
		}
		if !ok && s.convs[uid][cid] == nil {
			return false, errNotMember
		}
		return true, nil
	}
	if _, err := conv.PeerUID(cid, uid); err != nil {
		return false, fmt.Errorf("%w: %v", errInvalid, err)
	}
	return true, nil
}

func (s *memoryStore) GetReadState(_ context.Context, uid, cid string, convSeq uint64) (int, int, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var members []string
	if conv.IsGroup(cid) {
		g := s.groups[cid]
		if g == nil {
			return 0, 0, nil, fmt.Errorf("%w: group not found", errInvalid)
		}
		for _, m := range g.Members {
			members = append(members, m.UID)
		}
	} else {
		peer, err := conv.PeerUID(cid, uid)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("%w: %v", errInvalid, err)
		}
		members = []string{uid, peer}
	}
	var readers []string
	for _, m := range members {
		if m == uid {
			continue
		}
		if s.reads[m][cid] >= convSeq && convSeq > 0 {
			readers = append(readers, m)
		}
	}
	return len(readers), len(members), readers, nil
}
