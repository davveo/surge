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

func (s *mysqlStore) ListReadCursors(ctx context.Context, uid, cid string) (map[string]uint64, int, error) {
	members := 2
	if conv.IsGroup(cid) {
		ids, err := s.GroupMembers(ctx, cid)
		if err != nil {
			return nil, 0, err
		}
		members = len(ids)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT uid, conv_seq FROM read_cursors WHERE cid = ?`, cid)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := map[string]uint64{}
	for rows.Next() {
		var id string
		var seq uint64
		if err := rows.Scan(&id, &seq); err != nil {
			return nil, 0, err
		}
		out[id] = seq
	}
	_ = uid
	return out, members, rows.Err()
}

func (s *mysqlStore) ResolveLogin(ctx context.Context, identifier string) (string, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return "", fmt.Errorf("%w: login required", errInvalid)
	}
	u, err := s.loadUser(ctx, identifier)
	if err != nil {
		return "", err
	}
	if u != nil {
		return u.UID, nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT uid FROM users WHERE email = ? OR phone = ? LIMIT 1`, strings.ToLower(identifier), identifier)
	var uid string
	if err := row.Scan(&uid); err == sql.ErrNoRows {
		return "", fmt.Errorf("%w: user not found", errAuth)
	} else if err != nil {
		return "", err
	}
	return uid, nil
}

func (s *mysqlStore) SetContacts(ctx context.Context, uid, email, phone string) error {
	if err := s.EnsureUser(ctx, uid); err != nil {
		return err
	}
	sets := []string{}
	args := []any{}
	if e := strings.ToLower(strings.TrimSpace(email)); e != "" {
		sets = append(sets, "email = ?")
		args = append(args, clipText(e, 128))
	}
	if p := strings.TrimSpace(phone); p != "" {
		sets = append(sets, "phone = ?")
		args = append(args, clipText(p, 32))
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, uid)
	_, err := s.db.ExecContext(ctx, `UPDATE users SET `+strings.Join(sets, ", ")+` WHERE uid = ?`, args...)
	return err
}

