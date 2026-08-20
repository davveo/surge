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

type sendResult struct {
	ack      *imv1.Ack
	peerUID  string
	peerPush *imv1.Push
}

type timelineRow struct {
	msgID      string
	convSeq    uint64
	fromUID    string
	payload    *imv1.Payload
	createdAt  int64
	clientMsg  string
	senderSync uint64
}

type Store interface {
	Send(ctx context.Context, fromUID, clientMsgID, cid, peerUID string, payload *imv1.Payload) (*sendResult, error)
	Sync(ctx context.Context, uid string, lastSeq uint64, limit int) (*imv1.SyncResponse, error)
	Watermark(ctx context.Context, uid string) (uint64, error)
	ListConversations(ctx context.Context, uid string) ([]*imv1.Conversation, error)
	Timeline(ctx context.Context, uid, cid string, afterSeq uint64, limit int) (string, []*imv1.TimelineMessage, error)
	AddFriend(ctx context.Context, uid, peerUID string) (already bool, err error)
	ListFriends(ctx context.Context, uid string) ([]string, error)
	AreFriends(ctx context.Context, uid, peerUID string) (bool, error)
}

func validateSend(fromUID, clientMsgID string, payload *imv1.Payload) error {
	if fromUID == "" || clientMsgID == "" {
		return fmt.Errorf("%w: from_uid and client_msg_id required", errInvalid)
	}
	if len(clientMsgID) > 64 {
		return fmt.Errorf("%w: client_msg_id too long", errInvalid)
	}
	if payload == nil || payload.Type != imv1.Payload_TEXT {
		return fmt.Errorf("%w: P0 only supports text payload", errInvalid)
	}
	if payload.Text == "" {
		return fmt.Errorf("%w: empty text", errInvalid)
	}
	if utf8.RuneCountInString(payload.Text) > 4000 {
		return fmt.Errorf("%w: text too long", errInvalid)
	}
	return nil
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
	mu      sync.Mutex
	seq     Seq
	byID    map[string]*timelineRow
	byDup   map[string]*timelineRow
	byCID   map[string][]*timelineRow
	inbox   map[string][]*imv1.InboxEvent
	convs   map[string]map[string]*imv1.Conversation
	friends map[string]map[string]struct{}
}

func newMemoryStore(seq Seq) *memoryStore {
	if seq == nil {
		seq = newMemSeq()
	}
	return &memoryStore{
		seq:     seq,
		byID:    map[string]*timelineRow{},
		byDup:   map[string]*timelineRow{},
		byCID:   map[string][]*timelineRow{},
		inbox:   map[string][]*imv1.InboxEvent{},
		convs:   map[string]map[string]*imv1.Conversation{},
		friends: map[string]map[string]struct{}{},
	}
}

func dupKey(fromUID, clientMsgID string) string { return fromUID + "|" + clientMsgID }

func (s *memoryStore) Send(ctx context.Context, fromUID, clientMsgID, cid, peerUID string, payload *imv1.Payload) (*sendResult, error) {
	if err := validateSend(fromUID, clientMsgID, payload); err != nil {
		return nil, err
	}
	canonical, peer, err := conv.ResolveCID(fromUID, cid, peerUID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalid, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

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
	senderSync, err := s.seq.Next(ctx, syncSeqKey(fromUID))
	if err != nil {
		return nil, err
	}
	peerSync, err := s.seq.Next(ctx, syncSeqKey(peer))
	if err != nil {
		return nil, err
	}

	now := time.Now().UnixMilli()
	msgID := uuid.NewString()
	row := &timelineRow{
		msgID:      msgID,
		convSeq:    convSeq,
		fromUID:    fromUID,
		payload:    payload,
		createdAt:  now,
		clientMsg:  clientMsgID,
		senderSync: senderSync,
	}
	s.byID[msgID] = row
	s.byDup[dupKey(fromUID, clientMsgID)] = row
	s.byCID[canonical] = append(s.byCID[canonical], row)

	s.appendInbox(fromUID, senderSync, canonical, row)
	s.appendInbox(peer, peerSync, canonical, row)
	s.upsertConv(fromUID, canonical, peer, row, false)
	s.upsertConv(peer, canonical, fromUID, row, true)

	ack := &imv1.Ack{
		ClientMsgId: clientMsgID,
		MsgId:       msgID,
		Cid:         canonical,
		ConvSeq:     convSeq,
		SyncSeq:     senderSync,
		CreatedAtMs: now,
	}
	push := &imv1.Push{
		Cid:         canonical,
		MsgId:       msgID,
		ConvSeq:     convSeq,
		SyncSeq:     peerSync,
		FromUid:     fromUID,
		Payload:     payload,
		CreatedAtMs: now,
	}
	return &sendResult{ack: ack, peerUID: peer, peerPush: push}, nil
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

func (s *memoryStore) upsertConv(uid, cid, peer string, row *timelineRow, incoming bool) {
	if s.convs[uid] == nil {
		s.convs[uid] = map[string]*imv1.Conversation{}
	}
	cur := s.convs[uid][cid]
	unread := uint32(0)
	if cur != nil {
		unread = cur.Unread
	}
	if incoming {
		unread++
	}
	s.convs[uid][cid] = &imv1.Conversation{
		Cid:         cid,
		PeerUid:     peer,
		LastMsgId:   row.msgID,
		LastConvSeq: row.convSeq,
		Unread:      unread,
		UpdatedAtMs: row.createdAt,
		LastText:    clipText(row.payload.GetText(), 128),
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
		out = append(out, c)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].UpdatedAtMs > out[i].UpdatedAtMs {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

func (s *memoryStore) Timeline(_ context.Context, uid, cid string, afterSeq uint64, limit int) (string, []*imv1.TimelineMessage, error) {
	if _, err := conv.PeerUID(cid, uid); err != nil {
		return "", nil, fmt.Errorf("%w: %v", errInvalid, err)
	}
	if limit <= 0 {
		limit = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := s.byCID[cid]
	out := make([]*imv1.TimelineMessage, 0, limit)
	for _, row := range rows {
		if row.convSeq <= afterSeq {
			continue
		}
		out = append(out, &imv1.TimelineMessage{
			MsgId:       row.msgID,
			ConvSeq:     row.convSeq,
			FromUid:     row.fromUID,
			Payload:     row.payload,
			CreatedAtMs: row.createdAt,
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
