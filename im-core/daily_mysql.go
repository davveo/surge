package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/davveo/surge/pkg/conv"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

func (s *mysqlStore) attachReactions(ctx context.Context, msgs []*imv1.TimelineMessage) {
	if len(msgs) == 0 {
		return
	}
	ids := make([]string, 0, len(msgs))
	idx := map[string]*imv1.TimelineMessage{}
	for _, m := range msgs {
		if m == nil || m.MsgId == "" {
			continue
		}
		ids = append(ids, m.MsgId)
		idx[m.MsgId] = m
	}
	got, err := s.ReactionsOf(ctx, ids)
	if err != nil {
		return
	}
	for id, buckets := range got {
		if m := idx[id]; m != nil {
			m.Reactions = buckets
		}
	}
}

func (s *mysqlStore) LookupMessage(ctx context.Context, uid, cid, msgID string) (*timelineRow, error) {
	var ptype int32
	var text, media, from string
	var recalled int
	var created int64
	var seq uint64
	err := s.db.QueryRowContext(ctx, `SELECT from_uid, payload_type, payload_text, COALESCE(payload_media, ''), created_at_ms, recalled, conv_seq FROM messages WHERE msg_id = ? AND cid = ?`, msgID, cid).
		Scan(&from, &ptype, &text, &media, &created, &recalled, &seq)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: message not found", errInvalid)
	}
	if err != nil {
		return nil, err
	}
	_ = uid
	return &timelineRow{
		msgID:     msgID,
		convSeq:   seq,
		fromUID:   from,
		payload:   payloadFromCols(ptype, text, media, recalled != 0),
		createdAt: created,
		recalled:  recalled != 0,
	}, nil
}

func (s *mysqlStore) ReactMessage(ctx context.Context, uid, cid, msgID, emoji string) ([]*imv1.ReactionBucket, error) {
	uid, cid, msgID = strings.TrimSpace(uid), strings.TrimSpace(cid), strings.TrimSpace(msgID)
	emoji = clipText(strings.TrimSpace(emoji), 8)
	if uid == "" || msgID == "" {
		return nil, fmt.Errorf("%w: uid and msg_id required", errInvalid)
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM messages WHERE msg_id = ? AND cid = ?`, msgID, cid).Scan(&n)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: message not found", errInvalid)
	}
	if err != nil {
		return nil, err
	}
	if emoji == "" {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM message_reactions WHERE msg_id = ? AND uid = ?`, msgID, uid)
	} else {
		var cur string
		err = s.db.QueryRowContext(ctx, `SELECT emoji FROM message_reactions WHERE msg_id = ? AND uid = ?`, msgID, uid).Scan(&cur)
		if err == nil && cur == emoji {
			_, _ = s.db.ExecContext(ctx, `DELETE FROM message_reactions WHERE msg_id = ? AND uid = ?`, msgID, uid)
		} else {
			_, err = s.db.ExecContext(ctx, `
				INSERT INTO message_reactions (msg_id, uid, emoji, created_at_ms) VALUES (?, ?, ?, ?)
				ON DUPLICATE KEY UPDATE emoji = VALUES(emoji), created_at_ms = VALUES(created_at_ms)`,
				msgID, uid, emoji, time.Now().UnixMilli())
			if err != nil {
				return nil, err
			}
		}
	}
	got, err := s.ReactionsOf(ctx, []string{msgID})
	if err != nil {
		return nil, err
	}
	return got[msgID], nil
}

