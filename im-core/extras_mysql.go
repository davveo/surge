package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/davveo/surge/pkg/conv"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

func (s *mysqlStore) GetProfiles(ctx context.Context, uids []string) ([]*imv1.UserProfile, error) {
	uids = uniqueUIDs(uids)
	if len(uids) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(uids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(uids))
	for _, u := range uids {
		args = append(args, u)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT uid, password_hash, display_name, avatar_url FROM users WHERE uid IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := map[string]*imv1.UserProfile{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		found[u.UID] = profileOf(u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []*imv1.UserProfile
	for _, uid := range uids {
		if p := found[uid]; p != nil {
			out = append(out, p)
		} else {
			out = append(out, &imv1.UserProfile{Uid: uid, DisplayName: uid})
		}
	}
	return out, nil
}

func (s *mysqlStore) RemoveFriend(ctx context.Context, uid, peerUID string) error {
	uid, peerUID, err := normalizePair(uid, peerUID)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM friends WHERE (uid = ? AND peer_uid = ?) OR (uid = ? AND peer_uid = ?)`, uid, peerUID, peerUID, uid)
	return err
}

func (s *mysqlStore) RequestFriend(ctx context.Context, fromUID, toUID string) (string, error) {
	fromUID, toUID, err := normalizePair(fromUID, toUID)
	if err != nil {
		return "", err
	}
	blocked, err := s.IsBlocked(ctx, fromUID, toUID)
	if err != nil {
		return "", err
	}
	if blocked {
		return "", errBlocked
	}
	ok, err := s.AreFriends(ctx, fromUID, toUID)
	if err != nil {
		return "", err
	}
	if ok {
		return "friends", nil
	}
	var n int
	err = s.db.QueryRowContext(ctx, `SELECT 1 FROM friend_requests WHERE from_uid = ? AND to_uid = ?`, toUID, fromUID).Scan(&n)
	if err == nil {
		if err := s.AcceptFriend(ctx, toUID, fromUID); err != nil {
			return "", err
		}
		return "friends", nil
	}
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	now := time.Now().UnixMilli()
	_, err = s.db.ExecContext(ctx, `INSERT IGNORE INTO friend_requests (from_uid, to_uid, created_at_ms) VALUES (?, ?, ?)`, fromUID, toUID, now)
	if err != nil {
		return "", err
	}
	return "pending", nil
}

func (s *mysqlStore) AcceptFriend(ctx context.Context, fromUID, toUID string) error {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM friend_requests WHERE from_uid = ? AND to_uid = ?`, fromUID, toUID).Scan(&n)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: no request", errInvalid)
	}
	if err != nil {
		return err
	}
	if _, err := s.AddFriend(ctx, fromUID, toUID); err != nil {
		return err
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM friend_requests WHERE (from_uid = ? AND to_uid = ?) OR (from_uid = ? AND to_uid = ?)`, fromUID, toUID, toUID, fromUID)
	return nil
}

func (s *mysqlStore) DeclineFriend(ctx context.Context, fromUID, toUID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM friend_requests WHERE from_uid = ? AND to_uid = ?`, fromUID, toUID)
	return err
}

func (s *mysqlStore) ListFriendRequests(ctx context.Context, uid string) (incoming, outgoing [][2]string, err error) {
	rows, err := s.db.QueryContext(ctx, `SELECT from_uid, to_uid FROM friend_requests WHERE to_uid = ? OR from_uid = ?`, uid, uid)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var from, to string
		if err := rows.Scan(&from, &to); err != nil {
			return nil, nil, err
		}
		if to == uid {
			incoming = append(incoming, [2]string{from, to})
		} else {
			outgoing = append(outgoing, [2]string{from, to})
		}
	}
	return incoming, outgoing, rows.Err()
}

func (s *mysqlStore) BlockUser(ctx context.Context, uid, peerUID string) error {
	uid, peerUID, err := normalizePair(uid, peerUID)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `INSERT IGNORE INTO blocks (uid, peer_uid, created_at_ms) VALUES (?, ?, ?)`, uid, peerUID, now); err != nil {
		return err
	}
	return s.RemoveFriend(ctx, uid, peerUID)
}

