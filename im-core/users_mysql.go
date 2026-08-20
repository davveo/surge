package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	imv1 "github.com/davveo/surge/proto/gen/im/v1"
	"golang.org/x/crypto/bcrypt"
)

func scanUser(row interface{ Scan(dest ...any) error }) (*userRec, error) {
	u := &userRec{}
	var email, phone, pub sql.NullString
	if err := row.Scan(&u.UID, &u.PasswordHash, &u.DisplayName, &u.AvatarURL, &email, &phone, &pub); err != nil {
		return nil, err
	}
	u.Email = email.String
	u.Phone = phone.String
	u.PublicKey = pub.String
	return u, nil
}

const userCols = `uid, password_hash, display_name, avatar_url, IFNULL(email,''), IFNULL(phone,''), IFNULL(public_key,'')`

func (s *mysqlStore) loadUser(ctx context.Context, uid string) (*userRec, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE uid = ?`, uid))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (s *mysqlStore) EnsureUser(ctx context.Context, uid string) error {
	if err := validUID(uid); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (uid, password_hash, display_name, avatar_url, created_at_ms)
		VALUES (?, '', ?, '', ?)
		ON DUPLICATE KEY UPDATE uid = uid`, uid, uid, now)
	return err
}

func (s *mysqlStore) Register(ctx context.Context, uid, password string) (*imv1.UserProfile, error) {
	if err := validUID(uid); err != nil {
		return nil, err
	}
	password = strings.TrimSpace(password)
	if password == "" {
		return nil, fmt.Errorf("%w: password required", errInvalid)
	}
	if len(password) < 6 {
		return nil, fmt.Errorf("%w: password too short", errInvalid)
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	cur, err := s.loadUser(ctx, uid)
	if err != nil {
		return nil, err
	}
	if cur != nil && cur.PasswordHash != "" {
		return nil, fmt.Errorf("%w: already registered", errInvalid)
	}
	now := time.Now().UnixMilli()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO users (uid, password_hash, display_name, avatar_url, created_at_ms)
		VALUES (?, ?, ?, '', ?)
		ON DUPLICATE KEY UPDATE password_hash = IF(password_hash='', VALUES(password_hash), password_hash)`,
		uid, hash, uid, now)
	if err != nil {
		return nil, err
	}
	u, err := s.loadUser(ctx, uid)
	if err != nil {
		return nil, err
	}
	return profileOf(u), nil
}

func (s *mysqlStore) VerifyPassword(ctx context.Context, uid, password string) (*imv1.UserProfile, error) {
	if err := validUID(uid); err != nil {
		return nil, err
	}
	u, err := s.loadUser(ctx, uid)
	if err != nil {
		return nil, err
	}
	if u == nil || u.PasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, fmt.Errorf("%w: bad credentials", errAuth)
	}
	return profileOf(u), nil
}

func (s *mysqlStore) GetProfile(ctx context.Context, uid string) (*imv1.UserProfile, error) {
	if err := validUID(uid); err != nil {
		return nil, err
	}
	u, err := s.loadUser(ctx, uid)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, fmt.Errorf("%w: user not found", errInvalid)
	}
	return profileOf(u), nil
}

func (s *mysqlStore) UpdateProfile(ctx context.Context, uid, displayName, avatarURL string) (*imv1.UserProfile, error) {
	if err := s.EnsureUser(ctx, uid); err != nil {
		return nil, err
	}
	sets := []string{}
	args := []any{}
	if n := strings.TrimSpace(displayName); n != "" {
		sets = append(sets, "display_name = ?")
		args = append(args, clipText(n, 64))
	}
	if a := strings.TrimSpace(avatarURL); a != "" {
		sets = append(sets, "avatar_url = ?")
		args = append(args, clipText(a, 512))
	}
	if len(sets) > 0 {
		args = append(args, uid)
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET `+strings.Join(sets, ", ")+` WHERE uid = ?`, args...); err != nil {
			return nil, err
		}
	}
	return s.GetProfile(ctx, uid)
}

func (s *mysqlStore) SearchUsers(ctx context.Context, query string, limit int) ([]*imv1.UserProfile, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("%w: query required", errInvalid)
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	like := strings.NewReplacer("%", "", "_", "").Replace(query) + "%"
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+userCols+` FROM users
		WHERE uid LIKE ? OR display_name LIKE ? OR email LIKE ? OR phone LIKE ?
		ORDER BY uid ASC LIMIT ?`, like, "%"+strings.TrimSuffix(like, "%")+"%", "%"+strings.TrimSuffix(like, "%")+"%", "%"+strings.TrimSuffix(like, "%")+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*imv1.UserProfile
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, profileOf(u))
	}
	return out, rows.Err()
}

func (s *mysqlStore) UpdateGroup(ctx context.Context, operatorUID, cid, name, avatarURL, announcement string, setAnnouncement bool, joinApproval *bool) (*groupInfo, error) {
	g, err := s.loadGroup(ctx, cid)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	avatarURL = strings.TrimSpace(avatarURL)
	if avatarURL != "" && !isOwner(g, operatorUID) {
		return nil, errNotOwner
	}
	if !isManager(g, operatorUID) {
		return nil, errNotAdmin
	}
	if name != "" {
		g.Name = clipText(name, 128)
	}
	if avatarURL != "" {
		g.AvatarURL = clipText(avatarURL, 512)
	}
	if setAnnouncement {
		if !isOwner(g, operatorUID) {
			return nil, errNotOwner
		}
		g.Announcement = clipText(announcement, 2000)
	}
	if joinApproval != nil {
		if g.OwnerUID != operatorUID {
			return nil, errNotOwner
		}
		g.JoinApproval = *joinApproval
	}
	join := 0
	if g.JoinApproval {
		join = 1
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE im_groups SET name = ?, avatar_url = ?, announcement = ?, join_approval = ? WHERE cid = ?`,
		g.Name, g.AvatarURL, g.Announcement, join, cid); err != nil {
		return nil, err
	}
	if name != "" {
		_, _ = s.db.ExecContext(ctx, `UPDATE conversations SET title = ? WHERE cid = ?`, g.Name, cid)
	}
	return s.loadGroup(ctx, cid)
}

func (s *mysqlStore) SetMute(ctx context.Context, uid, cid string, muted bool) error {
	uid = strings.TrimSpace(uid)
	cid = strings.TrimSpace(cid)
	if uid == "" || cid == "" {
		return fmt.Errorf("%w: uid and cid required", errInvalid)
	}
	if muted {
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO conv_mutes (uid, cid, muted) VALUES (?, ?, 1)
			ON DUPLICATE KEY UPDATE muted = 1`, uid, cid)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM conv_mutes WHERE uid = ? AND cid = ?`, uid, cid)
	return err
}

func (s *mysqlStore) ListMutes(ctx context.Context, uid string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT cid FROM conv_mutes WHERE uid = ? AND muted = 1`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var cid string
		if err := rows.Scan(&cid); err != nil {
			return nil, err
		}
		out = append(out, cid)
	}
	return out, rows.Err()
}

func (s *mysqlStore) lookupQuoteText(ctx context.Context, cid, quoteMsgID string) string {
	if quoteMsgID == "" {
		return ""
	}
	var ptype int32
	var text string
	err := s.db.QueryRowContext(ctx, `SELECT payload_type, payload_text FROM messages WHERE msg_id = ? AND cid = ?`, quoteMsgID, cid).
		Scan(&ptype, &text)
	if err != nil {
		return ""
	}
	return previewOf(&imv1.Payload{Type: imv1.Payload_Type(ptype), Text: text})
}
