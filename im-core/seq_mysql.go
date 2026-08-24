package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const seqBatch = 100

func seedSeqs(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS seqs (
  k VARCHAR(160) NOT NULL,
  n BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (k)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`INSERT INTO seqs (k, n)
SELECT CONCAT('seq:conv:', cid), MAX(conv_seq) FROM messages GROUP BY cid
ON DUPLICATE KEY UPDATE n = GREATEST(seqs.n, VALUES(n))`,
		`INSERT INTO seqs (k, n)
SELECT CONCAT('seq:sync:', uid), MAX(sync_seq) FROM inbox GROUP BY uid
ON DUPLICATE KEY UPDATE n = GREATEST(seqs.n, VALUES(n))`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("seed seqs: %w", err)
		}
	}
	return nil
}

func bumpSeqs(ctx context.Context, tx *sql.Tx, keys []string) (map[string]uint64, error) {
	out := make(map[string]uint64, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	keys = uniqueSorted(append([]string(nil), keys...))
	for i := 0; i < len(keys); i += seqBatch {
		j := i + seqBatch
		if j > len(keys) {
			j = len(keys)
		}
		chunk := keys[i:j]
		var sb strings.Builder
		args := make([]any, 0, len(chunk))
		sb.WriteString(`INSERT INTO seqs (k, n) VALUES `)
		for k, key := range chunk {
			if k > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(`(?, 1)`)
			args = append(args, key)
		}
		sb.WriteString(` ON DUPLICATE KEY UPDATE n = n + 1`)
		if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
			return nil, fmt.Errorf("bump seqs: %w", err)
		}
		ph := strings.Repeat("?,", len(chunk))
		ph = ph[:len(ph)-1]
		qargs := make([]any, len(chunk))
		for k, key := range chunk {
			qargs[k] = key
		}
		rows, err := tx.QueryContext(ctx, `SELECT k, n FROM seqs WHERE k IN (`+ph+`)`, qargs...)
		if err != nil {
			return nil, fmt.Errorf("read seqs: %w", err)
		}
		for rows.Next() {
			var key string
			var n uint64
			if err := rows.Scan(&key, &n); err != nil {
				rows.Close()
				return nil, err
			}
			out[key] = n
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	for _, key := range keys {
		if out[key] == 0 {
			return nil, fmt.Errorf("seq missing %s", key)
		}
	}
	return out, nil
}
