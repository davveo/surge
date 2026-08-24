package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/davveo/surge/pkg/mail"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

func (a *httpAPI) forgotPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.tooMany(r, "rl:forgot:"+clientIP(r), 8, time.Minute) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	var body struct {
		UID string `json:"uid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.UID) == "" {
		http.Error(w, `{"error":"uid required"}`, http.StatusBadRequest)
		return
	}
	prof, err := a.core.GetProfile(r.Context(), &imv1.GetProfileRequest{Uid: strings.TrimSpace(body.UID)})
	if err != nil || prof.GetUid() == "" {
		lookup, lerr := a.core.LookupUser(r.Context(), &imv1.LookupUserRequest{Query: strings.TrimSpace(body.UID)})
		if lerr != nil || !lookup.GetFound() || lookup.GetUid() == "" {
			writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
			return
		}
		prof, err = a.core.GetProfile(r.Context(), &imv1.GetProfileRequest{Uid: lookup.GetUid()})
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
			return
		}
	}
	code := randDigits(6)
	if a.rdb != nil {
		_ = a.rdb.Set(r.Context(), "reset:"+code, prof.GetUid(), 15*time.Minute).Err()
	}
	if prof.GetEmail() != "" {
		a.sendMail(prof.GetEmail(), "Surge 重置密码", "验证码："+code+"（15 分钟内有效）")
	}
	out := map[string]string{"ok": "1"}
	if a.rdb == nil {
		out["code"] = code
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *httpAPI) resetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Code) == "" {
		http.Error(w, `{"error":"code required"}`, http.StatusBadRequest)
		return
	}
	uid := strings.TrimSpace(body.Code)
	if a.rdb != nil {
		got, err := a.rdb.Get(r.Context(), "reset:"+strings.TrimSpace(body.Code)).Result()
		if err != nil || got == "" {
			http.Error(w, `{"error":"invalid code"}`, http.StatusBadRequest)
			return
		}
		uid = got
	}
	if _, err := a.core.ResetPassword(r.Context(), &imv1.LoginRequest{Uid: uid, Password: body.Password}); err != nil {
		writeRPCError(w, err)
		return
	}
	if a.rdb != nil {
		_ = a.rdb.Del(r.Context(), "reset:"+strings.TrimSpace(body.Code)).Err()
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1", "uid": uid})
}

func (a *httpAPI) deleteAccount(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := a.core.DeleteAccount(r.Context(), &imv1.GetProfileRequest{Uid: uid}); err != nil {
		writeRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

func (a *httpAPI) loginHistory(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	list := []map[string]string{}
	if a.rdb != nil {
		raw, _ := a.rdb.LRange(r.Context(), "loginhist:"+uid, 0, 19).Result()
		for _, s := range raw {
			var row map[string]string
			if json.Unmarshal([]byte(s), &row) == nil {
				list = append(list, row)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": list})
}

func (a *httpAPI) noteLogin(ctx context.Context, uid, device, ip string) {
	if a.rdb == nil || uid == "" {
		return
	}
	b, _ := json.Marshal(map[string]string{
		"at": time.Now().Format(time.RFC3339), "device": device, "ip": ip,
	})
	_ = a.rdb.LPush(ctx, "loginhist:"+uid, string(b)).Err()
	_ = a.rdb.LTrim(ctx, "loginhist:"+uid, 0, 49).Err()
}

func randDigits(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	const digits = "0123456789"
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = digits[int(buf[i])%10]
	}
	return string(out)
}

func (a *httpAPI) sendMail(to, subject, body string) {
	if err := mail.Send(mail.FromEnv(), to, subject, body); err != nil {
		log.Printf("smtp %s: %v", to, err)
	}
}

func (a *httpAPI) revokeInvite(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Token == "" {
		body.Token = r.URL.Query().Get("token")
	}
	if _, err := a.core.RevokeGroupInvite(r.Context(), &imv1.JoinInviteRequest{Uid: uid, Token: body.Token}); err != nil {
		writeRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}
