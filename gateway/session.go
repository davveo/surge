package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/davveo/surge/pkg/auth"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

const accessTTL = 24 * time.Hour
const refreshTTL = 30 * 24 * time.Hour
const devAccessTTL = 7 * 24 * time.Hour

func refreshKey(token string) string { return "refresh:" + token }

type sessionOut struct {
	UID          string `json:"uid"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in"`
}

func (a *httpAPI) issueSession(ctx context.Context, uid, device string, ttl time.Duration) (*sessionOut, error) {
	if ttl <= 0 {
		ttl = accessTTL
	}
	token, err := auth.Issue(a.secret, uid, device, ttl)
	if err != nil {
		return nil, err
	}
	_, _ = a.core.UpdateProfile(ctx, &imv1.UpdateProfileRequest{Uid: uid})
	out := &sessionOut{UID: uid, AccessToken: token, ExpiresIn: int(ttl.Seconds())}
	if a.rdb != nil {
		refresh := uuid.NewString()
		if err := a.rdb.Set(ctx, refreshKey(refresh), uid+"|"+device, refreshTTL).Err(); err == nil {
			out.RefreshToken = refresh
		}
	}
	return out, nil
}

func (a *httpAPI) register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		UID      string `json:"uid"`
		Password string `json:"password"`
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if _, err := a.core.Register(r.Context(), &imv1.RegisterRequest{Uid: body.UID, Password: body.Password}); err != nil {
		writeRPCError(w, err)
		return
	}
	sess, err := a.issueSession(r.Context(), body.UID, body.DeviceID, accessTTL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (a *httpAPI) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		UID      string `json:"uid"`
		Password string `json:"password"`
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if _, err := a.core.VerifyPassword(r.Context(), &imv1.LoginRequest{Uid: body.UID, Password: body.Password}); err != nil {
		writeRPCError(w, err)
		return
	}
	sess, err := a.issueSession(r.Context(), body.UID, body.DeviceID, accessTTL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (a *httpAPI) refresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.rdb == nil {
		http.Error(w, "refresh unavailable", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		RefreshToken string `json:"refresh_token"`
		DeviceID     string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.RefreshToken) == "" {
		http.Error(w, `{"error":"refresh_token required"}`, http.StatusBadRequest)
		return
	}
	raw, err := a.rdb.Get(r.Context(), refreshKey(body.RefreshToken)).Result()
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	uid, device, _ := strings.Cut(raw, "|")
	if body.DeviceID != "" {
		device = body.DeviceID
	}
	_ = a.rdb.Del(r.Context(), refreshKey(body.RefreshToken)).Err()
	sess, err := a.issueSession(r.Context(), uid, device, accessTTL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (a *httpAPI) me(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		resp, err := a.core.GetProfile(r.Context(), &imv1.GetProfileRequest{Uid: uid})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		writeProtoJSON(w, resp)
	case http.MethodPost:
		var body struct {
			DisplayName string `json:"display_name"`
			AvatarURL   string `json:"avatar_url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		resp, err := a.core.UpdateProfile(r.Context(), &imv1.UpdateProfileRequest{
			Uid: uid, DisplayName: body.DisplayName, AvatarUrl: body.AvatarURL,
		})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		writeProtoJSON(w, resp)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
