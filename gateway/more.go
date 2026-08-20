package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/davveo/surge/pkg/route"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

type memLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newMemLimiter() *memLimiter {
	return &memLimiter{hits: map[string][]time.Time{}}
}

func (l *memLimiter) hit(key string, n int, window time.Duration) bool {
	now := time.Now()
	cut := now.Add(-window)
	l.mu.Lock()
	defer l.mu.Unlock()
	xs := l.hits[key]
	i := 0
	for i < len(xs) && xs[i].Before(cut) {
		i++
	}
	xs = xs[i:]
	if len(xs) >= n {
		l.hits[key] = xs
		return true
	}
	l.hits[key] = append(xs, now)
	return false
}

func (a *httpAPI) tooMany(r *http.Request, key string, n int64, window time.Duration) bool {
	if a.rdb != nil {
		v, err := a.rdb.Incr(r.Context(), key).Result()
		if err == nil {
			if v == 1 {
				_ = a.rdb.Expire(r.Context(), key, window).Err()
			}
			return v > n
		}
	}
	if a.limit == nil {
		return false
	}
	return a.limit.hit(key, int(n), window)
}

func (a *httpAPI) presence(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.uidFromAuth(w, r); !ok {
		return
	}
	raw := strings.TrimSpace(r.URL.Query().Get("uids"))
	uids := strings.FieldsFunc(raw, func(c rune) bool { return c == ',' || c == ' ' })
	online := map[string]bool{}
	for _, uid := range uids {
		on := a.ws != nil && a.ws.hub != nil && a.ws.hub.isOnline(uid)
		if !on && a.rdb != nil {
			n, err := a.rdb.Exists(r.Context(), route.Key(uid)).Result()
			on = err == nil && n > 0
		}
		online[uid] = on
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"online": online})
}

