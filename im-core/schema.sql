CREATE TABLE IF NOT EXISTS messages (
  msg_id CHAR(36) NOT NULL,
  client_msg_id VARCHAR(64) NOT NULL,
  cid VARCHAR(128) NOT NULL,
  conv_seq BIGINT UNSIGNED NOT NULL,
  from_uid VARCHAR(64) NOT NULL,
  payload_type TINYINT NOT NULL,
  payload_text TEXT NOT NULL,
  payload_media TEXT NOT NULL,
  created_at_ms BIGINT NOT NULL,
  recalled TINYINT NOT NULL DEFAULT 0,
  quote_msg_id VARCHAR(36) NOT NULL DEFAULT '',
  PRIMARY KEY (msg_id),
  UNIQUE KEY uk_sender_client (from_uid, client_msg_id),
  UNIQUE KEY uk_cid_seq (cid, conv_seq),
  KEY idx_cid_seq (cid, conv_seq)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS inbox (
  uid VARCHAR(64) NOT NULL,
  sync_seq BIGINT UNSIGNED NOT NULL,
  cid VARCHAR(128) NOT NULL,
  msg_id CHAR(36) NOT NULL,
  conv_seq BIGINT UNSIGNED NOT NULL,
  from_uid VARCHAR(64) NOT NULL,
  created_at_ms BIGINT NOT NULL,
  PRIMARY KEY (uid, sync_seq),
  KEY idx_inbox_msg (msg_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS conversations (
  uid VARCHAR(64) NOT NULL,
  cid VARCHAR(128) NOT NULL,
  peer_uid VARCHAR(64) NOT NULL,
  last_msg_id CHAR(36) NOT NULL,
  last_conv_seq BIGINT UNSIGNED NOT NULL,
  last_text VARCHAR(512) NOT NULL,
  unread INT UNSIGNED NOT NULL DEFAULT 0,
  updated_at_ms BIGINT NOT NULL,
  title VARCHAR(128) NOT NULL DEFAULT '',
  kind VARCHAR(16) NOT NULL DEFAULT 'p2p',
  hidden TINYINT NOT NULL DEFAULT 0,
  pinned TINYINT NOT NULL DEFAULT 0,
  cleared_seq BIGINT UNSIGNED NOT NULL DEFAULT 0,
  unread_mention INT UNSIGNED NOT NULL DEFAULT 0,
  draft_text TEXT,
  PRIMARY KEY (uid, cid),
  KEY idx_uid_updated (uid, updated_at_ms)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS friends (
  uid VARCHAR(64) NOT NULL,
  peer_uid VARCHAR(64) NOT NULL,
  created_at_ms BIGINT NOT NULL,
  PRIMARY KEY (uid, peer_uid),
  KEY idx_peer (peer_uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS im_groups (
  cid VARCHAR(128) NOT NULL,
  name VARCHAR(128) NOT NULL,
  owner_uid VARCHAR(64) NOT NULL,
  created_at_ms BIGINT NOT NULL,
  avatar_url VARCHAR(512) NOT NULL DEFAULT '',
  muted_all TINYINT NOT NULL DEFAULT 0,
  announcement TEXT,
  join_approval TINYINT NOT NULL DEFAULT 0,
  PRIMARY KEY (cid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS group_members (
  cid VARCHAR(128) NOT NULL,
  uid VARCHAR(64) NOT NULL,
  role VARCHAR(16) NOT NULL,
  nickname VARCHAR(64) NOT NULL DEFAULT '',
  muted TINYINT NOT NULL DEFAULT 0,
  joined_at_ms BIGINT NOT NULL,
  PRIMARY KEY (cid, uid),
  KEY idx_uid (uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS read_cursors (
  uid VARCHAR(64) NOT NULL,
  cid VARCHAR(128) NOT NULL,
  conv_seq BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (uid, cid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS users (
  uid VARCHAR(64) NOT NULL,
  password_hash VARCHAR(255) NOT NULL DEFAULT '',
  display_name VARCHAR(64) NOT NULL DEFAULT '',
  avatar_url VARCHAR(512) NOT NULL DEFAULT '',
  email VARCHAR(128) NOT NULL DEFAULT '',
  phone VARCHAR(32) NOT NULL DEFAULT '',
  public_key TEXT,
  created_at_ms BIGINT NOT NULL,
  PRIMARY KEY (uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS conv_mutes (
  uid VARCHAR(64) NOT NULL,
  cid VARCHAR(128) NOT NULL,
  muted TINYINT NOT NULL DEFAULT 1,
  PRIMARY KEY (uid, cid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS friend_requests (
  from_uid VARCHAR(64) NOT NULL,
  to_uid VARCHAR(64) NOT NULL,
  created_at_ms BIGINT NOT NULL,
  hello VARCHAR(200) NOT NULL DEFAULT '',
  source VARCHAR(64) NOT NULL DEFAULT '',
  PRIMARY KEY (from_uid, to_uid),
  KEY idx_to (to_uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS blocks (
  uid VARCHAR(64) NOT NULL,
  peer_uid VARCHAR(64) NOT NULL,
  created_at_ms BIGINT NOT NULL,
  PRIMARY KEY (uid, peer_uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS friend_remarks (
  uid VARCHAR(64) NOT NULL,
  peer_uid VARCHAR(64) NOT NULL,
  remark VARCHAR(64) NOT NULL,
  PRIMARY KEY (uid, peer_uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS friend_tags (
  uid VARCHAR(64) NOT NULL,
  peer_uid VARCHAR(64) NOT NULL,
  tag VARCHAR(32) NOT NULL,
  created_at_ms BIGINT NOT NULL,
  PRIMARY KEY (uid, peer_uid, tag),
  KEY idx_uid_tag (uid, tag)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS stickers (
  id CHAR(36) NOT NULL,
  uid VARCHAR(64) NOT NULL,
  url VARCHAR(512) NOT NULL,
  pack VARCHAR(64) NOT NULL,
  created_at_ms BIGINT NOT NULL,
  PRIMARY KEY (id),
  KEY idx_uid (uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS hidden_messages (
  uid VARCHAR(64) NOT NULL,
  msg_id CHAR(36) NOT NULL,
  PRIMARY KEY (uid, msg_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS group_join_requests (
  cid VARCHAR(128) NOT NULL,
  uid VARCHAR(64) NOT NULL,
  from_uid VARCHAR(64) NOT NULL,
  created_at_ms BIGINT NOT NULL,
  PRIMARY KEY (cid, uid),
  KEY idx_cid (cid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS message_reactions (
  msg_id CHAR(36) NOT NULL,
  uid VARCHAR(64) NOT NULL,
  emoji VARCHAR(16) NOT NULL,
  created_at_ms BIGINT NOT NULL,
  PRIMARY KEY (msg_id, uid),
  KEY idx_msg (msg_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS favorites (
  fav_id CHAR(36) NOT NULL,
  uid VARCHAR(64) NOT NULL,
  cid VARCHAR(128) NOT NULL,
  msg_id CHAR(36) NOT NULL,
  from_uid VARCHAR(64) NOT NULL,
  preview VARCHAR(512) NOT NULL,
  payload_json TEXT NOT NULL,
  created_at_ms BIGINT NOT NULL,
  PRIMARY KEY (fav_id),
  UNIQUE KEY uk_uid_msg (uid, msg_id),
  KEY idx_uid (uid, created_at_ms)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS group_invites (
  token VARCHAR(32) NOT NULL,
  cid VARCHAR(128) NOT NULL,
  from_uid VARCHAR(64) NOT NULL,
  created_at_ms BIGINT NOT NULL,
  expires_at_ms BIGINT NOT NULL,
  PRIMARY KEY (token),
  KEY idx_cid (cid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS chat_pins (
  cid VARCHAR(128) NOT NULL,
  msg_id CHAR(36) NOT NULL,
  from_uid VARCHAR(64) NOT NULL,
  preview VARCHAR(512) NOT NULL,
  created_at_ms BIGINT NOT NULL,
  PRIMARY KEY (cid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS reports (
  id CHAR(36) NOT NULL,
  uid VARCHAR(64) NOT NULL,
  cid VARCHAR(128) NOT NULL,
  msg_id CHAR(36) NOT NULL,
  reason VARCHAR(256) NOT NULL,
  created_at_ms BIGINT NOT NULL,
  PRIMARY KEY (id),
  KEY idx_uid (uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS seqs (
  k VARCHAR(160) NOT NULL,
  n BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (k)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS user_settings (
  uid VARCHAR(64) NOT NULL,
  dark TINYINT NOT NULL DEFAULT 0,
  wallpaper VARCHAR(64) NOT NULL DEFAULT '',
  notify_sound TINYINT NOT NULL DEFAULT 1,
  notify_preview TINYINT NOT NULL DEFAULT 1,
  dnd_start VARCHAR(8) NOT NULL DEFAULT '',
  dnd_end VARCHAR(8) NOT NULL DEFAULT '',
  PRIMARY KEY (uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