func (s *mysqlStore) ReactionsOf(ctx context.Context, msgIDs []string) (map[string][]*imv1.ReactionBucket, error) {
	out := map[string][]*imv1.ReactionBucket{}
	if len(msgIDs) == 0 {
		return out, nil
	}
	args := make([]interface{}, 0, len(msgIDs))
	ph := make([]string, 0, len(msgIDs))
	for _, id := range msgIDs {
		if id == "" {
			continue
		}
		args = append(args, id)
		ph = append(ph, "?")
	}
	if len(args) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT msg_id, uid, emoji FROM message_reactions WHERE msg_id IN (`+strings.Join(ph, ",")+`) ORDER BY created_at_ms`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tmp := map[string]map[string]string{}
	for rows.Next() {
		var msgID, uid, emoji string
		if err := rows.Scan(&msgID, &uid, &emoji); err != nil {
			return nil, err
		}
		if tmp[msgID] == nil {
			tmp[msgID] = map[string]string{}
		}
		tmp[msgID][uid] = emoji
	}
	for id, by := range tmp {
		out[id] = reactionBuckets(by)
	}
	return out, rows.Err()
}

func (s *mysqlStore) AddFavorite(ctx context.Context, uid, cid, msgID string) (*imv1.Favorite, error) {
	row, err := s.LookupMessage(ctx, uid, cid, msgID)
	if err != nil {
		return nil, err
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
	blob, _ := json.Marshal(map[string]interface{}{
		"type": int32(0),
		"text": "",
	})
	if row.payload != nil {
		blob, _ = json.Marshal(struct {
			Type    int32  `json:"type"`
			Text    string `json:"text"`
			Media   string `json:"media"`
			CardUID string `json:"cardUid,omitempty"`
		}{
			Type:    int32(row.payload.Type),
			Text:    row.payload.Text,
			Media:   marshalPayloadBlob(row.payload),
			CardUID: row.payload.CardUid,
		})
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO favorites (fav_id, uid, cid, msg_id, from_uid, preview, payload_json, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE preview = VALUES(preview), payload_json = VALUES(payload_json)`,
		fav.FavId, uid, cid, msgID, fav.FromUid, fav.Preview, string(blob), fav.CreatedAtMs)
	if err != nil {
		return nil, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT fav_id, created_at_ms FROM favorites WHERE uid = ? AND msg_id = ?`, uid, msgID).Scan(&fav.FavId, &fav.CreatedAtMs)
	return fav, err
}

func (s *mysqlStore) ListFavorites(ctx context.Context, uid, query string) ([]*imv1.Favorite, error) {
	q := `SELECT fav_id, cid, msg_id, from_uid, preview, payload_json, created_at_ms FROM favorites WHERE uid = ?`
	args := []interface{}{uid}
	if query = strings.TrimSpace(query); query != "" {
		q += ` AND (preview LIKE ? OR from_uid LIKE ?)`
		like := "%" + query + "%"
		args = append(args, like, like)
	}
	q += ` ORDER BY created_at_ms DESC LIMIT 200`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*imv1.Favorite
	for rows.Next() {
		f := &imv1.Favorite{}
		var raw string
		if err := rows.Scan(&f.FavId, &f.Cid, &f.MsgId, &f.FromUid, &f.Preview, &raw, &f.CreatedAtMs); err != nil {
			return nil, err
		}
		var blob struct {
			Type    int32  `json:"type"`
			Text    string `json:"text"`
			Media   string `json:"media"`
			CardUID string `json:"cardUid"`
		}
		_ = json.Unmarshal([]byte(raw), &blob)
		f.Payload = payloadFromCols(blob.Type, blob.Text, blob.Media, false)
		if f.Payload != nil && blob.CardUID != "" {
			f.Payload.CardUid = blob.CardUID
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *mysqlStore) DeleteFavorite(ctx context.Context, uid, favID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM favorites WHERE uid = ? AND (fav_id = ? OR msg_id = ?)`, uid, favID, favID)
	return err
}

func (s *mysqlStore) CreateGroupInvite(ctx context.Context, uid, cid string) (*imv1.GroupInvite, error) {
	ok, err := s.isMember(ctx, cid, uid)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errNotMember
	}
	tok := inviteToken()
	now := time.Now()
	_, err = s.db.ExecContext(ctx, `INSERT INTO group_invites (token, cid, from_uid, created_at_ms, expires_at_ms) VALUES (?, ?, ?, ?, ?)`,
		tok, cid, uid, now.UnixMilli(), now.Add(7*24*time.Hour).UnixMilli())
	if err != nil {
		return nil, err
	}
	return &imv1.GroupInvite{Token: tok, Cid: cid, ExpiresAtMs: now.Add(7 * 24 * time.Hour).UnixMilli()}, nil
}

