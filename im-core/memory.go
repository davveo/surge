package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/davveo/surge/pkg/conv"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

var errInvalid = errors.New("invalid argument")
var errNotFriends = errors.New("not friends")
var errNotMember = errors.New("not a group member")
var errNotOwner = errors.New("not group owner")
var errTooLarge = errors.New("group too large")
var errAuth = errors.New("unauthorized")
var errBlocked = errors.New("blocked")
var errMutedAll = errors.New("group muted")

const maxGroupMembers = 200
const recallWindowMS int64 = 2 * 60 * 1000

type delivery struct {
	uid  string
	push *imv1.Push
}

type sendResult struct {
	ack        *imv1.Ack
	peerUID    string
	peerPush   *imv1.Push
	deliveries []delivery
}

type timelineRow struct {
	msgID      string
	convSeq    uint64
	fromUID    string
	payload    *imv1.Payload
	createdAt  int64
	clientMsg  string
	senderSync uint64
	recalled   bool
	quoteMsgID string
}

type groupInfo struct {
	CID          string
	Name         string
	OwnerUID     string
	AvatarURL    string
	MutedAll     bool
	Announcement string
	JoinApproval bool
	Mode         string
	Members      []groupMember
}

type groupMember struct {
	UID      string
	Role     string
	Nickname string
	Muted    bool
}

type joinReq struct {
	UID         string
	FromUID     string
	CreatedAtMs int64
}