func (s *mysqlStore) UnblockUser(ctx context.Context, uid, peerUID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM blocks WHERE uid = ? AND peer_uid = ?`, uid, peerUID)
	return err
}

func (s *mysqlStore) ListBlocks(ctx context.Context, uid string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT peer_uid FROM blocks WHERE uid = ?`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *mysqlStore) IsBlocked(ctx context.Context, uid, peerUID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM blocks WHERE (uid = ? AND peer_uid = ?) OR (uid = ? AND peer_uid = ?) LIMIT 1`, uid, peerUID, peerUID, uid).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *mysqlStore) SetRemark(ctx context.Context, uid, peerUID, remark string) error {
	uid = strings.TrimSpace(uid)
	peerUID = strings.TrimSpace(peerUID)
	if uid == "" || peerUID == "" {
		return fmt.Errorf("%w: uid required", errInvalid)
	}
	if strings.TrimSpace(remark) == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM friend_remarks WHERE uid = ? AND peer_uid = ?`, uid, peerUID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO friend_remarks (uid, peer_uid, remark) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE remark = VALUES(remark)`, uid, peerUID, clipText(remark, 64))
	return err
}

func (s *mysqlStore) GetRemark(ctx context.Context, uid, peerUID string) (string, error) {
	var remark string
	err := s.db.QueryRowContext(ctx, `SELECT remark FROM friend_remarks WHERE uid = ? AND peer_uid = ?`, uid, peerUID).Scan(&remark)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return remark, err
}

func (s *mysqlStore) LeaveGroup(ctx context.Context, uid, cid string) (*groupInfo, error) {
	g, err := s.loadGroup(ctx, cid)
	if err != nil {
		return nil, err
	}
	if g.OwnerUID == uid {
		return nil, fmt.Errorf("%w: transfer owner first", errInvalid)
	}
	ok, err := s.isMember(ctx, cid, uid)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errNotMember
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM group_members WHERE cid = ? AND uid = ?`, cid, uid); err != nil {
		return nil, err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE conversations SET last_text = '你已退出群聊' WHERE uid = ? AND cid = ?`, uid, cid)
	return s.loadGroup(ctx, cid)
}

func (s *mysqlStore) DissolveGroup(ctx context.Context, uid, cid string) error {
	g, err := s.loadGroup(ctx, cid)
	if err != nil {
		return err
	}
	if g.OwnerUID != uid {
		return errNotOwner
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE conversations SET last_text = '群聊已解散' WHERE cid = ?`, cid)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM group_members WHERE cid = ?`, cid)
	_, err = s.db.ExecContext(ctx, `DELETE FROM im_groups WHERE cid = ?`, cid)
	return err
}

func (s *mysqlStore) TransferOwner(ctx context.Context, operatorUID, cid, memberUID string) (*groupInfo, error) {
	g, err := s.loadGroup(ctx, cid)
	if err != nil {
		return nil, err
	}
	if g.OwnerUID != operatorUID {
		return nil, errNotOwner
	}
	ok, err := s.isMember(ctx, cid, memberUID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errNotMember
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE im_groups SET owner_uid = ? WHERE cid = ?`, memberUID, cid); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE group_members SET role = 'member' WHERE cid = ? AND uid = ?`, cid, operatorUID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE group_members SET role = 'owner' WHERE cid = ? AND uid = ?`, cid, memberUID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.loadGroup(ctx, cid)
}

