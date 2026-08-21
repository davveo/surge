package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/davveo/surge/pkg/conv"
)

func (s *mysqlStore) DeleteMessage(ctx context.Context, uid, cid, msgID string) error {
	uid = strings.TrimSpace(uid)
	msgID = strings.TrimSpace(msgID)
	if uid == "" || msgID == "" {
		return fmt.Errorf("%w: uid and msg_id required", errInvalid)
	}
	if cid != "" {
		var n int
		err := s.db.QueryRowContext(ctx, `SELECT 1 FROM messages WHERE msg_id = ? AND cid = ? LIMIT 1`, msgID, cid).Scan(&n)
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: message not found", errInvalid)
		}
		if err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `INSERT IGNORE INTO hidden_messages (uid, msg_id) VALUES (?, ?)`, uid, msgID)
	return err
}

func (s *mysqlStore) ClearConversation(ctx context.Context, uid, cid string) error {
	uid = strings.TrimSpace(uid)
	cid = strings.TrimSpace(cid)
	if uid == "" || cid == "" {
		return fmt.Errorf("%w: uid and cid required", errInvalid)
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE conversations SET cleared_seq = last_conv_seq, unread = 0, last_text = ''
		WHERE uid = ? AND cid = ?`, uid, cid)
	return err
}

func (s *mysqlStore) SetMember(ctx context.Context, operatorUID, cid, memberUID, nickname, role string, muted bool, setNick, setRole, setMuted bool, mutedUntil int64) (*groupInfo, error) {
	g, err := s.loadGroup(ctx, cid)
	if err != nil {
		return nil, err
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
		if _, err := s.db.ExecContext(ctx, `UPDATE group_members SET nickname = ? WHERE cid = ? AND uid = ?`, m.Nickname, cid, memberUID); err != nil {
			return nil, err
		}
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
		default:
			return nil, fmt.Errorf("%w: invalid role", errInvalid)
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE group_members SET role = ? WHERE cid = ? AND uid = ?`, role, cid, memberUID); err != nil {
			return nil, err
		}
	}
	if setMuted {
		if !isManager(g, operatorUID) {
			return nil, errNotAdmin
		}
		if m.Role == "owner" {
			return nil, errNotOwner
		}
		v := 0
		until := int64(0)
		if muted {
			v = 1
			until = mutedUntil
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE group_members SET muted = ?, muted_until_ms = ? WHERE cid = ? AND uid = ?`, v, until, cid, memberUID); err != nil {
			return nil, err
		}
	}
	return s.loadGroup(ctx, cid)
}

func (s *mysqlStore) ListJoinRequests(ctx context.Context, uid, cid string) ([]joinReq, error) {
	g, err := s.loadGroup(ctx, cid)
	if err != nil {
		return nil, err
	}
	if !isManager(g, uid) {
		return nil, errNotAdmin
	}
	rows, err := s.db.QueryContext(ctx, `SELECT uid, from_uid, created_at_ms FROM group_join_requests WHERE cid = ? ORDER BY created_at_ms`, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []joinReq
	for rows.Next() {
		var r joinReq
		if err := rows.Scan(&r.UID, &r.FromUID, &r.CreatedAtMs); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *mysqlStore) RequestJoin(ctx context.Context, uid, cid string) (*groupInfo, error) {
	uid = strings.TrimSpace(uid)
	cid = strings.TrimSpace(cid)
	if uid == "" || cid == "" {
		return nil, fmt.Errorf("%w: uid and cid required", errInvalid)
	}
	g, err := s.loadGroup(ctx, cid)
	if err != nil {
		return nil, err
	}
	if memberOf(g, uid) != nil {
		return g, nil
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO group_join_requests (cid, uid, from_uid, created_at_ms) VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE created_at_ms = VALUES(created_at_ms)`,
		cid, uid, uid, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	return g, nil
}

func (s *mysqlStore) DecideJoin(ctx context.Context, operatorUID, cid, memberUID string, accept bool) (*groupInfo, error) {
	g, err := s.loadGroup(ctx, cid)
	if err != nil {
		return nil, err
	}
	if !isManager(g, operatorUID) {
		return nil, errNotAdmin
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM group_join_requests WHERE cid = ? AND uid = ?`, cid, memberUID)
	if !accept {
		return g, nil
	}
	if memberOf(g, memberUID) != nil {
		return g, nil
	}
	if len(g.Members)+1 > maxGroupMembers {
		return nil, errTooLarge
	}
	now := time.Now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO group_members (cid, uid, role, joined_at_ms) VALUES (?, ?, 'member', ?)`,
		cid, memberUID, now); err != nil {
		return nil, err
	}
	if err := upsertConv(ctx, tx, memberUID, cid, "", g.Name, conv.KindGroup, "", 0, "加入群聊", now, true); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.loadGroup(ctx, cid)
}