func (s *mysqlStore) SetPublicKey(ctx context.Context, uid, publicKey string) error {
	if err := s.EnsureUser(ctx, uid); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE users SET public_key = ? WHERE uid = ?`, strings.TrimSpace(publicKey), uid)
	return err
}

func sqlContains(q string) string {
	q = strings.NewReplacer("%", "", "_", "").Replace(strings.TrimSpace(q))
	return "%" + q + "%"
}

func (s *mysqlStore) SearchMessages(ctx context.Context, uid, query string, limit int) ([]*imv1.SearchHit, error) {
	query = strings.TrimSpace(query)
	if uid == "" || query == "" {
		return nil, fmt.Errorf("%w: uid and query required", errInvalid)
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	like := sqlContains(query)
	titleExpr := `COALESCE(NULLIF(r.remark,''), NULLIF(u.display_name,''), NULLIF(c.title,''), c.peer_uid)`
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.cid, `+titleExpr+`, c.last_text
		FROM conversations c
		LEFT JOIN friend_remarks r ON r.uid = c.uid AND r.peer_uid = c.peer_uid
		LEFT JOIN users u ON u.uid = c.peer_uid
		WHERE c.uid = ? AND (`+titleExpr+` LIKE ? OR c.peer_uid LIKE ?)
		ORDER BY c.updated_at_ms DESC
		LIMIT ?`, uid, like, like, limit)
	if err != nil {
		return nil, err
	}
	var hits []*imv1.SearchHit
	for rows.Next() {
		var cid, title, last string
		if err := rows.Scan(&cid, &title, &last); err != nil {
			rows.Close()
			return nil, err
		}
		hits = append(hits, &imv1.SearchHit{
			Cid:   cid,
			Title: title,
			Message: &imv1.TimelineMessage{
				Payload: &imv1.Payload{Type: imv1.Payload_TEXT, Text: last},
			},
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(hits) >= limit {
		return hits, nil
	}
	msgRows, err := s.db.QueryContext(ctx, `
		SELECT m.msg_id, m.cid, m.conv_seq, m.from_uid, m.payload_type, m.payload_text, m.payload_media, m.created_at_ms, m.recalled, `+titleExpr+`
		FROM messages m
		JOIN conversations c ON c.cid = m.cid AND c.uid = ?
		LEFT JOIN friend_remarks r ON r.uid = c.uid AND r.peer_uid = c.peer_uid
		LEFT JOIN users u ON u.uid = c.peer_uid
		WHERE c.uid = ? AND m.recalled = 0 AND m.payload_text LIKE ?
		ORDER BY m.created_at_ms DESC
		LIMIT ?`, uid, uid, like, limit)
	if err != nil {
		return nil, err
	}
	defer msgRows.Close()
	for msgRows.Next() {
		var msgID, cid, from, text, media, title string
		var ptype int32
		var seq uint64
		var created int64
		var recalled int
		if err := msgRows.Scan(&msgID, &cid, &seq, &from, &ptype, &text, &media, &created, &recalled, &title); err != nil {
			return nil, err
		}
		p := payloadFromCols(ptype, text, media, recalled != 0)
		if p != nil && p.E2Ee {
			continue
		}
		hits = append(hits, &imv1.SearchHit{
			Cid:   cid,
			Title: title,
			Message: &imv1.TimelineMessage{
				MsgId: msgID, ConvSeq: seq, FromUid: from, Payload: p, CreatedAtMs: created,
			},
		})
		if len(hits) >= limit {
			break
		}
	}
	return hits, msgRows.Err()
}

func (s *mysqlStore) SetGroupMuteAll(ctx context.Context, operatorUID, cid string, muted bool) (*groupInfo, error) {
	g, err := s.loadGroup(ctx, cid)
	if err != nil {
		return nil, err
	}
	if !isManager(g, operatorUID) {
		return nil, errNotAdmin
	}
	v := 0
	if muted {
		v = 1
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE im_groups SET muted_all = ? WHERE cid = ?`, v, cid); err != nil {
		return nil, err
	}
	g.MutedAll = muted
	return g, nil
}

func (s *mysqlStore) SetFriendTags(ctx context.Context, uid, peerUID string, tags []string) error {
	uid, peerUID, err := normalizePair(uid, peerUID)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM friend_tags WHERE uid = ? AND peer_uid = ?`, uid, peerUID); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	for _, t := range uniqueTags(tags) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO friend_tags (uid, peer_uid, tag, created_at_ms) VALUES (?, ?, ?, ?)`, uid, peerUID, t, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *mysqlStore) ListFriendTags(ctx context.Context, uid string) ([]*imv1.TagGroup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tag, peer_uid FROM friend_tags WHERE uid = ?`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	by := map[string][]string{}
	for rows.Next() {
		var tag, peer string
		if err := rows.Scan(&tag, &peer); err != nil {
			return nil, err
		}
		by[tag] = append(by[tag], peer)
	}
	var out []*imv1.TagGroup
	for name, uids := range by {
		out = append(out, &imv1.TagGroup{Name: name, Uids: uids})
	}
	return out, rows.Err()
}

func (s *mysqlStore) FriendTagsOf(ctx context.Context, uid, peerUID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tag FROM friend_tags WHERE uid = ? AND peer_uid = ?`, uid, peerUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *mysqlStore) ConsumeEphemeral(ctx context.Context, uid, cid, msgID string) (*imv1.RecallNotify, []string, error) {
	var from, media string
	var ptype int32
	var recalled int
	err := s.db.QueryRowContext(ctx, `SELECT from_uid, payload_type, payload_media, recalled, cid FROM messages WHERE msg_id = ?`, msgID).
		Scan(&from, &ptype, &media, &recalled, &cid)
	if err == sql.ErrNoRows {
		return nil, nil, fmt.Errorf("%w: message not found", errInvalid)
	}
	if err != nil {
		return nil, nil, err
	}
	p := payloadFromCols(ptype, "", media, false)
	if p == nil || !p.Ephemeral {
		return nil, nil, fmt.Errorf("%w: not ephemeral", errInvalid)
	}
	if from == uid {
		return nil, nil, fmt.Errorf("%w: sender cannot burn", errInvalid)
	}
	blob := unmarshalPayloadBlob(media)
	blob.Burned = true
	raw, err := json.Marshal(blob)
	if err != nil {
		return nil, nil, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE messages SET recalled = 1, payload_media = ? WHERE msg_id = ?`, string(raw), msgID); err != nil {
		return nil, nil, err
	}
	var members []string
	if conv.IsGroup(cid) {
		members, err = s.GroupMembers(ctx, cid)
		if err != nil {
			return nil, nil, err
		}
	} else if peer, err := conv.PeerUID(cid, uid); err == nil {
		members = []string{uid, peer}
	}
	return &imv1.RecallNotify{Cid: cid, MsgId: msgID, FromUid: uid}, members, nil
}

func (s *mysqlStore) AddSticker(ctx context.Context, uid, url, pack string) (*imv1.Sticker, error) {
	url = strings.TrimSpace(url)
	if uid == "" || url == "" {
		return nil, fmt.Errorf("%w: url required", errInvalid)
	}
	if pack = strings.TrimSpace(pack); pack == "" {
		pack = "mine"
	}
	st := &imv1.Sticker{Id: uuid.NewString(), Url: url, Pack: pack}
	_, err := s.db.ExecContext(ctx, `INSERT INTO stickers (id, uid, url, pack, created_at_ms) VALUES (?, ?, ?, ?, ?)`,
		st.Id, uid, st.Url, st.Pack, time.Now().UnixMilli())
	return st, err
}

func (s *mysqlStore) ListStickers(ctx context.Context, uid string) ([]*imv1.Sticker, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, url, pack FROM stickers WHERE uid = ? ORDER BY created_at_ms DESC`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*imv1.Sticker
	for rows.Next() {
		st := &imv1.Sticker{}
		if err := rows.Scan(&st.Id, &st.Url, &st.Pack); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *mysqlStore) DeleteSticker(ctx context.Context, uid, id string) error {
	id = strings.TrimSpace(id)
	if uid == "" || id == "" {
		return fmt.Errorf("%w: id required", errInvalid)
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM stickers WHERE uid = ? AND id = ?`, uid, id)
	return err
}
