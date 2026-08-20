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

func (s *mysqlStore) loadGroup(ctx context.Context, cid string) (*groupInfo, error) {
	g := &groupInfo{CID: cid}
	err := s.db.QueryRowContext(ctx, `SELECT name, owner_uid FROM groups WHERE cid = ?`, cid).Scan(&g.Name, &g.OwnerUID)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: group not found", errInvalid)
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT uid, role FROM group_members WHERE cid = ?`, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m groupMember
		if err := rows.Scan(&m.UID, &m.Role); err != nil {
			return nil, err
		}
		g.Members = append(g.Members, m)
	}
	return g, rows.Err()
}

func (s *mysqlStore) isMember(ctx context.Context, cid, uid string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM group_members WHERE cid = ? AND uid = ? LIMIT 1`, cid, uid).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *mysqlStore) CreateGroup(ctx context.Context, ownerUID, name string, memberUIDs []string) (*groupInfo, error) {
	ownerUID = strings.TrimSpace(ownerUID)
	name = strings.TrimSpace(name)
	if ownerUID == "" || name == "" {
		return nil, fmt.Errorf("%w: owner and name required", errInvalid)
	}
	members := uniqueUIDs(append([]string{ownerUID}, memberUIDs...))
	if len(members) > maxGroupMembers {
		return nil, errTooLarge
	}
	for _, uid := range members {
		if uid == ownerUID {
			continue
		}
		ok, err := s.AreFriends(ctx, ownerUID, uid)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("%w: add friend first", errNotFriends)
		}
	}
	cid := conv.GroupPrefix() + uuid.NewString()
	now := time.Now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO groups (cid, name, owner_uid, created_at_ms) VALUES (?, ?, ?, ?)`,
		cid, name, ownerUID, now); err != nil {
		return nil, err
	}
	g := &groupInfo{CID: cid, Name: name, OwnerUID: ownerUID}
	for _, uid := range members {
		role := "member"
		if uid == ownerUID {
			role = "owner"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO group_members (cid, uid, role, joined_at_ms) VALUES (?, ?, ?, ?)`,
			cid, uid, role, now); err != nil {
			return nil, err
		}
		if err := upsertConv(ctx, tx, uid, cid, "", name, conv.KindGroup, "", 0, "群聊已创建", now, false); err != nil {
			return nil, err
		}
		g.Members = append(g.Members, groupMember{UID: uid, Role: role})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return g, nil
}

func (s *mysqlStore) InviteGroup(ctx context.Context, operatorUID, cid string, memberUIDs []string) (*groupInfo, error) {
	ok, err := s.isMember(ctx, cid, operatorUID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errNotMember
	}
	g, err := s.loadGroup(ctx, cid)
	if err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	existing := map[string]struct{}{}
	for _, m := range g.Members {
		existing[m.UID] = struct{}{}
	}
	for _, uid := range uniqueUIDs(memberUIDs) {
		if _, ok := existing[uid]; ok {
			continue
		}
		ok, err := s.AreFriends(ctx, operatorUID, uid)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("%w: add friend first", errNotFriends)
		}
		if len(existing)+1 > maxGroupMembers {
			return nil, errTooLarge
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO group_members (cid, uid, role, joined_at_ms) VALUES (?, ?, 'member', ?)`,
			cid, uid, now); err != nil {
			return nil, err
		}
		if err := upsertConv(ctx, tx, uid, cid, "", g.Name, conv.KindGroup, "", 0, "加入群聊", now, false); err != nil {
			return nil, err
		}
		existing[uid] = struct{}{}
		g.Members = append(g.Members, groupMember{UID: uid, Role: "member"})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return g, nil
}

func (s *mysqlStore) KickGroup(ctx context.Context, operatorUID, cid, memberUID string) (*groupInfo, error) {
	g, err := s.loadGroup(ctx, cid)
	if err != nil {
		return nil, err
	}
	if g.OwnerUID != operatorUID {
		return nil, errNotOwner
	}
	if memberUID == operatorUID {
		return nil, fmt.Errorf("%w: cannot kick owner", errInvalid)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM group_members WHERE cid = ? AND uid = ?`, cid, memberUID); err != nil {
		return nil, err
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM conversations WHERE uid = ? AND cid = ?`, memberUID, cid)
	return s.loadGroup(ctx, cid)
}

func (s *mysqlStore) GetGroup(ctx context.Context, uid, cid string) (*groupInfo, error) {
	ok, err := s.isMember(ctx, cid, uid)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errNotMember
	}
	return s.loadGroup(ctx, cid)
}

func (s *mysqlStore) GroupMembers(ctx context.Context, cid string) ([]string, error) {
	g, err := s.loadGroup(ctx, cid)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range g.Members {
		out = append(out, m.UID)
	}
	return out, nil
}

func (s *mysqlStore) Recall(ctx context.Context, uid, cid, msgID string) (*imv1.RecallNotify, []string, error) {
	var fromUID string
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT from_uid, created_at_ms FROM messages WHERE msg_id = ? AND cid = ?`, msgID, cid).
		Scan(&fromUID, &created)
	if err == sql.ErrNoRows {
		return nil, nil, fmt.Errorf("%w: message not found", errInvalid)
	}
	if err != nil {
		return nil, nil, err
	}
	if fromUID != uid {
		return nil, nil, fmt.Errorf("%w: only sender can recall", errInvalid)
	}
	if time.Now().UnixMilli()-created > recallWindowMS {
		return nil, nil, fmt.Errorf("%w: recall window exceeded", errInvalid)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE messages SET recalled = 1, payload_type = ?, payload_text = '' WHERE msg_id = ?`,
		int32(imv1.Payload_RECALL), msgID); err != nil {
		return nil, nil, err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE conversations SET last_text = '已撤回一条消息' WHERE cid = ? AND last_msg_id = ?`, cid, msgID)
	var members []string
	if conv.IsGroup(cid) {
		_, _, members, err = s.targets(ctx, uid, cid, "")
		if err != nil {
			return nil, nil, err
		}
	} else {
		peer, err := conv.PeerUID(cid, uid)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", errInvalid, err)
		}
		members = []string{uid, peer}
	}
	return &imv1.RecallNotify{Cid: cid, MsgId: msgID, FromUid: uid}, members, nil
}

func (s *mysqlStore) MarkRead(ctx context.Context, uid, cid string, convSeq uint64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO read_cursors (uid, cid, conv_seq) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE conv_seq = GREATEST(conv_seq, VALUES(conv_seq))`, uid, cid, convSeq)
	if err != nil {
		return err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE conversations SET unread = 0 WHERE uid = ? AND cid = ?`, uid, cid)
	return nil
}