type Store interface {
	Send(ctx context.Context, fromUID, clientMsgID, cid, peerUID string, payload *imv1.Payload, quoteMsgID string) (*sendResult, error)
	Sync(ctx context.Context, uid string, lastSeq uint64, limit int) (*imv1.SyncResponse, error)
	Watermark(ctx context.Context, uid string) (uint64, error)
	ListConversations(ctx context.Context, uid string) ([]*imv1.Conversation, error)
	Timeline(ctx context.Context, uid, cid string, afterSeq uint64, limit int) (string, []*imv1.TimelineMessage, error)
	AddFriend(ctx context.Context, uid, peerUID string) (already bool, err error)
	ListFriends(ctx context.Context, uid string) ([]string, error)
	AreFriends(ctx context.Context, uid, peerUID string) (bool, error)
	CreateGroup(ctx context.Context, ownerUID, name string, memberUIDs []string, mode string) (*groupInfo, error)
	InviteGroup(ctx context.Context, operatorUID, cid string, memberUIDs []string) (*groupInfo, error)
	KickGroup(ctx context.Context, operatorUID, cid, memberUID string) (*groupInfo, error)
	GetGroup(ctx context.Context, uid, cid string) (*groupInfo, error)
	GroupMembers(ctx context.Context, cid string) ([]string, error)
	Recall(ctx context.Context, uid, cid, msgID string) (*imv1.RecallNotify, []string, error)
	MarkRead(ctx context.Context, uid, cid string, convSeq uint64) error
	EnsureUser(ctx context.Context, uid string) error
	Register(ctx context.Context, uid, password string) (*imv1.UserProfile, error)
	VerifyPassword(ctx context.Context, uid, password string) (*imv1.UserProfile, error)
	GetProfile(ctx context.Context, uid string) (*imv1.UserProfile, error)
	UpdateProfile(ctx context.Context, uid, displayName, avatarURL string) (*imv1.UserProfile, error)
	SearchUsers(ctx context.Context, query string, limit int) ([]*imv1.UserProfile, error)
	UpdateGroup(ctx context.Context, operatorUID, cid, name, avatarURL, announcement string, setAnnouncement bool, joinApproval *bool) (*groupInfo, error)
	SetMute(ctx context.Context, uid, cid string, muted bool) error
	ListMutes(ctx context.Context, uid string) ([]string, error)
	GetProfiles(ctx context.Context, uids []string) ([]*imv1.UserProfile, error)
	RemoveFriend(ctx context.Context, uid, peerUID string) error
	RequestFriend(ctx context.Context, fromUID, toUID string) (string, error)
	AcceptFriend(ctx context.Context, fromUID, toUID string) error
	DeclineFriend(ctx context.Context, fromUID, toUID string) error
	ListFriendRequests(ctx context.Context, uid string) (incoming, outgoing [][2]string, err error)
	BlockUser(ctx context.Context, uid, peerUID string) error
	UnblockUser(ctx context.Context, uid, peerUID string) error
	ListBlocks(ctx context.Context, uid string) ([]string, error)
	IsBlocked(ctx context.Context, uid, peerUID string) (bool, error)
	SetRemark(ctx context.Context, uid, peerUID, remark string) error
	GetRemark(ctx context.Context, uid, peerUID string) (string, error)
	LeaveGroup(ctx context.Context, uid, cid string) (*groupInfo, error)
	DissolveGroup(ctx context.Context, uid, cid string) error
	TransferOwner(ctx context.Context, operatorUID, cid, memberUID string) (*groupInfo, error)
	HideConversation(ctx context.Context, uid, cid string) error
	UnhideConversation(ctx context.Context, uid, cid string) error
	SetPin(ctx context.Context, uid, cid string, pinned bool) error
	TimelineQuery(ctx context.Context, uid, cid string, afterSeq, beforeSeq uint64, limit int, query string) (string, []*imv1.TimelineMessage, bool, error)
	GetReadState(ctx context.Context, uid, cid string, convSeq uint64) (readCount, memberCount int, readers []string, err error)
	ListReadCursors(ctx context.Context, uid, cid string) (map[string]uint64, int, error)
	ResolveLogin(ctx context.Context, identifier string) (string, error)
	SetContacts(ctx context.Context, uid, email, phone string) error
	SetPublicKey(ctx context.Context, uid, publicKey string) error
	SearchMessages(ctx context.Context, uid, query string, limit int) ([]*imv1.SearchHit, error)
	SetGroupMuteAll(ctx context.Context, operatorUID, cid string, muted bool) (*groupInfo, error)
	SetFriendTags(ctx context.Context, uid, peerUID string, tags []string) error
	ListFriendTags(ctx context.Context, uid string) ([]*imv1.TagGroup, error)
	FriendTagsOf(ctx context.Context, uid, peerUID string) ([]string, error)
	ConsumeEphemeral(ctx context.Context, uid, cid, msgID string) (*imv1.RecallNotify, []string, error)
	AddSticker(ctx context.Context, uid, url, pack string) (*imv1.Sticker, error)
	ListStickers(ctx context.Context, uid string) ([]*imv1.Sticker, error)
	DeleteMessage(ctx context.Context, uid, cid, msgID string) error
	ClearConversation(ctx context.Context, uid, cid string) error
	SetMember(ctx context.Context, operatorUID, cid, memberUID, nickname, role string, muted bool, setNick, setRole, setMuted bool) (*groupInfo, error)
	ListJoinRequests(ctx context.Context, uid, cid string) ([]joinReq, error)
	RequestJoin(ctx context.Context, uid, cid string) (*groupInfo, error)
	DecideJoin(ctx context.Context, operatorUID, cid, memberUID string, accept bool) (*groupInfo, error)
}

func clipText(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	i := 0
	for idx := range s {
		if i == max {
			return s[:idx]
		}
		i++
	}
	return s
}

