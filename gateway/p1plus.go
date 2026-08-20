package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/davveo/surge/pkg/auth"
	"github.com/davveo/surge/pkg/route"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
	"github.com/skip2/go-qrcode"
)

func deviceIDFromRequest(secret string, r *http.Request) string {
	raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	claims, err := auth.Parse(secret, strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return claims.DeviceID
}

func (a *httpAPI) messageDelete(w http.ResponseWriter, r *http.Request) {
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
	resp, err := a.core.DeleteMessage(r.Context(), &imv1.RecallMessageRequest{Uid: uid, Cid: body.CID, MsgId: body.MsgID})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) conversationClear(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		CID string `json:"cid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CID == "" {
		http.Error(w, `{"error":"cid required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.ClearConversation(r.Context(), &imv1.HideConversationRequest{Uid: uid, Cid: body.CID})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) groupMember(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		CID      string  `json:"cid"`
		UID      string  `json:"uid"`
		Nickname *string `json:"nickname"`
		Role     *string `json:"role"`
		Muted    *bool   `json:"muted"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CID == "" || body.UID == "" {
		http.Error(w, `{"error":"cid and uid required"}`, http.StatusBadRequest)
		return
	}
	req := &imv1.SetMemberRequest{OperatorUid: uid, Cid: body.CID, MemberUid: body.UID}
	if body.Nickname != nil {
		req.Nickname = *body.Nickname
		req.SetNickname = true
	}
	if body.Role != nil {
		req.Role = *body.Role
		req.SetRole = true
	}
	if body.Muted != nil {
		req.Muted = *body.Muted
		req.SetMuted = true
	}
	resp, err := a.core.SetMember(r.Context(), req)
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) groupJoinRequests(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	cid := r.URL.Query().Get("cid")
	if cid == "" {
		http.Error(w, `{"error":"cid required"}`, http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodGet {
		resp, err := a.core.ListJoinRequests(r.Context(), &imv1.GetGroupRequest{Uid: uid, Cid: cid})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		writeProtoJSON(w, resp)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		CID    string `json:"cid"`
		UID    string `json:"uid"`
		Accept bool   `json:"accept"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CID == "" || body.UID == "" {
		http.Error(w, `{"error":"cid and uid required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.DecideJoin(r.Context(), &imv1.DecideJoinRequest{
		OperatorUid: uid, Cid: body.CID, MemberUid: body.UID, Accept: body.Accept,
	})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	if body.Accept {
		a.pushRoster(body.UID, uid, "group_invite", body.CID)
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) groupJoin(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		CID string `json:"cid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CID == "" {
		http.Error(w, `{"error":"cid required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.RequestJoin(r.Context(), &imv1.LeaveGroupRequest{Uid: uid, Cid: body.CID})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) devices(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	selfDevice := deviceIDFromRequest(a.secret, r)
	switch r.Method {
	case http.MethodGet:
		list := []map[string]string{}
		if a.ws != nil && a.ws.hub != nil {
			list = a.ws.hub.listDevices(uid, selfDevice)
		}
		if a.rdb != nil {
			raw, err := a.rdb.Get(r.Context(), route.Key(uid)).Bytes()
			if err == nil {
				rs, _ := route.DecodeAll(raw)
				seen := map[string]bool{}
				for _, d := range list {
					seen[d["conn_id"]] = true
				}
				for _, rec := range rs {
					if seen[rec.ConnID] {
						continue
					}
					self := "0"
					if selfDevice != "" && rec.DeviceID == selfDevice {
						self = "1"
					}
					list = append(list, map[string]string{
						"conn_id": rec.ConnID, "device_id": rec.DeviceID, "gateway_id": rec.GatewayID, "self": self,
					})
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"devices": list})
	case http.MethodPost:
		var body struct {
			ConnID string `json:"conn_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ConnID == "" {
			http.Error(w, `{"error":"conn_id required"}`, http.StatusBadRequest)
			return
		}
		if a.ws != nil && a.ws.hub != nil {
			if selfDevice != "" && a.ws.hub.isSelfDevice(body.ConnID, selfDevice) {
				http.Error(w, "cannot kick current device", http.StatusBadRequest)
				return
			}
			a.ws.hub.kick(r.Context(), uid, body.ConnID)
		}
		writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *httpAPI) meQRPNG(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		origin = scheme + "://" + r.Host
	}
	payload := strings.TrimRight(origin, "/") + "/?add=" + uid
	png, err := qrcode.Encode(payload, qrcode.Medium, 196)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

func (h *Hub) listDevices(uid, selfDeviceID string) []map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]map[string]string, 0)
	for _, c := range h.byUID[uid] {
		self := "0"
		if selfDeviceID != "" && c.deviceID == selfDeviceID {
			self = "1"
		}
		out = append(out, map[string]string{
			"conn_id": c.id, "device_id": c.deviceID, "gateway_id": h.gwID, "self": self,
		})
	}
	return out
}

func (h *Hub) isSelfDevice(connID, selfDeviceID string) bool {
	if connID == "" || selfDeviceID == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.byConn[connID]
	return c != nil && c.deviceID == selfDeviceID
}

func (h *Hub) kick(ctx context.Context, uid, connID string) {
	h.kickLocal(uid, connID)
	if h.rdb == nil {
		return
	}
	b, _ := json.Marshal(map[string]string{"uid": uid, "conn_id": connID, "kick": "1"})
	raw, err := h.rdb.Get(ctx, route.Key(uid)).Bytes()
	if err != nil {
		return
	}
	rs, _ := route.DecodeAll(raw)
	seen := map[string]struct{}{}
	for _, rec := range rs {
		if rec.GatewayID == h.gwID {
			continue
		}
		if _, ok := seen[rec.GatewayID]; ok {
			continue
		}
		seen[rec.GatewayID] = struct{}{}
		_ = h.rdb.Publish(ctx, route.Channel(rec.GatewayID), b).Err()
	}
}

func (h *Hub) kickLocal(uid, connID string) {
	h.mu.Lock()
	c := h.byConn[connID]
	if c != nil && (uid == "" || c.uid == uid) {
		h.mu.Unlock()
		_ = c.ws.Close()
		return
	}
	h.mu.Unlock()
}
