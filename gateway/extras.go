package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

func clientIP(r *http.Request) string {
	if x := r.Header.Get("X-Forwarded-For"); x != "" {
		return strings.TrimSpace(strings.Split(x, ",")[0])
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		return host[:i]
	}
	return host
}

func (a *httpAPI) profiles(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.uidFromAuth(w, r); !ok {
		return
	}
	raw := strings.TrimSpace(r.URL.Query().Get("uids"))
	uids := strings.FieldsFunc(raw, func(c rune) bool { return c == ',' || c == ' ' })
	resp, err := a.core.GetProfiles(r.Context(), &imv1.GetProfilesRequest{Uids: uids})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) friendRequests(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		resp, err := a.core.ListFriendRequests(r.Context(), &imv1.ListFriendsRequest{Uid: uid})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		writeProtoJSON(w, resp)
	case http.MethodPost:
		var body struct {
			PeerUID string `json:"peer_uid"`
			Action  string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.PeerUID) == "" {
			http.Error(w, `{"error":"peer_uid required"}`, http.StatusBadRequest)
			return
		}
		switch body.Action {
		case "accept":
			resp, err := a.core.AcceptFriend(r.Context(), &imv1.AddFriendRequest{Uid: uid, PeerUid: body.PeerUID})
			if err != nil {
				writeRPCError(w, err)
				return
			}
			writeProtoJSON(w, resp)
		case "decline":
			resp, err := a.core.DeclineFriend(r.Context(), &imv1.AddFriendRequest{Uid: uid, PeerUid: body.PeerUID})
			if err != nil {
				writeRPCError(w, err)
				return
			}
			writeProtoJSON(w, resp)
		default:
			resp, err := a.core.RequestFriend(r.Context(), &imv1.AddFriendRequest{Uid: uid, PeerUid: body.PeerUID})
			if err != nil {
				writeRPCError(w, err)
				return
			}
			writeProtoJSON(w, resp)
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *httpAPI) blocks(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		resp, err := a.core.ListBlocks(r.Context(), &imv1.ListFriendsRequest{Uid: uid})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		writeProtoJSON(w, resp)
	case http.MethodPost:
		var body struct {
			PeerUID string `json:"peer_uid"`
			Unblock bool   `json:"unblock"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.PeerUID) == "" {
			http.Error(w, `{"error":"peer_uid required"}`, http.StatusBadRequest)
			return
		}
		var (
			resp *imv1.BlockUserResponse
			err  error
		)
		if body.Unblock {
			resp, err = a.core.UnblockUser(r.Context(), &imv1.BlockUserRequest{Uid: uid, PeerUid: body.PeerUID})
		} else {
			resp, err = a.core.BlockUser(r.Context(), &imv1.BlockUserRequest{Uid: uid, PeerUid: body.PeerUID})
		}
		if err != nil {
			writeRPCError(w, err)
			return
		}
		writeProtoJSON(w, resp)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *httpAPI) remark(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		PeerUID string `json:"peer_uid"`
		Remark  string `json:"remark"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.PeerUID) == "" {
		http.Error(w, `{"error":"peer_uid required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.SetRemark(r.Context(), &imv1.SetRemarkRequest{Uid: uid, PeerUid: body.PeerUID, Remark: body.Remark})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) groupLeave(w http.ResponseWriter, r *http.Request) {
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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.CID) == "" {
		http.Error(w, `{"error":"cid required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.LeaveGroup(r.Context(), &imv1.LeaveGroupRequest{Uid: uid, Cid: body.CID})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) groupDissolve(w http.ResponseWriter, r *http.Request) {
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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"cid required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.DissolveGroup(r.Context(), &imv1.LeaveGroupRequest{Uid: uid, Cid: body.CID})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) groupTransfer(w http.ResponseWriter, r *http.Request) {
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
		UID string `json:"uid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CID == "" || body.UID == "" {
		http.Error(w, `{"error":"cid and uid required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.TransferOwner(r.Context(), &imv1.TransferOwnerRequest{OperatorUid: uid, Cid: body.CID, MemberUid: body.UID})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) hideConv(w http.ResponseWriter, r *http.Request) {
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
	resp, err := a.core.HideConversation(r.Context(), &imv1.HideConversationRequest{Uid: uid, Cid: body.CID})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) pin(w http.ResponseWriter, r *http.Request) {
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
		Pinned bool   `json:"pinned"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CID == "" {
		http.Error(w, `{"error":"cid required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.SetPin(r.Context(), &imv1.SetMuteRequest{Uid: uid, Cid: body.CID, Muted: body.Pinned})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) readState(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	cid := r.URL.Query().Get("cid")
	seq, _ := strconv.ParseUint(r.URL.Query().Get("seq"), 10, 64)
	resp, err := a.core.GetReadState(r.Context(), &imv1.GetReadStateRequest{Uid: uid, Cid: cid, ConvSeq: seq})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}