func (s *mysqlStore) JoinByInvite(ctx context.Context, uid, token string) (*groupInfo, error) {
	var cid, from string
	var exp int64
	err := s.db.QueryRowContext(ctx, `SELECT cid, from_uid, expires_at_ms FROM group_invites WHERE token = ?`, strings.TrimSpace(token)).Scan(&cid, &from, &exp)
	_ = from
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: invite expired", errInvalid)
	}
	if err != nil {
		return nil, err
	}
	if time.Now().UnixMilli() > exp {
		return nil, fmt.Errorf("%w: invite expired", errInvalid)
	}
	g, err := s.loadGroup(ctx, cid)
	if err != nil {
		return nil, err
	}
	if memberOf(g, uid) != nil {
		return g, nil
	}
	if g.JoinApproval {
		return s.RequestJoin(ctx, uid, cid)
	}
	now := time.Now().UnixMilli()
	if len(g.Members)+1 > maxGroupMembers {
		return nil, errTooLarge
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO group_members (cid, uid, role, joined_at_ms) VALUES (?, ?, 'member', ?)`, cid, uid, now); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := upsertConv(ctx, tx, uid, cid, "", g.Name, conv.KindGroup, "", 0, "加入群聊", now, true); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.loadGroup(ctx, cid)
}

func (s *mysqlStore) SetDraft(ctx context.Context, uid, cid, text string) error {
	uid, cid = strings.TrimSpace(uid), strings.TrimSpace(cid)
	if uid == "" || cid == "" {
		return fmt.Errorf("%w: cid required", errInvalid)
	}
	text = clipText(text, 4000)
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO conversations (uid, cid, peer_uid, last_msg_id, last_conv_seq, last_text, unread, updated_at_ms, title, kind, draft_text)
		VALUES (?, ?, '', '', 0, '', 0, ?, '', '', ?)
		ON DUPLICATE KEY UPDATE draft_text = VALUES(draft_text)`, uid, cid, now, text)
	return err
}

func (s *mysqlStore) PinChatMessage(ctx context.Context, uid, cid, msgID string) (*imv1.PinnedMessage, error) {
	ok, err := s.isMemberOrConv(ctx, uid, cid)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errNotMember
	}
	msgID = strings.TrimSpace(msgID)
	if msgID == "" {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM chat_pins WHERE cid = ?`, cid)
		return &imv1.PinnedMessage{Cid: cid}, nil
	}
	row, err := s.LookupMessage(ctx, uid, cid, msgID)
	if err != nil {
		return nil, err
	}
	prev := previewOf(row.payload)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO chat_pins (cid, msg_id, from_uid, preview, created_at_ms) VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE msg_id = VALUES(msg_id), from_uid = VALUES(from_uid), preview = VALUES(preview), created_at_ms = VALUES(created_at_ms)`,
		cid, msgID, row.fromUID, prev, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	return &imv1.PinnedMessage{Cid: cid, MsgId: msgID, FromUid: row.fromUID, Text: prev}, nil
}

func (s *mysqlStore) GetPinnedMessage(ctx context.Context, uid, cid string) (*imv1.PinnedMessage, error) {
	var pin imv1.PinnedMessage
	err := s.db.QueryRowContext(ctx, `SELECT cid, msg_id, from_uid, preview FROM chat_pins WHERE cid = ?`, cid).
		Scan(&pin.Cid, &pin.MsgId, &pin.FromUid, &pin.Text)
	if err == sql.ErrNoRows {
		return &imv1.PinnedMessage{Cid: cid}, nil
	}
	_ = uid
	return &pin, err
}