type memoryStore struct {
	mu       sync.Mutex
	seq      Seq
	byID     map[string]*timelineRow
	byDup    map[string]*timelineRow
	byCID    map[string][]*timelineRow
	inbox    map[string][]*imv1.InboxEvent
	convs    map[string]map[string]*imv1.Conversation
	friends  map[string]map[string]struct{}
	groups   map[string]*groupInfo
	reads    map[string]map[string]uint64
	users    map[string]*userRec
	mutes    map[string]map[string]bool
	hidden   map[string]map[string]bool
	pins     map[string]map[string]bool
	blocks   map[string]map[string]struct{}
	requests map[string]map[string]struct{} // toUID -> fromUID
	remarks  map[string]map[string]string
	tags        map[string]map[string][]string // uid -> peer -> tags
	stickers    map[string][]*imv1.Sticker
	deletedMsgs map[string]map[string]struct{} // uid -> msgID
	cleared     map[string]map[string]uint64   // uid -> cid -> seq
	joins       map[string]map[string]joinReq  // cid -> uid
}

func newMemoryStore(seq Seq) *memoryStore {
	if seq == nil {
		seq = newMemSeq()
	}
	return &memoryStore{
		seq:      seq,
		byID:     map[string]*timelineRow{},
		byDup:    map[string]*timelineRow{},
		byCID:    map[string][]*timelineRow{},
		inbox:    map[string][]*imv1.InboxEvent{},
		convs:    map[string]map[string]*imv1.Conversation{},
		friends:  map[string]map[string]struct{}{},
		groups:   map[string]*groupInfo{},
		reads:    map[string]map[string]uint64{},
		users:    map[string]*userRec{},
		mutes:    map[string]map[string]bool{},
		hidden:   map[string]map[string]bool{},
		pins:     map[string]map[string]bool{},
		blocks:   map[string]map[string]struct{}{},
		requests: map[string]map[string]struct{}{},
		remarks:  map[string]map[string]string{},
		tags:        map[string]map[string][]string{},
		stickers:    map[string][]*imv1.Sticker{},
		deletedMsgs: map[string]map[string]struct{}{},
		cleared:     map[string]map[string]uint64{},
		joins:       map[string]map[string]joinReq{},
	}
}

func dupKey(fromUID, clientMsgID string) string { return fromUID + "|" + clientMsgID }

