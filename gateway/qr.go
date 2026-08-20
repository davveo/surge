package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/skip2/go-qrcode"
)

const qrTTL = 2 * time.Minute

type qrSession struct {
	Status  string `json:"status"`
	UID     string `json:"uid,omitempty"`
	Token   string `json:"token,omitempty"`
	Refresh string `json:"refresh,omitempty"`
}

func qrRedisKey(ticket string) string { return "qrlogin:" + ticket }

func (a *httpAPI) qrNew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.rdb == nil {
		http.Error(w, "qr unavailable", http.StatusServiceUnavailable)
		return
	}
	ticket := uuid.NewString()
	sess := qrSession{Status: "pending"}
	b, _ := json.Marshal(sess)
	if err := a.rdb.Set(r.Context(), qrRedisKey(ticket), b, qrTTL).Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ticket":     ticket,
		"expires_in": int(qrTTL.Seconds()),
		"png":        "/v1/auth/qr.png?ticket=" + ticket,
	})
}

func (a *httpAPI) qrPNG(w http.ResponseWriter, r *http.Request) {
	ticket := strings.TrimSpace(r.URL.Query().Get("ticket"))
	if ticket == "" {
		http.Error(w, "ticket required", http.StatusBadRequest)
		return
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	scan := scheme + "://" + r.Host + "/?ticket=" + urlQueryEscape(ticket)
	png, err := qrcode.Encode(scan, qrcode.Medium, 256)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(png)
}

func urlQueryEscape(s string) string {
	return strings.ReplaceAll(s, " ", "%20")
}

func (a *httpAPI) qrStatus(w http.ResponseWriter, r *http.Request) {
	ticket := strings.TrimSpace(r.URL.Query().Get("ticket"))
	sess, err := a.loadQR(r, ticket)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	out := map[string]string{"status": sess.Status}
	if sess.Status == "approved" {
		out["uid"] = sess.UID
		out["access_token"] = sess.Token
		if sess.Refresh != "" {
			out["refresh_token"] = sess.Refresh
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *httpAPI) qrApprove(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Ticket string `json:"ticket"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Ticket) == "" {
		http.Error(w, `{"error":"ticket required"}`, http.StatusBadRequest)
		return
	}
	sess, err := a.loadQR(r, body.Ticket)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if sess.Status == "approved" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
		return
	}
	sessOut, err := a.issueSession(r.Context(), uid, "web-qr", accessTTL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sess.Status = "approved"
	sess.UID = uid
	sess.Token = sessOut.AccessToken
	sess.Refresh = sessOut.RefreshToken
	b, _ := json.Marshal(sess)
	ttl, err := a.rdb.TTL(r.Context(), qrRedisKey(body.Ticket)).Result()
	if err != nil || ttl <= 0 {
		ttl = qrTTL
	}
	if err := a.rdb.Set(r.Context(), qrRedisKey(body.Ticket), b, ttl).Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved", "uid": uid})
}

func (a *httpAPI) loadQR(r *http.Request, ticket string) (*qrSession, error) {
	ticket = strings.TrimSpace(ticket)
	if ticket == "" {
		return nil, errQR("ticket required")
	}
	if a.rdb == nil {
		return nil, errQR("qr unavailable")
	}
	raw, err := a.rdb.Get(r.Context(), qrRedisKey(ticket)).Bytes()
	if err != nil {
		return nil, errQR("ticket expired")
	}
	var sess qrSession
	if err := json.Unmarshal(raw, &sess); err != nil {
		return nil, errQR("bad ticket")
	}
	return &sess, nil
}

type qrError string

func (e qrError) Error() string { return string(e) }

func errQR(msg string) error { return qrError(msg) }