func (s *mysqlStore) isMemberOrConv(ctx context.Context, uid, cid string) (bool, error) {
	if strings.HasPrefix(cid, "grp:") {
		return s.isMember(ctx, cid, uid)
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM conversations WHERE uid = ? AND cid = ?`, uid, cid).Scan(&n)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *mysqlStore) ReportMessage(ctx context.Context, uid, cid, msgID, reason string) error {
	if strings.TrimSpace(msgID) == "" {
		return fmt.Errorf("%w: msg_id required", errInvalid)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO reports (id, uid, cid, msg_id, reason, created_at_ms) VALUES (?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), uid, cid, msgID, clipText(reason, 256), time.Now().UnixMilli())
	return err
}

func (s *mysqlStore) GetSettings(ctx context.Context, uid string) (*imv1.UserSettings, error) {
	st := defaultSettings(uid)
	var dark, sound, preview, atMuted, hideRead, hideTyping, hideSeen int
	err := s.db.QueryRowContext(ctx, `SELECT dark, wallpaper, notify_sound, notify_preview, dnd_start, dnd_end,
		IFNULL(notify_at_muted, 1), IFNULL(add_me, 'verify'), IFNULL(hide_read, 0), IFNULL(hide_typing, 0), IFNULL(hide_last_seen, 0), IFNULL(burn_sec, 5)
		FROM user_settings WHERE uid = ?`, uid).
		Scan(&dark, &st.Wallpaper, &sound, &preview, &st.DndStart, &st.DndEnd, &atMuted, &st.AddMe, &hideRead, &hideTyping, &hideSeen, &st.BurnSec)
	if err == sql.ErrNoRows {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	st.Dark = dark != 0
	st.NotifySound = sound != 0
	st.NotifyPreview = preview != 0
	st.NotifyAtMuted = atMuted != 0
	st.HideRead = hideRead != 0
	st.HideTyping = hideTyping != 0
	st.HideLastSeen = hideSeen != 0
	return fillSettingsDefaults(st), nil
}

func (s *mysqlStore) SetSettings(ctx context.Context, st *imv1.UserSettings) (*imv1.UserSettings, error) {
	if st == nil || strings.TrimSpace(st.Uid) == "" {
		return nil, fmt.Errorf("%w: uid required", errInvalid)
	}
	st = fillSettingsDefaults(st)
	dark, sound, preview, atMuted, hideRead, hideTyping, hideSeen := 0, 0, 0, 0, 0, 0, 0
	if st.Dark {
		dark = 1
	}
	if st.NotifySound {
		sound = 1
	}
	if st.NotifyPreview {
		preview = 1
	}
	if st.NotifyAtMuted {
		atMuted = 1
	}
	if st.HideRead {
		hideRead = 1
	}
	if st.HideTyping {
		hideTyping = 1
	}
	if st.HideLastSeen {
		hideSeen = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO user_settings (uid, dark, wallpaper, notify_sound, notify_preview, dnd_start, dnd_end, notify_at_muted, add_me, hide_read, hide_typing, hide_last_seen, burn_sec)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE dark = VALUES(dark), wallpaper = VALUES(wallpaper), notify_sound = VALUES(notify_sound),
			notify_preview = VALUES(notify_preview), dnd_start = VALUES(dnd_start), dnd_end = VALUES(dnd_end),
			notify_at_muted = VALUES(notify_at_muted), add_me = VALUES(add_me), hide_read = VALUES(hide_read),
			hide_typing = VALUES(hide_typing), hide_last_seen = VALUES(hide_last_seen), burn_sec = VALUES(burn_sec)`,
		st.Uid, dark, clipText(st.Wallpaper, 512), sound, preview, clipText(st.DndStart, 8), clipText(st.DndEnd, 8),
		atMuted, clipText(st.AddMe, 16), hideRead, hideTyping, hideSeen, st.BurnSec)
	if err != nil {
		return nil, err
	}
	return s.GetSettings(ctx, st.Uid)
}
