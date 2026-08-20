package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/davveo/surge/pkg/conv"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

func (s *memoryStore) DeleteMessage(_ context.Context, uid, _, msgID string) error {
	uid = strings.TrimSpace(uid)
	msgID = strings.TrimSpace(msgID)
	if uid == "" || msgID == "" {
		return fmt.Errorf("%w: uid and msg_id required", errInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deletedMsgs[uid] == nil {
		s.deletedMsgs[uid] = map[string]struct{}{}
	}
	s.deletedMsgs[uid][msgID] = struct{}{}
	return nil
}

func (s *memoryStore) ClearConversation(_ context.Context, uid, cid string) error {
	uid = strings.TrimSpace(uid)
	cid = strings.TrimSpace(cid)
	if uid == "" || cid == "" {
		return fmt.Errorf("%w: uid and cid required", errInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var max uint64
	for _, row := range s.byCID[cid] {
		if row.convSeq > max {
			max = row.convSeq
		}
	}
	if c := s.convs[uid][cid]; c != nil && c.LastConvSeq > max {
		max = c.LastConvSeq
	}
	if s.cleared[uid] == nil {
		s.cleared[uid] = map[string]uint64{}
	}
	s.cleared[uid][cid] = max
	if c := s.convs[uid][cid]; c != nil {
		c.Unread = 0
		c.LastText = ""
	}
	return nil
}

func (s *memoryStore) SetMember(_ context.Context, operatorUID, cid, memberUID, nickname, role string, muted bool, setNick, setRole, setMuted bool) (*groupInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.groups[cid]
	if g == nil {
		return nil, fmt.Errorf("%w: group not found", errInvalid)
	}
	m := memberOf(g, memberUID)
	if m == nil {
		return nil, errNotMember
	}
	if setNick {
		if operatorUID != memberUID && !isManager(g, operatorUID) {
			return nil, errNotAdmin
		}
		m.Nickname = clipText(nickname, 64)
	}
	if setRole {
		if g.OwnerUID != operatorUID {
			return nil, errNotOwner
		}
		if memberUID == operatorUID {
			return nil, fmt.Errorf("%w: cannot change owner role", errInvalid)
		}
		switch role {
		case "admin", "member":
			m.Role = role
		default:
			return nil, fmt.Errorf("%w: invalid role", errInvalid)
		}
	}
	if setMuted {
		if !isManager(g, operatorUID) {
			return nil, errNotAdmin
		}
		if m.Role == "owner" {
			return nil, errNotOwner
		}
		m.Muted = muted
	}
	cp := *g
	cp.Members = append([]groupMember{}, g.Members...)
	return &cp, nil
}

func (s *memoryStore) ListJoinRequests(_ context.Context, uid, cid string) ([]joinReq, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.groups[cid]
	if g == nil {
		return nil, fmt.Errorf("%w: group not found", errInvalid)
	}
	if !isManager(g, uid) {
		return nil, errNotAdmin
	}
	var out []joinReq
	for _, r := range s.joins[cid] {
		out = append(out, r)
	}
	return out, nil
}

func (s *memoryStore) RequestJoin(_ context.Context, uid, cid string) (*groupInfo, error) {
	uid = strings.TrimSpace(uid)
	cid = strings.TrimSpace(cid)
	if uid == "" || cid == "" {
		return nil, fmt.Errorf("%w: uid and cid required", errInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.groups[cid]
	if g == nil {
		return nil, fmt.Errorf("%w: group not found", errInvalid)
	}
	if s.isMemberLocked(g, uid) {
		cp := *g
		cp.Members = append([]groupMember{}, g.Members...)
		return &cp, nil
	}
	if s.joins[cid] == nil {
		s.joins[cid] = map[string]joinReq{}
	}
	s.joins[cid][uid] = joinReq{UID: uid, FromUID: uid, CreatedAtMs: time.Now().UnixMilli()}
	cp := *g
	cp.Members = append([]groupMember{}, g.Members...)
	return &cp, nil
}

func (s *memoryStore) DecideJoin(_ context.Context, operatorUID, cid, memberUID string, accept bool) (*groupInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.groups[cid]
	if g == nil {
		return nil, fmt.Errorf("%w: group not found", errInvalid)
	}
	if !isManager(g, operatorUID) {
		return nil, errNotAdmin
	}
	if s.joins[cid] != nil {
		delete(s.joins[cid], memberUID)
	}
	if !accept {
		cp := *g
		cp.Members = append([]groupMember{}, g.Members...)
		return &cp, nil
	}
	if s.isMemberLocked(g, memberUID) {
		cp := *g
		cp.Members = append([]groupMember{}, g.Members...)
		return &cp, nil
	}
	if len(g.Members)+1 > maxGroupMembers {
		return nil, errTooLarge
	}
	now := time.Now().UnixMilli()
	g.Members = append(g.Members, groupMember{UID: memberUID, Role: "member"})
	row := &timelineRow{msgID: "", convSeq: 0, createdAt: now, payload: &imv1.Payload{Text: "加入群聊"}}
	s.upsertConv(memberUID, g.CID, "", g.Name, conv.KindGroup, row, "加入群聊", true)
	cp := *g
	cp.Members = append([]groupMember{}, g.Members...)
	return &cp, nil
}