func (s *memoryStore) Send(ctx context.Context, fromUID, clientMsgID, cid, peerUID string, payload *imv1.Payload, quoteMsgID string) (*sendResult, error) {
	if err := validateSend(fromUID, clientMsgID, payload); err != nil {
		return nil, err
	}
	canonical, peer, err := conv.ResolveCID(fromUID, cid, peerUID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalid, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	title, kind, members, err := s.targetsLocked(fromUID, canonical, peer)
	if err != nil {
		return nil, err
	}
	if conv.IsGroup(canonical) {
		applyEphemeralMode(s.groups[canonical], payload)
	}

	if row, ok := s.byDup[dupKey(fromUID, clientMsgID)]; ok {
		return &sendResult{
			ack: &imv1.Ack{
				ClientMsgId: clientMsgID,
				MsgId:       row.msgID,
				Cid:         canonical,
				ConvSeq:     row.convSeq,
				SyncSeq:     row.senderSync,
				CreatedAtMs: row.createdAt,
				Duplicate:   true,
			},
			peerUID: peer,
		}, nil
	}

	convSeq, err := s.seq.Next(ctx, convSeqKey(canonical))
	if err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	msgID := uuid.NewString()
	payload = enrichPayload(payload, s.lookupQuoteTextLocked(canonical, quoteMsgID))
	row := &timelineRow{
		msgID:      msgID,
		convSeq:    convSeq,
		fromUID:    fromUID,
		payload:    payload,
		createdAt:  now,
		clientMsg:  clientMsgID,
		quoteMsgID: quoteMsgID,
	}
	s.byID[msgID] = row
	s.byDup[dupKey(fromUID, clientMsgID)] = row
	s.byCID[canonical] = append(s.byCID[canonical], row)

	preview := previewOf(payload)
	var deliveries []delivery
	var senderSync uint64
	for _, uid := range members {
		syncSeq, err := s.seq.Next(ctx, syncSeqKey(uid))
		if err != nil {
			return nil, err
		}
		if uid == fromUID {
			row.senderSync = syncSeq
			senderSync = syncSeq
		}
		s.appendInbox(uid, syncSeq, canonical, row)
		peerLabel := peer
		if kind == conv.KindGroup {
			peerLabel = ""
		} else if uid == fromUID {
			peerLabel = peer
		} else {
			peerLabel = fromUID
		}
		s.upsertConv(uid, canonical, peerLabel, title, kind, row, preview, uid != fromUID)
		if uid != fromUID {
			push := &imv1.Push{
				Cid:         canonical,
				MsgId:       msgID,
				ConvSeq:     convSeq,
				SyncSeq:     syncSeq,
				FromUid:     fromUID,
				Payload:     payload,
				CreatedAtMs: now,
			}
			deliveries = append(deliveries, delivery{uid: uid, push: push})
		}
	}

	res := &sendResult{
		ack: &imv1.Ack{
			ClientMsgId: clientMsgID,
			MsgId:       msgID,
			Cid:         canonical,
			ConvSeq:     convSeq,
			SyncSeq:     senderSync,
			CreatedAtMs: now,
		},
		peerUID:    peer,
		deliveries: deliveries,
	}
	if len(deliveries) > 0 {
		res.peerPush = deliveries[0].push
	}
	return res, nil
}

func (s *memoryStore) targetsLocked(fromUID, cid, peer string) (title, kind string, members []string, err error) {
	if conv.IsGroup(cid) {
		g := s.groups[cid]
		if g == nil {
			return "", "", nil, fmt.Errorf("%w: group not found", errInvalid)
		}
		ok := false
		for _, m := range g.Members {
			members = append(members, m.UID)
			if m.UID == fromUID {
				ok = true
			}
		}
		if !ok {
			return "", "", nil, errNotMember
		}
		if err := canSpeak(g, fromUID); err != nil {
			return "", "", nil, err
		}
		return g.Name, conv.KindGroup, members, nil
	}
	return "", conv.KindP2P, []string{fromUID, peer}, nil
}

func (s *memoryStore) appendInbox(uid string, syncSeq uint64, cid string, row *timelineRow) {
	s.inbox[uid] = append(s.inbox[uid], &imv1.InboxEvent{
		SyncSeq:     syncSeq,
		Cid:         cid,
		MsgId:       row.msgID,
		ConvSeq:     row.convSeq,
		FromUid:     row.fromUID,
		Payload:     row.payload,
		CreatedAtMs: row.createdAt,
	})
}

func (s *memoryStore) upsertConv(uid, cid, peer, title, kind string, row *timelineRow, preview string, incoming bool) {
	if s.convs[uid] == nil {
		s.convs[uid] = map[string]*imv1.Conversation{}
	}
	cur := s.convs[uid][cid]
	unread := uint32(0)
	if cur != nil {
		unread = cur.Unread
		if title == "" {
			title = cur.Title
		}
		if kind == "" {
			kind = cur.Kind
		}
	}
	if incoming {
		unread++
	}
	if s.hidden[uid] != nil {
		delete(s.hidden[uid], cid)
	}
	if preview == "" && row.payload != nil {
		preview = clipText(row.payload.GetText(), 128)
	}
	s.convs[uid][cid] = &imv1.Conversation{
		Cid:         cid,
		PeerUid:     peer,
		LastMsgId:   row.msgID,
		LastConvSeq: row.convSeq,
		Unread:      unread,
		UpdatedAtMs: row.createdAt,
		LastText:    preview,
		Title:       title,
		Kind:        kind,
	}
}

func (s *memoryStore) Sync(_ context.Context, uid string, lastSeq uint64, limit int) (*imv1.SyncResponse, error) {
	if uid == "" {
		return nil, fmt.Errorf("%w: uid required", errInvalid)
	}
	if limit <= 0 {
		limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	all := s.inbox[uid]
	out := make([]*imv1.InboxEvent, 0, limit)
	for _, ev := range all {
		if ev.SyncSeq <= lastSeq {
			continue
		}
		out = append(out, ev)
		if len(out) == limit {
			break
		}
	}
	resp := &imv1.SyncResponse{Events: out, LastSyncSeq: lastSeq}
	if n := len(out); n > 0 {
		resp.LastSyncSeq = out[n-1].SyncSeq
		resp.HasMore = n == limit
	}
	return resp, nil
}

func (s *memoryStore) Watermark(_ context.Context, uid string) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := s.inbox[uid]
	if len(all) == 0 {
		return 0, nil
	}
	return all[len(all)-1].SyncSeq, nil
}

func (s *memoryStore) ListConversations(_ context.Context, uid string) ([]*imv1.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.convs[uid]
	out := make([]*imv1.Conversation, 0, len(m))
	for _, c := range m {
		if s.hidden[uid][c.Cid] {
			continue
		}
		cp := *c
		cp.Muted = s.mutes[uid][c.Cid]
		cp.Pinned = s.pins[uid][c.Cid]
		if conv.IsGroup(cp.Cid) {
			if g := s.groups[cp.Cid]; g != nil {
				cp.PeerProfile = groupPeerProfile(g)
			}
		}
		out = append(out, &cp)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			pi, pj := 0, 0
			if out[i].Pinned {
				pi = 1
			}
			if out[j].Pinned {
				pj = 1
			}
			if pj > pi || (pj == pi && out[j].UpdatedAtMs > out[i].UpdatedAtMs) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

func (s *memoryStore) Timeline(_ context.Context, uid, cid string, afterSeq uint64, limit int) (string, []*imv1.TimelineMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
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
			return "", nil, errNotMember
		}
	} else if _, err := conv.PeerUID(cid, uid); err != nil {
		return "", nil, fmt.Errorf("%w: %v", errInvalid, err)
	}
	rows := s.byCID[cid]
	out := make([]*imv1.TimelineMessage, 0, limit)
	for _, row := range rows {
		if row.convSeq <= afterSeq {
			continue
		}
		p := row.payload
		if row.recalled {
			p = &imv1.Payload{Type: imv1.Payload_RECALL, Text: ""}
		}
		out = append(out, &imv1.TimelineMessage{
			MsgId:       row.msgID,
			ConvSeq:     row.convSeq,
			FromUid:     row.fromUID,
			Payload:     p,
			CreatedAtMs: row.createdAt,
			Recalled:    row.recalled,
			QuoteMsgId:  row.quoteMsgID,
		})
		if len(out) == limit {
			break
		}
	}
	if c := s.convs[uid][cid]; c != nil {
		c.Unread = 0
	}
	return cid, out, nil
}

func (s *memoryStore) AddFriend(_ context.Context, uid, peerUID string) (bool, error) {
	uid, peerUID, err := normalizePair(uid, peerUID)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	already := s.hasFriend(uid, peerUID)
	s.putFriend(uid, peerUID)
	s.putFriend(peerUID, uid)
	if s.users[uid] == nil {
		s.users[uid] = &userRec{UID: uid, DisplayName: uid}
	}
	if s.users[peerUID] == nil {
		s.users[peerUID] = &userRec{UID: peerUID, DisplayName: peerUID}
	}
	return already, nil
}

func (s *memoryStore) ListFriends(_ context.Context, uid string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.friends[uid]
	out := make([]string, 0, len(set))
	for peer := range set {
		out = append(out, peer)
	}
	return out, nil
}

func (s *memoryStore) AreFriends(_ context.Context, uid, peerUID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hasFriend(uid, peerUID), nil
}

func (s *memoryStore) hasFriend(uid, peerUID string) bool {
	_, ok := s.friends[uid][peerUID]
	return ok
}

func (s *memoryStore) putFriend(uid, peerUID string) {
	if s.friends[uid] == nil {
		s.friends[uid] = map[string]struct{}{}
	}
	s.friends[uid][peerUID] = struct{}{}
}

func normalizePair(uid, peerUID string) (string, string, error) {
	uid = strings.TrimSpace(uid)
	peerUID = strings.TrimSpace(peerUID)
	if uid == "" || peerUID == "" {
		return "", "", fmt.Errorf("%w: uid required", errInvalid)
	}
	if uid == peerUID {
		return "", "", fmt.Errorf("%w: cannot add self", errInvalid)
	}
	return uid, peerUID, nil
}

func uniqueUIDs(ids []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func protoGroup(g *groupInfo) *imv1.GroupResponse {
	if g == nil {
		return nil
	}
	out := &imv1.GroupResponse{
		Cid: g.CID, Name: g.Name, OwnerUid: g.OwnerUID, AvatarUrl: g.AvatarURL,
		MutedAll: g.MutedAll, Announcement: g.Announcement, JoinApproval: g.JoinApproval, Mode: g.Mode,
	}
	for _, m := range g.Members {
		out.Members = append(out.Members, &imv1.GroupMember{Uid: m.UID, Role: m.Role, Nickname: m.Nickname, Muted: m.Muted})
	}
	return out
}

func (s *memoryStore) CreateGroup(_ context.Context, ownerUID, name string, memberUIDs []string, mode string) (*groupInfo, error) {
	ownerUID = strings.TrimSpace(ownerUID)
	name = strings.TrimSpace(name)
	if ownerUID == "" || name == "" {
		return nil, fmt.Errorf("%w: owner and name required", errInvalid)
	}
	members := uniqueUIDs(append([]string{ownerUID}, memberUIDs...))
	if len(members) > maxGroupMembers {
		return nil, errTooLarge
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, uid := range members {
		if uid == ownerUID {
			continue
		}
		if !s.hasFriend(ownerUID, uid) {
			return nil, fmt.Errorf("%w: add friend first", errNotFriends)
		}
	}
	cid := conv.GroupPrefix() + uuid.NewString()
	g := &groupInfo{CID: cid, Name: name, OwnerUID: ownerUID}
	applyGroupMode(g, mode)
	now := time.Now().UnixMilli()
	for _, uid := range members {
		role := "member"
		if uid == ownerUID {
			role = "owner"
		}
		g.Members = append(g.Members, groupMember{UID: uid, Role: role})
		s.seedGroupConv(uid, g, now)
	}
	s.groups[cid] = g
	return g, nil
}

func (s *memoryStore) seedGroupConv(uid string, g *groupInfo, now int64) {
	row := &timelineRow{msgID: "", convSeq: 0, createdAt: now, payload: &imv1.Payload{Text: "群聊已创建"}}
	s.upsertConv(uid, g.CID, "", g.Name, conv.KindGroup, row, "群聊已创建", false)
}

func (s *memoryStore) InviteGroup(_ context.Context, operatorUID, cid string, memberUIDs []string) (*groupInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.groups[cid]
	if g == nil {
		return nil, fmt.Errorf("%w: group not found", errInvalid)
	}
	if !s.isMemberLocked(g, operatorUID) {
		return nil, errNotMember
	}
	if err := canInvite(g, operatorUID); err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	pending := g.JoinApproval && !isManager(g, operatorUID)
	for _, uid := range uniqueUIDs(memberUIDs) {
		if s.isMemberLocked(g, uid) {
			continue
		}
		if !s.hasFriend(operatorUID, uid) {
			return nil, fmt.Errorf("%w: add friend first", errNotFriends)
		}
		if pending {
			if s.joins[cid] == nil {
				s.joins[cid] = map[string]joinReq{}
			}
			s.joins[cid][uid] = joinReq{UID: uid, FromUID: operatorUID, CreatedAtMs: now}
			continue
		}
		if len(g.Members)+1 > maxGroupMembers {
			return nil, errTooLarge
		}
		g.Members = append(g.Members, groupMember{UID: uid, Role: "member"})
		row := &timelineRow{msgID: "", convSeq: 0, createdAt: now, payload: &imv1.Payload{Text: "加入群聊"}}
		s.upsertConv(uid, g.CID, "", g.Name, conv.KindGroup, row, "加入群聊", true)
		if s.joins[cid] != nil {
			delete(s.joins[cid], uid)
		}
	}
	return g, nil
}

func (s *memoryStore) KickGroup(_ context.Context, operatorUID, cid, memberUID string) (*groupInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.groups[cid]
	if g == nil {
		return nil, fmt.Errorf("%w: group not found", errInvalid)
	}
	if err := canKick(g, operatorUID, memberUID); err != nil {
		return nil, err
	}
	kept := g.Members[:0]
	for _, m := range g.Members {
		if m.UID != memberUID {
			kept = append(kept, m)
		}
	}
	g.Members = kept
	if s.convs[memberUID] != nil {
		if c := s.convs[memberUID][cid]; c != nil {
			c.LastText = "你已被移出群聊"
		}
	}
	return g, nil
}

func (s *memoryStore) GetGroup(_ context.Context, uid, cid string) (*groupInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.groups[cid]
	if g == nil {
		return nil, fmt.Errorf("%w: group not found", errInvalid)
	}
	if !s.isMemberLocked(g, uid) {
		return nil, errNotMember
	}
	cp := *g
	cp.Members = append([]groupMember{}, g.Members...)
	return &cp, nil
}

func (s *memoryStore) GroupMembers(_ context.Context, cid string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.groups[cid]
	if g == nil {
		return nil, fmt.Errorf("%w: group not found", errInvalid)
	}
	var out []string
	for _, m := range g.Members {
		out = append(out, m.UID)
	}
	return out, nil
}

func (s *memoryStore) isMemberLocked(g *groupInfo, uid string) bool {
	for _, m := range g.Members {
		if m.UID == uid {
			return true
		}
	}
	return false
}

func (s *memoryStore) Recall(_ context.Context, uid, cid, msgID string) (*imv1.RecallNotify, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.byID[msgID]
	if row == nil || !containsCID(s.byCID[cid], msgID) {
		return nil, nil, fmt.Errorf("%w: message not found", errInvalid)
	}
	if row.fromUID != uid {
		return nil, nil, fmt.Errorf("%w: only sender can recall", errInvalid)
	}
	if time.Now().UnixMilli()-row.createdAt > recallWindowMS {
		return nil, nil, fmt.Errorf("%w: recall window exceeded", errInvalid)
	}
	row.recalled = true
	row.payload = &imv1.Payload{Type: imv1.Payload_RECALL, Text: ""}
	for _, convs := range s.convs {
		if c := convs[cid]; c != nil && c.LastMsgId == msgID {
			c.LastText = "已撤回一条消息"
		}
	}
	var mems []string
	if conv.IsGroup(cid) {
		var err error
		_, _, mems, err = s.targetsLocked(uid, cid, "")
		if err != nil {
			return nil, nil, err
		}
	} else {
		peer, err := conv.PeerUID(cid, uid)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", errInvalid, err)
		}
		mems = []string{uid, peer}
	}
	return &imv1.RecallNotify{Cid: cid, MsgId: msgID, FromUid: uid}, mems, nil
}

func containsCID(rows []*timelineRow, msgID string) bool {
	for _, r := range rows {
		if r != nil && r.msgID == msgID {
			return true
		}
	}
	return false
}

func (s *memoryStore) MarkRead(_ context.Context, uid, cid string, convSeq uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reads[uid] == nil {
		s.reads[uid] = map[string]uint64{}
	}
	if convSeq > s.reads[uid][cid] {
		s.reads[uid][cid] = convSeq
	}
	if c := s.convs[uid][cid]; c != nil {
		c.Unread = 0
	}
	return nil
}
