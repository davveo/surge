package main

import (
	"context"
	"fmt"
	"strings"
)

func (s *mysqlStore) ResetPassword(ctx context.Context, uid, newPassword string) error {
	if err := validUID(uid); err != nil {
		return err
	}
	newPassword = strings.TrimSpace(newPassword)
	if len(newPassword) < 6 {
		return fmt.Errorf("%w: password too short", errInvalid)
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE uid = ?`, hash, uid)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: user not found", errInvalid)
	}
	return nil
}

func (s *mysqlStore) DeleteAccount(ctx context.Context, uid string) error {
	if err := validUID(uid); err != nil {
		return err
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM friends WHERE uid = ? OR peer_uid = ?`, uid, uid)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM conversations WHERE uid = ?`, uid)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM user_settings WHERE uid = ?`, uid)
	_, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = '', display_name = '已注销', email = '', phone = '', avatar_url = '' WHERE uid = ?`, uid)
	return err
}

func (s *mysqlStore) RevokeGroupInvite(ctx context.Context, uid, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("%w: token required", errInvalid)
	}
	var cid string
	err := s.db.QueryRowContext(ctx, `SELECT cid FROM group_invites WHERE token = ?`, token).Scan(&cid)
	if err != nil {
		return fmt.Errorf("%w: invite not found", errInvalid)
	}
	g, err := s.loadGroup(ctx, cid)
	if err != nil {
		return err
	}
	if !isManager(g, uid) {
		return errNotAdmin
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM group_invites WHERE token = ?`, token)
	return err
}

func (s *mysqlStore) DeleteMemberMessages(ctx context.Context, cid, memberUID string) error {
	rows, err := s.db.QueryContext(ctx, `SELECT msg_id FROM messages WHERE cid = ? AND from_uid = ?`, cid, memberUID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		if _, err := s.db.ExecContext(ctx, `INSERT IGNORE INTO hidden_messages (uid, msg_id) SELECT uid, ? FROM group_members WHERE cid = ?`, id, cid); err != nil {
			return err
		}
	}
	return nil
}

func (s *mysqlStore) PatchGroup(ctx context.Context, operatorUID, cid, mode string, historyDays int32, announceAck bool, setMode, setHistory, setAck bool) (*groupInfo, error) {
	g, err := s.loadGroup(ctx, cid)
	if err != nil {
		return nil, err
	}
	if !isOwner(g, operatorUID) {
		return nil, errNotOwner
	}
	if setMode {
		applyGroupMode(g, mode)
		join, muted := 0, 0
		if g.JoinApproval {
			join = 1
		}
		if g.MutedAll {
			muted = 1
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE im_groups SET mode = ?, join_approval = ?, muted_all = ? WHERE cid = ?`, g.Mode, join, muted, cid); err != nil {
			return nil, err
		}
	}
	if setHistory {
		if historyDays < 0 {
			historyDays = 0
		}
		g.HistoryDays = historyDays
		if _, err := s.db.ExecContext(ctx, `UPDATE im_groups SET history_days = ? WHERE cid = ?`, historyDays, cid); err != nil {
			return nil, err
		}
	}
	if setAck {
		g.AnnounceAck = announceAck
		v := 0
		if announceAck {
			v = 1
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE im_groups SET announce_ack = ? WHERE cid = ?`, v, cid); err != nil {
			return nil, err
		}
	}
	return s.loadGroup(ctx, cid)
}