func (a *httpAPI) searchAll(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	resp, err := a.core.SearchMessages(r.Context(), &imv1.SearchMessagesRequest{Uid: uid, Query: q, Limit: 30})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) groupMuteAll(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		CID   string `json:"cid"`
		Muted bool   `json:"muted"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CID == "" {
		http.Error(w, `{"error":"cid required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.SetGroupMuteAll(r.Context(), &imv1.SetMuteRequest{Uid: uid, Cid: body.CID, Muted: body.Muted})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) friendTags(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		resp, err := a.core.ListFriendTags(r.Context(), &imv1.ListFriendsRequest{Uid: uid})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		writeProtoJSON(w, resp)
	case http.MethodPost:
		var body struct {
			PeerUID string   `json:"peer_uid"`
			Tags    []string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PeerUID == "" {
			http.Error(w, `{"error":"peer_uid required"}`, http.StatusBadRequest)
			return
		}
		resp, err := a.core.SetFriendTags(r.Context(), &imv1.SetFriendTagsRequest{Uid: uid, PeerUid: body.PeerUID, Tags: body.Tags})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		writeProtoJSON(w, resp)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *httpAPI) e2eeKey(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		raw := strings.TrimSpace(r.URL.Query().Get("uids"))
		uids := strings.FieldsFunc(raw, func(c rune) bool { return c == ',' || c == ' ' })
		resp, err := a.core.GetPublicKeys(r.Context(), &imv1.GetProfilesRequest{Uids: uids})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		writeProtoJSON(w, resp)
	case http.MethodPost:
		var body struct {
			PublicKey string `json:"public_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.PublicKey) == "" {
			http.Error(w, `{"error":"public_key required"}`, http.StatusBadRequest)
			return
		}
		resp, err := a.core.SetPublicKey(r.Context(), &imv1.SetPublicKeyRequest{Uid: uid, PublicKey: body.PublicKey})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		writeProtoJSON(w, resp)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *httpAPI) consume(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		CID   string `json:"cid"`
		MsgID string `json:"msg_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.MsgID == "" {
		http.Error(w, `{"error":"msg_id required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.ConsumeEphemeral(r.Context(), &imv1.RecallMessageRequest{Uid: uid, Cid: body.CID, MsgId: body.MsgID})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) stickers(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		resp, err := a.core.ListStickers(r.Context(), &imv1.ListFriendsRequest{Uid: uid})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		writeProtoJSON(w, resp)
	case http.MethodPost:
		var body struct {
			URL  string `json:"url"`
			Pack string `json:"pack"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.URL) == "" {
			http.Error(w, `{"error":"url required"}`, http.StatusBadRequest)
			return
		}
		resp, err := a.core.AddSticker(r.Context(), &imv1.AddStickerRequest{Uid: uid, Url: body.URL, Pack: body.Pack})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		writeProtoJSON(w, resp)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *httpAPI) oauthDemo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.tooMany(r, "rl:login:"+clientIP(r), 30, time.Minute) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	var body struct {
		Provider string `json:"provider"`
		Subject  string `json:"subject"`
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Subject) == "" {
		http.Error(w, `{"error":"subject required"}`, http.StatusBadRequest)
		return
	}
	provider := strings.ToLower(strings.TrimSpace(body.Provider))
	if provider == "" {
		provider = "oauth"
	}
	uid := sanitizeOAuthUID(provider + "_" + body.Subject)
	if _, err := a.core.UpdateProfile(r.Context(), &imv1.UpdateProfileRequest{Uid: uid, DisplayName: body.Subject}); err != nil {
		writeRPCError(w, err)
		return
	}
	sess, err := a.issueSession(r.Context(), uid, body.DeviceID, accessTTL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func sanitizeOAuthUID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) < 2 {
		out = "oauth_" + out + "x"
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func (a *httpAPI) oauthGithubStart(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(os.Getenv("GITHUB_CLIENT_ID"))
	if id == "" {
		http.Error(w, `{"error":"github oauth not configured"}`, http.StatusNotImplemented)
		return
	}
	redir := strings.TrimSpace(os.Getenv("OAUTH_REDIRECT_URL"))
	if redir == "" {
		redir = "http://127.0.0.1:8080/v1/auth/oauth/github/callback"
	}
	st := randomHex(16)
	http.SetCookie(w, &http.Cookie{Name: "oauth_state", Value: st, Path: "/", MaxAge: 600, HttpOnly: true})
	u := "https://github.com/login/oauth/authorize?client_id=" + url.QueryEscape(id) +
		"&redirect_uri=" + url.QueryEscape(redir) + "&scope=read:user&state=" + st
	http.Redirect(w, r, u, http.StatusFound)
}

func (a *httpAPI) oauthGithubCallback(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(os.Getenv("GITHUB_CLIENT_ID"))
	secret := strings.TrimSpace(os.Getenv("GITHUB_CLIENT_SECRET"))
	if id == "" || secret == "" {
		http.Error(w, "oauth not configured", http.StatusNotImplemented)
		return
	}
	ck, _ := r.Cookie("oauth_state")
	if ck == nil || ck.Value == "" || ck.Value != r.URL.Query().Get("state") {
		http.Error(w, "bad state", http.StatusBadRequest)
		return
	}
	form := url.Values{
		"client_id":     {id},
		"client_secret": {secret},
		"code":          {r.URL.Query().Get("code")},
	}
	req, err := http.NewRequest(http.MethodPost, "https://github.com/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&tok)
	if tok.AccessToken == "" {
		http.Error(w, "token failed", http.StatusBadGateway)
		return
	}
	ureq, _ := http.NewRequest(http.MethodGet, "https://api.github.com/user", nil)
	ureq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	ureq.Header.Set("Accept", "application/json")
	uresp, err := http.DefaultClient.Do(ureq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer uresp.Body.Close()
	raw, _ := io.ReadAll(uresp.Body)
	var gh struct {
		Login string `json:"login"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	_ = json.Unmarshal(raw, &gh)
	if gh.Login == "" {
		http.Error(w, "github user failed", http.StatusBadGateway)
		return
	}
	uid := sanitizeOAuthUID("gh_" + gh.Login)
	name := gh.Name
	if name == "" {
		name = gh.Login
	}
	if _, err := a.core.UpdateProfile(r.Context(), &imv1.UpdateProfileRequest{Uid: uid, DisplayName: name, Email: gh.Email}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sess, err := a.issueSession(r.Context(), uid, "web", accessTTL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "oauth_state", Path: "/", MaxAge: -1})
	js, _ := json.Marshal(sess)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><script>
localStorage.setItem("surge:oauth", JSON.stringify(` + string(js) + `));
location.href="/";
</script>`))
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
