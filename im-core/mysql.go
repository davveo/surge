package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/davveo/surge/pkg/conv"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

type mysqlStore struct {
	db  *sql.DB
	seq Seq
}

func newMySQLStore(db *sql.DB, seq Seq) *mysqlStore {
	return &mysqlStore{db: db, seq: seq}
}

func migrate(db *sql.DB, schema string) error {
	for _, stmt := range strings.Split(schema, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

func (s *mysqlStore) Send(ctx context.Context, fromUID, clientMsgID, cid, peerUID string, payload *imv1.Payload) (*sendResult, error) {
	if err := validateSend(fromUID, clientMsgID, payload); err != nil {
		return nil, err
	}
	canonical, peer, err := conv.ResolveCID(fromUID, cid, peerUID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalid, err)
	}

	if dup, err := s.loadDup(ctx, fromUID, clientMsgID, canonical); err != nil {
		return nil, err
	} else if dup != nil {
		return dup, nil
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO messages (msg_id, client_msg_id, cid, conv_seq, from_uid, payload_type, payload_text, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		msgID, clientMsgID, canonical, convSeq, fromUID, int32(payload.Type), payload.Text, now)
	if err != nil {
		if isDupErr(err) {
			return s.loadDup(ctx, fromUID, clientMsgID, canonical)
		}
		return nil, fmt.Errorf("insert message: %w", err)
	}

	if err := insertInbox(ctx, tx, fromUID, senderSync, canonical, msgID, convSeq, fromUID, now); err != nil {
		return nil, err
	}
	if err := insertInbox(ctx, tx, peer, peerSync, canonical, msgID, convSeq, fromUID, now); err != nil {
		return nil, err
	}
	preview := clipText(payload.Text, 128)
	if err := upsertConv(ctx, tx, fromUID, canonical, peer, msgID, convSeq, preview, now, false); err != nil {
		return nil, err
	}
	if err := upsertConv(ctx, tx, peer, canonical, fromUID, msgID, convSeq, preview, now, true); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		if isDupErr(err) {
			return s.loadDup(ctx, fromUID, clientMsgID, canonical)
		}
		return nil, err
	}

	return &sendResult{
		ack: &imv1.Ack{
			ClientMsgId: clientMsgID,
			MsgId:       msgID,
			Cid:         canonical,
			ConvSeq:     convSeq,
			SyncSeq:     senderSync,
			CreatedAtMs: now,
		},
		peerUID: peer,
		peerPush: &imv1.Push{
			Cid:         canonical,
			MsgId:       msgID,
			ConvSeq:     convSeq,
			SyncSeq:     peerSync,
			FromUid:     fromUID,
			Payload:     payload,
			CreatedAtMs: now,
		},
	}, nil
}

func insertInbox(ctx context.Context, tx *sql.Tx, uid string, syncSeq uint64, cid, msgID string, convSeq uint64, fromUID string, now int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO inbox (uid, sync_seq, cid, msg_id, conv_seq, from_uid, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, uid, syncSeq, cid, msgID, convSeq, fromUID, now)
	if err != nil {
		return fmt.Errorf("insert inbox: %w", err)
	}
	return nil
}

func upsertConv(ctx context.Context, tx *sql.Tx, uid, cid, peer, msgID string, convSeq uint64, text string, now int64, incoming bool) error {
	unreadInc := 0
	if incoming {
		unreadInc = 1
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO conversations (uid, cid, peer_uid, last_msg_id, last_conv_seq, last_text, unread, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			last_msg_id = VALUES(last_msg_id),
			last_conv_seq = VALUES(last_conv_seq),
			last_text = VALUES(last_text),
			unread = unread + ?,
			updated_at_ms = VALUES(updated_at_ms)`,
		uid, cid, peer, msgID, convSeq, text, unreadInc, now, unreadInc)
	if err != nil {
		return fmt.Errorf("upsert conversation: %w", err)
	}
	return nil
}

func (s *mysqlStore) loadDup(ctx context.Context, fromUID, clientMsgID, cid string) (*sendResult, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT m.msg_id, m.conv_seq, m.created_at_ms, i.sync_seq
		FROM messages m
		JOIN inbox i ON i.msg_id = m.msg_id AND i.uid = m.from_uid
		WHERE m.from_uid = ? AND m.client_msg_id = ?
		LIMIT 1`, fromUID, clientMsgID)
	var msgID string
	var convSeq, syncSeq uint64
	var created int64
	err := row.Scan(&msgID, &convSeq, &created, &syncSeq)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sendResult{
		ack: &imv1.Ack{
			ClientMsgId: clientMsgID,
			MsgId:       msgID,
			Cid:         cid,
			ConvSeq:     convSeq,
			SyncSeq:     syncSeq,
			CreatedAtMs: created,
			Duplicate:   true,
		},
	}, nil
}

func (s *mysqlStore) Sync(ctx context.Context, uid string, lastSeq uint64, limit int) (*imv1.SyncResponse, error) {
	if uid == "" {
		return nil, fmt.Errorf("%w: uid required", errInvalid)
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.sync_seq, i.cid, i.msg_id, i.conv_seq, i.from_uid, i.created_at_ms, m.payload_type, m.payload_text
		FROM inbox i
		JOIN messages m ON m.msg_id = i.msg_id
		WHERE i.uid = ? AND i.sync_seq > ?
		ORDER BY i.sync_seq ASC
		LIMIT ?`, uid, lastSeq, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]*imv1.InboxEvent, 0, limit)
	for rows.Next() {
		ev := &imv1.InboxEvent{Payload: &imv1.Payload{}}
		var ptype int32
		if err := rows.Scan(&ev.SyncSeq, &ev.Cid, &ev.MsgId, &ev.ConvSeq, &ev.FromUid, &ev.CreatedAtMs, &ptype, &ev.Payload.Text); err != nil {
			return nil, err
		}
		ev.Payload.Type = imv1.Payload_Type(ptype)
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	resp := &imv1.SyncResponse{LastSyncSeq: lastSeq, Events: events}
	if len(events) > limit {
		resp.Events = events[:limit]
		resp.HasMore = true
	}
	if n := len(resp.Events); n > 0 {
		resp.LastSyncSeq = resp.Events[n-1].SyncSeq
	}
	return resp, nil
}

func (s *mysqlStore) Watermark(ctx context.Context, uid string) (uint64, error) {
	var seq sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(sync_seq) FROM inbox WHERE uid = ?`, uid).Scan(&seq)
	if err != nil {
		return 0, err
	}
	if !seq.Valid {
		return 0, nil
	}
	return uint64(seq.Int64), nil
}

func (s *mysqlStore) ListConversations(ctx context.Context, uid string) ([]*imv1.Conversation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT cid, peer_uid, last_msg_id, last_conv_seq, unread, updated_at_ms, last_text
		FROM conversations WHERE uid = ?
		ORDER BY updated_at_ms DESC`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*imv1.Conversation
	for rows.Next() {
		c := &imv1.Conversation{}
		if err := rows.Scan(&c.Cid, &c.PeerUid, &c.LastMsgId, &c.LastConvSeq, &c.Unread, &c.UpdatedAtMs, &c.LastText); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *mysqlStore) Timeline(ctx context.Context, uid, cid string, afterSeq uint64, limit int) (string, []*imv1.TimelineMessage, error) {
	if _, err := conv.PeerUID(cid, uid); err != nil {
		return "", nil, fmt.Errorf("%w: %v", errInvalid, err)
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT msg_id, conv_seq, from_uid, payload_type, payload_text, created_at_ms
		FROM messages WHERE cid = ? AND conv_seq > ?
		ORDER BY conv_seq ASC
		LIMIT ?`, cid, afterSeq, limit)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	var out []*imv1.TimelineMessage
	for rows.Next() {
		m := &imv1.TimelineMessage{Payload: &imv1.Payload{}}
		var ptype int32
		if err := rows.Scan(&m.MsgId, &m.ConvSeq, &m.FromUid, &ptype, &m.Payload.Text, &m.CreatedAtMs); err != nil {
			return "", nil, err
		}
		m.Payload.Type = imv1.Payload_Type(ptype)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE conversations SET unread = 0 WHERE uid = ? AND cid = ?`, uid, cid)
	return cid, out, nil
}

func (s *mysqlStore) AddFriend(ctx context.Context, uid, peerUID string) (bool, error) {
	uid, peerUID, err := normalizePair(uid, peerUID)
	if err != nil {
		return false, err
	}
	already, err := s.AreFriends(ctx, uid, peerUID)
	if err != nil {
		return false, err
	}
	now := time.Now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		INSERT IGNORE INTO friends (uid, peer_uid, created_at_ms) VALUES (?, ?, ?), (?, ?, ?)`,
		uid, peerUID, now, peerUID, uid, now); err != nil {
		return false, fmt.Errorf("insert friends: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return already, nil
}

func (s *mysqlStore) ListFriends(ctx context.Context, uid string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT peer_uid FROM friends WHERE uid = ? ORDER BY peer_uid`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var peer string
		if err := rows.Scan(&peer); err != nil {
			return nil, err
		}
		out = append(out, peer)
	}
	return out, rows.Err()
}

func (s *mysqlStore) AreFriends(ctx context.Context, uid, peerUID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM friends WHERE uid = ? AND peer_uid = ? LIMIT 1`, uid, peerUID).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func isDupErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Duplicate entry") || strings.Contains(msg, "UNIQUE constraint")
}
