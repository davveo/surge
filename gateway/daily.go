package main

import (
	"encoding/json"
	"net/http"
	"strings"

	imv1 "github.com/davveo/surge/proto/gen/im/v1"
	"github.com/skip2/go-qrcode"
)

func (a *httpAPI) react(w http.ResponseWriter, r *http.Request) {
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
		Emoji string `json:"emoji"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.MsgID == "" {
		http.Error(w, `{"error":"msg_id required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.ReactMessage(r.Context(), &imv1.ReactMessageRequest{Uid: uid, Cid: body.CID, MsgId: body.MsgID, Emoji: body.Emoji})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) favorites(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		resp, err := a.core.ListFavorites(r.Context(), &imv1.ListFavoritesRequest{Uid: uid, Query: r.URL.Query().Get("q")})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		writeProtoJSON(w, resp)
	case http.MethodPost:
		var body struct {
			CID   string `json:"cid"`
			MsgID string `json:"msg_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.MsgID == "" {
			http.Error(w, `{"error":"msg_id required"}`, http.StatusBadRequest)
			return
		}
		resp, err := a.core.AddFavorite(r.Context(), &imv1.FavoriteRequest{Uid: uid, Cid: body.CID, MsgId: body.MsgID})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		writeProtoJSON(w, resp)
	case http.MethodDelete:
		var body struct {
			FavID string `json:"fav_id"`
			MsgID string `json:"msg_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"fav_id required"}`, http.StatusBadRequest)
			return
		}
		id := body.FavID
		if id == "" {
			id = body.MsgID
		}
		resp, err := a.core.DeleteFavorite(r.Context(), &imv1.FavoriteRequest{Uid: uid, FavId: id})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		writeProtoJSON(w, resp)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *httpAPI) groupInviteLink(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cid := strings.TrimSpace(r.URL.Query().Get("cid"))
	if r.Method == http.MethodPost {
		var body struct {
			CID string `json:"cid"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.CID != "" {
			cid = body.CID
		}
	}
	if cid == "" {
		http.Error(w, `{"error":"cid required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.CreateGroupInvite(r.Context(), &imv1.GetGroupRequest{Uid: uid, Cid: cid})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	resp.Url = originOf(r) + "/?join=" + resp.Token
	writeProtoJSON(w, resp)
}

func originOf(r *http.Request) string {
	origin := r.Header.Get("Origin")
	if origin == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		origin = scheme + "://" + r.Host
	}
	return strings.TrimRight(origin, "/")
}

func (a *httpAPI) groupInvitePNG(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		http.Error(w, "token required", http.StatusBadRequest)
		return
	}
	payload := originOf(r) + "/?join=" + token
	png, err := qrcode.Encode(payload, qrcode.Medium, 196)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

func (a *httpAPI) groupJoinInvite(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Token) == "" {
		http.Error(w, `{"error":"token required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.JoinByInvite(r.Context(), &imv1.JoinInviteRequest{Uid: uid, Token: body.Token})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) drafts(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		CID  string `json:"cid"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CID == "" {
		http.Error(w, `{"error":"cid required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.SetDraft(r.Context(), &imv1.SetDraftRequest{Uid: uid, Cid: body.CID, Text: body.Text})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) pinned(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		cid := r.URL.Query().Get("cid")
		resp, err := a.core.GetPinnedMessage(r.Context(), &imv1.GetGroupRequest{Uid: uid, Cid: cid})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		writeProtoJSON(w, resp)
	case http.MethodPost:
		var body struct {
			CID   string `json:"cid"`
			MsgID string `json:"msg_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CID == "" {
			http.Error(w, `{"error":"cid required"}`, http.StatusBadRequest)
			return
		}
		resp, err := a.core.PinChatMessage(r.Context(), &imv1.PinChatMessageRequest{Uid: uid, Cid: body.CID, MsgId: body.MsgID})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		writeProtoJSON(w, resp)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *httpAPI) report(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		CID    string `json:"cid"`
		MsgID  string `json:"msg_id"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.MsgID == "" {
		http.Error(w, `{"error":"msg_id required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.ReportMessage(r.Context(), &imv1.ReportRequest{Uid: uid, Cid: body.CID, MsgId: body.MsgID, Reason: body.Reason})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) settings(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		resp, err := a.core.GetSettings(r.Context(), &imv1.ListFriendsRequest{Uid: uid})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, settingsJSON(resp))
	case http.MethodPost:
		var body struct {
			Dark            bool   `json:"dark"`
			Wallpaper       string `json:"wallpaper"`
			NotifySound     *bool  `json:"notify_sound"`
			NotifyPreview   *bool  `json:"notify_preview"`
			DndStart        string `json:"dnd_start"`
			DndEnd          string `json:"dnd_end"`
			NotifyAtMuted   *bool  `json:"notify_at_muted"`
			AddMe           string `json:"add_me"`
			HideRead        *bool  `json:"hide_read"`
			HideTyping      *bool  `json:"hide_typing"`
			HideLastSeen    *bool  `json:"hide_last_seen"`
			BurnSec         int32  `json:"burn_sec"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		st := &imv1.UserSettings{
			Uid: uid, Dark: body.Dark, Wallpaper: body.Wallpaper, DndStart: body.DndStart, DndEnd: body.DndEnd,
			NotifySound: true, NotifyPreview: true, NotifyAtMuted: true, AddMe: body.AddMe, BurnSec: body.BurnSec,
		}
		if body.NotifySound != nil {
			st.NotifySound = *body.NotifySound
		}
		if body.NotifyPreview != nil {
			st.NotifyPreview = *body.NotifyPreview
		}
		if body.NotifyAtMuted != nil {
			st.NotifyAtMuted = *body.NotifyAtMuted
		}
		if body.HideRead != nil {
			st.HideRead = *body.HideRead
		}
		if body.HideTyping != nil {
			st.HideTyping = *body.HideTyping
		}
		if body.HideLastSeen != nil {
			st.HideLastSeen = *body.HideLastSeen
		}
		resp, err := a.core.SetSettings(r.Context(), st)
		if err != nil {
			writeRPCError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, settingsJSON(resp))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func settingsJSON(st *imv1.UserSettings) map[string]interface{} {
	if st == nil {
		st = &imv1.UserSettings{}
	}
	return map[string]interface{}{
		"uid": st.Uid, "dark": st.Dark, "wallpaper": st.Wallpaper,
		"notify_sound": st.NotifySound, "notify_preview": st.NotifyPreview,
		"dnd_start": st.DndStart, "dnd_end": st.DndEnd,
		"notify_at_muted": st.NotifyAtMuted, "add_me": st.AddMe,
		"hide_read": st.HideRead, "hide_typing": st.HideTyping, "hide_last_seen": st.HideLastSeen,
		"burn_sec": st.BurnSec,
	}
}
