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
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
  id INT NOT NULL PRIMARY KEY,
  name VARCHAR(128) NOT NULL,
  applied_at_ms BIGINT NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		return fmt.Errorf("migrate schema_migrations: %w", err)
	}
	applied, err := loadAppliedMigrations(db)
	if err != nil {
		return err
	}
	for _, p := range schemaPatches {
		if applied[p.id] {
			continue
		}
		if _, err := db.Exec(p.sql); err != nil && !isIdempotentSchemaErr(err) {
			return fmt.Errorf("migrate %d %s: %w", p.id, p.name, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (id, name, applied_at_ms) VALUES (?, ?, ?)`,
			p.id, p.name, time.Now().UnixMilli()); err != nil {
			return fmt.Errorf("migrate stamp %d: %w", p.id, err)
		}
	}
	return seedSeqs(db)
}

type schemaPatch struct {
	id   int
	name string
	sql  string
}

func loadAppliedMigrations(db *sql.DB) (map[int]bool, error) {
	rows, err := db.Query(`SELECT id FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func isIdempotentSchemaErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Duplicate column") ||
		strings.Contains(msg, "already exists") ||
		strings.Contains(msg, "Duplicate key name")
}

var schemaPatches = []schemaPatch{
	{1, "conversations.title", `ALTER TABLE conversations ADD COLUMN title VARCHAR(128) NOT NULL DEFAULT ''`},
	{2, "conversations.kind", `ALTER TABLE conversations ADD COLUMN kind VARCHAR(16) NOT NULL DEFAULT 'p2p'`},
	{3, "messages.recalled", `ALTER TABLE messages ADD COLUMN recalled TINYINT NOT NULL DEFAULT 0`},
	{4, "messages.quote_msg_id", `ALTER TABLE messages ADD COLUMN quote_msg_id VARCHAR(36) NOT NULL DEFAULT ''`},
	{5, "messages.payload_media", `ALTER TABLE messages ADD COLUMN payload_media TEXT`},
	{6, "im_groups.avatar_url", `ALTER TABLE im_groups ADD COLUMN avatar_url VARCHAR(512) NOT NULL DEFAULT ''`},
	{7, "conversations.hidden", `ALTER TABLE conversations ADD COLUMN hidden TINYINT NOT NULL DEFAULT 0`},
	{8, "conversations.pinned", `ALTER TABLE conversations ADD COLUMN pinned TINYINT NOT NULL DEFAULT 0`},
	{9, "users.email", `ALTER TABLE users ADD COLUMN email VARCHAR(128) NOT NULL DEFAULT ''`},
	{10, "users.phone", `ALTER TABLE users ADD COLUMN phone VARCHAR(32) NOT NULL DEFAULT ''`},
	{11, "users.public_key", `ALTER TABLE users ADD COLUMN public_key TEXT`},
	{12, "im_groups.muted_all", `ALTER TABLE im_groups ADD COLUMN muted_all TINYINT NOT NULL DEFAULT 0`},
	{13, "im_groups.announcement", `ALTER TABLE im_groups ADD COLUMN announcement TEXT`},
	{14, "im_groups.join_approval", `ALTER TABLE im_groups ADD COLUMN join_approval TINYINT NOT NULL DEFAULT 0`},
	{15, "group_members.nickname", `ALTER TABLE group_members ADD COLUMN nickname VARCHAR(64) NOT NULL DEFAULT ''`},
	{16, "group_members.muted", `ALTER TABLE group_members ADD COLUMN muted TINYINT NOT NULL DEFAULT 0`},
	{17, "group_members.muted_until_ms", `ALTER TABLE group_members ADD COLUMN muted_until_ms BIGINT NOT NULL DEFAULT 0`},
	{18, "group_members.joined_at_ms", `ALTER TABLE group_members ADD COLUMN joined_at_ms BIGINT NOT NULL DEFAULT 0`},
	{19, "im_groups.history_days", `ALTER TABLE im_groups ADD COLUMN history_days INT NOT NULL DEFAULT 0`},
	{20, "im_groups.announce_ack", `ALTER TABLE im_groups ADD COLUMN announce_ack TINYINT NOT NULL DEFAULT 0`},
	{21, "user_settings.notify_at_muted", `ALTER TABLE user_settings ADD COLUMN notify_at_muted TINYINT NOT NULL DEFAULT 1`},
	{22, "user_settings.add_me", `ALTER TABLE user_settings ADD COLUMN add_me VARCHAR(16) NOT NULL DEFAULT 'verify'`},
	{23, "user_settings.hide_read", `ALTER TABLE user_settings ADD COLUMN hide_read TINYINT NOT NULL DEFAULT 0`},
	{24, "user_settings.hide_typing", `ALTER TABLE user_settings ADD COLUMN hide_typing TINYINT NOT NULL DEFAULT 0`},
	{25, "user_settings.hide_last_seen", `ALTER TABLE user_settings ADD COLUMN hide_last_seen TINYINT NOT NULL DEFAULT 0`},
	{26, "user_settings.burn_sec", `ALTER TABLE user_settings ADD COLUMN burn_sec INT NOT NULL DEFAULT 5`},
	{27, "user_settings.wallpaper", `ALTER TABLE user_settings MODIFY wallpaper VARCHAR(512) NOT NULL DEFAULT ''`},
	{28, "conversations.cleared_seq", `ALTER TABLE conversations ADD COLUMN cleared_seq BIGINT UNSIGNED NOT NULL DEFAULT 0`},
	{29, "im_groups.mode", `ALTER TABLE im_groups ADD COLUMN mode VARCHAR(32) NOT NULL DEFAULT 'normal'`},
	{30, "friend_requests.hello", `ALTER TABLE friend_requests ADD COLUMN hello VARCHAR(200) NOT NULL DEFAULT ''`},
	{31, "friend_requests.source", `ALTER TABLE friend_requests ADD COLUMN source VARCHAR(64) NOT NULL DEFAULT ''`},
	{32, "conversations.unread_mention", `ALTER TABLE conversations ADD COLUMN unread_mention INT UNSIGNED NOT NULL DEFAULT 0`},
	{33, "conversations.draft_text", `ALTER TABLE conversations ADD COLUMN draft_text TEXT`},
	{34, "hidden_messages", `CREATE TABLE IF NOT EXISTS hidden_messages (
  uid VARCHAR(64) NOT NULL,
  msg_id CHAR(36) NOT NULL,
  PRIMARY KEY (uid, msg_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`},
	{35, "group_join_requests", `CREATE TABLE IF NOT EXISTS group_join_requests (
  cid VARCHAR(128) NOT NULL,
  uid VARCHAR(64) NOT NULL,
  from_uid VARCHAR(64) NOT NULL,
  created_at_ms BIGINT NOT NULL,
  PRIMARY KEY (cid, uid),
  KEY idx_cid (cid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`},
	{36, "message_reactions", `CREATE TABLE IF NOT EXISTS message_reactions (
  msg_id CHAR(36) NOT NULL,
  uid VARCHAR(64) NOT NULL,
  emoji VARCHAR(16) NOT NULL,
  created_at_ms BIGINT NOT NULL,
  PRIMARY KEY (msg_id, uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`},
	{37, "favorites", `CREATE TABLE IF NOT EXISTS favorites (
  fav_id CHAR(36) NOT NULL,
  uid VARCHAR(64) NOT NULL,
  cid VARCHAR(128) NOT NULL,
  msg_id CHAR(36) NOT NULL,
  from_uid VARCHAR(64) NOT NULL,
  preview VARCHAR(512) NOT NULL,
  payload_json TEXT NOT NULL,
  created_at_ms BIGINT NOT NULL,
  PRIMARY KEY (fav_id),
  UNIQUE KEY uk_uid_msg (uid, msg_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`},
	{38, "group_invites", `CREATE TABLE IF NOT EXISTS group_invites (
  token VARCHAR(32) NOT NULL,
  cid VARCHAR(128) NOT NULL,
  from_uid VARCHAR(64) NOT NULL,
  created_at_ms BIGINT NOT NULL,
  expires_at_ms BIGINT NOT NULL,
  PRIMARY KEY (token)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`},
	{39, "chat_pins", `CREATE TABLE IF NOT EXISTS chat_pins (
  cid VARCHAR(128) NOT NULL,
  msg_id CHAR(36) NOT NULL,
  from_uid VARCHAR(64) NOT NULL,
  preview VARCHAR(512) NOT NULL,
  created_at_ms BIGINT NOT NULL,
  PRIMARY KEY (cid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`},
	{40, "reports", `CREATE TABLE IF NOT EXISTS reports (
  id CHAR(36) NOT NULL,
  uid VARCHAR(64) NOT NULL,
  cid VARCHAR(128) NOT NULL,
  msg_id CHAR(36) NOT NULL,
  reason VARCHAR(256) NOT NULL,
  created_at_ms BIGINT NOT NULL,
  PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`},
	{41, "user_settings", `CREATE TABLE IF NOT EXISTS user_settings (
  uid VARCHAR(64) NOT NULL,
  dark TINYINT NOT NULL DEFAULT 0,
  wallpaper VARCHAR(64) NOT NULL DEFAULT '',
  notify_sound TINYINT NOT NULL DEFAULT 1,
  notify_preview TINYINT NOT NULL DEFAULT 1,
  dnd_start VARCHAR(8) NOT NULL DEFAULT '',
  dnd_end VARCHAR(8) NOT NULL DEFAULT '',
  PRIMARY KEY (uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`},
}

func (s *mysqlStore) Send(ctx context.Context, fromUID, clientMsgID, cid, peerUID string, payload *imv1.Payload, quoteMsgID string) (*sendResult, error) {
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

	title, kind, members, err := s.targets(ctx, fromUID, canonical, peer)
	if err != nil {
		return nil, err
	}
	if conv.IsGroup(canonical) {
		if g, gerr := s.loadGroup(ctx, canonical); gerr == nil {
			applyEphemeralMode(g, payload)
		}
	}

	now := time.Now().UnixMilli()
	msgID := uuid.NewString()
	payload = enrichPayload(payload, s.lookupQuoteText(ctx, canonical, quoteMsgID))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	members = uniqueUIDs(members)
	seqs, err := bumpSeqs(ctx, tx, seqKeys(canonical, members))
	if err != nil {
		return nil, err
	}
	convSeq := seqs[convSeqKey(canonical)]
	if convSeq == 0 {
		return nil, fmt.Errorf("seq missing conv %s", canonical)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO messages (msg_id, client_msg_id, cid, conv_seq, from_uid, payload_type, payload_text, payload_media, created_at_ms, recalled, quote_msg_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		msgID, clientMsgID, canonical, convSeq, fromUID, int32(payload.Type), payload.Text, marshalPayloadBlob(payload), now, quoteMsgID)
	if err != nil {
		if isDupErr(err) {
			return s.loadDup(ctx, fromUID, clientMsgID, canonical)
		}
		return nil, fmt.Errorf("insert message: %w", err)
	}

	preview := previewOf(payload)
	inboxes := make([]inboxRow, 0, len(members))
	convs := make([]convUpsert, 0, len(members))
	var deliveries []delivery
	var senderSync uint64
	for _, uid := range members {
		syncSeq := seqs[syncSeqKey(uid)]
		if syncSeq == 0 {
			return nil, fmt.Errorf("seq missing sync %s", uid)
		}
		if uid == fromUID {
			senderSync = syncSeq
		}
		peerLabel := peer
		if kind == conv.KindGroup {
			peerLabel = ""
		} else if uid != fromUID {
			peerLabel = fromUID
		}
		unreadInc, mentionInc := 0, 0
		if uid != fromUID {
			unreadInc = 1
			if payloadMentions(payload, uid) {
				mentionInc = 1
			}
			deliveries = append(deliveries, delivery{uid: uid, push: &imv1.Push{
				Cid: canonical, MsgId: msgID, ConvSeq: convSeq, SyncSeq: syncSeq,
				FromUid: fromUID, Payload: payload, CreatedAtMs: now,
			}})
		}
		inboxes = append(inboxes, inboxRow{
			uid: uid, syncSeq: syncSeq, cid: canonical, msgID: msgID,
			convSeq: convSeq, fromUID: fromUID, now: now,
		})
		convs = append(convs, convUpsert{
			uid: uid, cid: canonical, peer: peerLabel, title: title, kind: kind,
			msgID: msgID, convSeq: convSeq, text: preview, now: now,
			unreadInc: unreadInc, mentionInc: mentionInc,
		})
	}
	if err := insertInboxBatch(ctx, tx, inboxes); err != nil {
		return nil, err
	}
	if err := upsertConvBatch(ctx, tx, convs); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		if isDupErr(err) {
			return s.loadDup(ctx, fromUID, clientMsgID, canonical)
		}
		return nil, err
	}
	res := &sendResult{
		ack: &imv1.Ack{
			ClientMsgId: clientMsgID, MsgId: msgID, Cid: canonical,
			ConvSeq: convSeq, SyncSeq: senderSync, CreatedAtMs: now,
		},
		peerUID:    peer,
		deliveries: deliveries,
	}
	if len(deliveries) > 0 {
		res.peerPush = deliveries[0].push
	}
	return res, nil
}

func (s *mysqlStore) targets(ctx context.Context, fromUID, cid, peer string) (title, kind string, members []string, err error) {
	if conv.IsGroup(cid) {
		g, err := s.loadGroup(ctx, cid)
		if err != nil {
			return "", "", nil, err
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

type inboxRow struct {
	uid, cid, msgID, fromUID string
	syncSeq, convSeq         uint64
	now                      int64
}

type convUpsert struct {
	uid, cid, peer, title, kind, msgID, text string
	convSeq                                  uint64
	now                                      int64
	unreadInc, mentionInc                    int
}

const sqlWriteBatch = 80

func insertInbox(ctx context.Context, tx *sql.Tx, uid string, syncSeq uint64, cid, msgID string, convSeq uint64, fromUID string, now int64) error {
	return insertInboxBatch(ctx, tx, []inboxRow{{
		uid: uid, syncSeq: syncSeq, cid: cid, msgID: msgID, convSeq: convSeq, fromUID: fromUID, now: now,
	}})
}

func insertInboxBatch(ctx context.Context, tx *sql.Tx, rows []inboxRow) error {
	if len(rows) == 0 {
		return nil
	}
	for i := 0; i < len(rows); i += sqlWriteBatch {
		j := i + sqlWriteBatch
		if j > len(rows) {
			j = len(rows)
		}
		chunk := rows[i:j]
		var sb strings.Builder
		args := make([]any, 0, len(chunk)*7)
		sb.WriteString(`INSERT INTO inbox (uid, sync_seq, cid, msg_id, conv_seq, from_uid, created_at_ms) VALUES `)
		for k, row := range chunk {
			if k > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(`(?,?,?,?,?,?,?)`)
			args = append(args, row.uid, row.syncSeq, row.cid, row.msgID, row.convSeq, row.fromUID, row.now)
		}
		if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
			return fmt.Errorf("insert inbox: %w", err)
		}
	}
	return nil
}

func upsertConv(ctx context.Context, tx *sql.Tx, uid, cid, peer, title, kind, msgID string, convSeq uint64, text string, now int64, incoming bool) error {
	unreadInc := 0
	if incoming {
		unreadInc = 1
	}
	return upsertConvBatch(ctx, tx, []convUpsert{{
		uid: uid, cid: cid, peer: peer, title: title, kind: kind, msgID: msgID,
		convSeq: convSeq, text: text, now: now, unreadInc: unreadInc,
	}})
}

func upsertConvBatch(ctx context.Context, tx *sql.Tx, rows []convUpsert) error {
	if len(rows) == 0 {
		return nil
	}
	for i := 0; i < len(rows); i += sqlWriteBatch {
		j := i + sqlWriteBatch
		if j > len(rows) {
			j = len(rows)
		}
		chunk := rows[i:j]
		var sb strings.Builder
		args := make([]any, 0, len(chunk)*12)
		sb.WriteString(`INSERT INTO conversations (uid, cid, peer_uid, last_msg_id, last_conv_seq, last_text, unread, unread_mention, updated_at_ms, title, kind)
VALUES `)
		for k, row := range chunk {
			if k > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(`(?,?,?,?,?,?,?,?,?,?,?)`)
			args = append(args, row.uid, row.cid, row.peer, row.msgID, row.convSeq, row.text, row.unreadInc, row.mentionInc, row.now, row.title, row.kind)
		}
		sb.WriteString(` ON DUPLICATE KEY UPDATE
			last_msg_id = VALUES(last_msg_id),
			last_conv_seq = VALUES(last_conv_seq),
			last_text = VALUES(last_text),
			unread = unread + VALUES(unread),
			unread_mention = unread_mention + VALUES(unread_mention),
			updated_at_ms = VALUES(updated_at_ms),
			title = IF(VALUES(title)='', title, VALUES(title)),
			kind = IF(VALUES(kind)='', kind, VALUES(kind)),
			hidden = 0`)
		if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
			return fmt.Errorf("upsert conversation: %w", err)
		}
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
		SELECT i.sync_seq, i.cid, i.msg_id, i.conv_seq, i.from_uid, i.created_at_ms, m.payload_type, m.payload_text, COALESCE(m.payload_media, '')
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
		ev := &imv1.InboxEvent{}
		var ptype int32
		var text, media string
		if err := rows.Scan(&ev.SyncSeq, &ev.Cid, &ev.MsgId, &ev.ConvSeq, &ev.FromUid, &ev.CreatedAtMs, &ptype, &text, &media); err != nil {
			return nil, err
		}
		ev.Payload = payloadFromCols(ptype, text, media, false)
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
		SELECT c.cid, c.peer_uid, c.last_msg_id, c.last_conv_seq, c.unread, c.updated_at_ms, c.last_text, c.title, c.kind,
			IFNULL(m.muted, 0), IFNULL(c.pinned, 0), IFNULL(g.avatar_url, ''), IFNULL(g.mode, ''), IFNULL(c.unread_mention, 0), IFNULL(c.draft_text, '')
		FROM conversations c
		LEFT JOIN conv_mutes m ON m.uid = c.uid AND m.cid = c.cid
		LEFT JOIN im_groups g ON g.cid = c.cid
		WHERE c.uid = ? AND IFNULL(c.hidden, 0) = 0
		ORDER BY c.pinned DESC, c.updated_at_ms DESC`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*imv1.Conversation
	for rows.Next() {
		c := &imv1.Conversation{}
		var muted, pinned int
		var groupAvatar, groupMode string
		if err := rows.Scan(&c.Cid, &c.PeerUid, &c.LastMsgId, &c.LastConvSeq, &c.Unread, &c.UpdatedAtMs, &c.LastText, &c.Title, &c.Kind, &muted, &pinned, &groupAvatar, &groupMode, &c.UnreadMention, &c.DraftText); err != nil {
			return nil, err
		}
		c.Muted = muted != 0
		c.Pinned = pinned != 0
		if conv.IsGroup(c.Cid) {
			c.PeerProfile = groupPeerProfile(&groupInfo{AvatarURL: groupAvatar, Mode: groupMode})
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *mysqlStore) Timeline(ctx context.Context, uid, cid string, afterSeq uint64, limit int) (string, []*imv1.TimelineMessage, error) {
	if conv.IsGroup(cid) {
		ok, err := s.isMember(ctx, cid, uid)
		if err != nil {
			return "", nil, err
		}
		if !ok {
			var n int
			err = s.db.QueryRowContext(ctx, `SELECT 1 FROM conversations WHERE uid = ? AND cid = ? LIMIT 1`, uid, cid).Scan(&n)
			if err == sql.ErrNoRows {
				return "", nil, errNotMember
			}
			if err != nil {
				return "", nil, err
			}
		}
	} else if _, err := conv.PeerUID(cid, uid); err != nil {
		return "", nil, fmt.Errorf("%w: %v", errInvalid, err)
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT msg_id, conv_seq, from_uid, payload_type, payload_text, COALESCE(payload_media, ''), created_at_ms, recalled, quote_msg_id
		FROM messages WHERE cid = ? AND conv_seq > ?
		ORDER BY conv_seq ASC
		LIMIT ?`, cid, afterSeq, limit)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	var out []*imv1.TimelineMessage
	for rows.Next() {
		m := &imv1.TimelineMessage{}
		var ptype int32
		var text, media string
		var recalled int
		if err := rows.Scan(&m.MsgId, &m.ConvSeq, &m.FromUid, &ptype, &text, &media, &m.CreatedAtMs, &recalled, &m.QuoteMsgId); err != nil {
			return "", nil, err
		}
		m.Recalled = recalled != 0
		m.Payload = payloadFromCols(ptype, text, media, m.Recalled)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	s.attachReactions(ctx, out)
	_, _ = s.db.ExecContext(ctx, `UPDATE conversations SET unread = 0, unread_mention = 0 WHERE uid = ? AND cid = ?`, uid, cid)
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
	_ = s.EnsureUser(ctx, uid)
	_ = s.EnsureUser(ctx, peerUID)
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
	if conv.IsFileHelper(peerUID) || conv.IsFileHelper(uid) {
		return true, nil
	}
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

func inPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

func (s *mysqlStore) AreFriendsMany(ctx context.Context, uid string, peers []string) (map[string]bool, error) {
	out := map[string]bool{}
	var need []string
	for _, peer := range uniqueUIDs(peers) {
		if conv.IsFileHelper(peer) || conv.IsFileHelper(uid) {
			out[peer] = true
			continue
		}
		if peer == uid {
			continue
		}
		need = append(need, peer)
	}
	if len(need) == 0 {
		return out, nil
	}
	args := make([]any, 0, 1+len(need))
	args = append(args, uid)
	for _, peer := range need {
		args = append(args, peer)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT peer_uid FROM friends WHERE uid = ? AND peer_uid IN (`+inPlaceholders(len(need))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var peer string
		if err := rows.Scan(&peer); err != nil {
			return nil, err
		}
		out[peer] = true
	}
	return out, rows.Err()
}

func (s *mysqlStore) requireFriends(ctx context.Context, uid string, peers []string) error {
	var check []string
	for _, peer := range uniqueUIDs(peers) {
		if peer != uid {
			check = append(check, peer)
		}
	}
	if len(check) == 0 {
		return nil
	}
	ok, err := s.AreFriendsMany(ctx, uid, check)
	if err != nil {
		return err
	}
	for _, peer := range check {
		if !ok[peer] {
			return fmt.Errorf("%w: add friend first", errNotFriends)
		}
	}
	return nil
}

func isDupErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Duplicate entry") || strings.Contains(msg, "UNIQUE constraint")
}