func (s *mysqlStore) HideConversation(ctx context.Context, uid, cid string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE conversations SET hidden = 1 WHERE uid = ? AND cid = ?`, uid, cid)
	return err
}

func (s *mysqlStore) SetPin(ctx context.Context, uid, cid string, pinned bool) error {
	v := 0
	if pinned {
		v = 1
	}
	_, err := s.db.ExecContext(ctx, `UPDATE conversations SET pinned = ? WHERE uid = ? AND cid = ?`, v, uid, cid)
	return err
}

func (s *mysqlStore) TimelineQuery(ctx context.Context, uid, cid string, afterSeq, beforeSeq uint64, limit int, query string) (string, []*imv1.TimelineMessage, bool, error) {
	if conv.IsGroup(cid) {
		ok, err := s.isMember(ctx, cid, uid)
		if err != nil {
			return "", nil, false, err
		}
		if !ok {
			var n int
			err = s.db.QueryRowContext(ctx, `SELECT 1 FROM conversations WHERE uid = ? AND cid = ? LIMIT 1`, uid, cid).Scan(&n)
			if err == sql.ErrNoRows {
				return "", nil, false, errNotMember
			}
			if err != nil {
				return "", nil, false, err
			}
		}
	} else if _, err := conv.PeerUID(cid, uid); err != nil {
		return "", nil, false, fmt.Errorf("%w: %v", errInvalid, err)
	}
	if limit <= 0 {
		limit = 50
	}
	query = strings.TrimSpace(query)
	var rows *sql.Rows
	var err error
	args := []any{cid}
	sqlStr := `SELECT msg_id, conv_seq, from_uid, payload_type, payload_text, COALESCE(payload_media, ''), created_at_ms, recalled, quote_msg_id FROM messages WHERE cid = ?`
	if afterSeq > 0 {
		sqlStr += ` AND conv_seq > ?`
		args = append(args, afterSeq)
	}
	if beforeSeq > 0 {
		sqlStr += ` AND conv_seq < ?`
		args = append(args, beforeSeq)
	}
	if query != "" {
		sqlStr += ` AND payload_text LIKE ? AND recalled = 0`
		args = append(args, "%"+strings.NewReplacer("%", "", "_", "").Replace(query)+"%")
	}
	if beforeSeq > 0 || afterSeq == 0 {
		sqlStr += ` ORDER BY conv_seq DESC LIMIT ?`
	} else {
		sqlStr += ` ORDER BY conv_seq ASC LIMIT ?`
	}
	args = append(args, limit+1)
	rows, err = s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return "", nil, false, err
	}
	defer rows.Close()
	var out []*imv1.TimelineMessage
	for rows.Next() {
		m := &imv1.TimelineMessage{}
		var ptype int32
		var text, media string
		var recalled int
		if err := rows.Scan(&m.MsgId, &m.ConvSeq, &m.FromUid, &ptype, &text, &media, &m.CreatedAtMs, &recalled, &m.QuoteMsgId); err != nil {
			return "", nil, false, err
		}
		m.Recalled = recalled != 0
		m.Payload = payloadFromCols(ptype, text, media, m.Recalled)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return "", nil, false, err
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	if beforeSeq > 0 || afterSeq == 0 {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	if query == "" && beforeSeq == 0 {
		_, _ = s.db.ExecContext(ctx, `UPDATE conversations SET unread = 0 WHERE uid = ? AND cid = ?`, uid, cid)
	}
	return cid, out, hasMore, nil
}

func (s *mysqlStore) GetReadState(ctx context.Context, uid, cid string, convSeq uint64) (int, int, []string, error) {
	var members []string
	if conv.IsGroup(cid) {
		ids, err := s.GroupMembers(ctx, cid)
		if err != nil {
			return 0, 0, nil, err
		}
		members = ids
	} else {
		peer, err := conv.PeerUID(cid, uid)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("%w: %v", errInvalid, err)
		}
		members = []string{uid, peer}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT uid FROM read_cursors WHERE cid = ? AND conv_seq >= ?`, cid, convSeq)
	if err != nil {
		return 0, 0, nil, err
	}
	defer rows.Close()
	seen := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, 0, nil, err
		}
		seen[id] = struct{}{}
	}
	var readers []string
	for _, m := range members {
		if m == uid {
			continue
		}
		if _, ok := seen[m]; ok {
			readers = append(readers, m)
		}
	}
	return len(readers), len(members), readers, rows.Err()
}
