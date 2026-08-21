package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/davveo/surge/pkg/auth"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type httpAPI struct {
	secret string
	core   imv1.IMCoreClient
	ws     *wsServer
	webDir string
	rdb    *redis.Client
	media  *mediaStore
	limit  *memLimiter
	qrMem  *qrMem
}

func (a *httpAPI) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/v1/auth/dev-login", a.devLogin)
	mux.HandleFunc("/v1/auth/register", a.register)
	mux.HandleFunc("/v1/auth/login", a.login)
	mux.HandleFunc("/v1/auth/refresh", a.refresh)
	mux.HandleFunc("/v1/auth/qr/new", a.qrNew)
	mux.HandleFunc("/v1/auth/qr/status", a.qrStatus)
	mux.HandleFunc("/v1/auth/qr.png", a.qrPNG)
	mux.HandleFunc("/v1/auth/qr/approve", a.qrApprove)
	mux.HandleFunc("/v1/me", a.me)
	mux.HandleFunc("/v1/conversations", a.conversations)
	mux.HandleFunc("/v1/timeline", a.timeline)
	mux.HandleFunc("/v1/friends", a.friends)
	mux.HandleFunc("/v1/users", a.lookupUser)
	mux.HandleFunc("/v1/groups", a.groups)
	mux.HandleFunc("/v1/group-invite", a.groupInvite)
	mux.HandleFunc("/v1/group-kick", a.groupKick)
	mux.HandleFunc("/v1/group-update", a.groupUpdate)
	mux.HandleFunc("/v1/group", a.groupGet)
	mux.HandleFunc("/v1/mute", a.mute)
	mux.HandleFunc("/v1/pin", a.pin)
	mux.HandleFunc("/v1/presence", a.presence)
	mux.HandleFunc("/v1/profiles", a.profiles)
	mux.HandleFunc("/v1/friend-requests", a.friendRequests)
	mux.HandleFunc("/v1/blocks", a.blocks)
	mux.HandleFunc("/v1/remark", a.remark)
	mux.HandleFunc("/v1/group-leave", a.groupLeave)
	mux.HandleFunc("/v1/group-dissolve", a.groupDissolve)
	mux.HandleFunc("/v1/group-transfer", a.groupTransfer)
	mux.HandleFunc("/v1/conversation-hide", a.hideConv)
	mux.HandleFunc("/v1/mark-unread", a.markUnread)
	mux.HandleFunc("/v1/read-state", a.readState)
	mux.HandleFunc("/v1/search", a.searchAll)
	mux.HandleFunc("/v1/group-mute-all", a.groupMuteAll)
	mux.HandleFunc("/v1/friend-tags", a.friendTags)
	mux.HandleFunc("/v1/e2ee/keys", a.e2eeKey)
	mux.HandleFunc("/v1/ephemeral/consume", a.consume)
	mux.HandleFunc("/v1/stickers", a.stickers)
	mux.HandleFunc("/v1/auth/oauth/demo", a.oauthDemo)
	mux.HandleFunc("/v1/auth/oauth/github", a.oauthGithubStart)
	mux.HandleFunc("/v1/auth/oauth/github/callback", a.oauthGithubCallback)
	mux.HandleFunc("/v1/link-preview", a.linkPreview)
	mux.HandleFunc("/v1/media/presign", a.mediaPresign)
	mux.HandleFunc("/v1/media/complete", a.mediaComplete)
	mux.HandleFunc("/v1/message-delete", a.messageDelete)
	mux.HandleFunc("/v1/conversation-clear", a.conversationClear)
	mux.HandleFunc("/v1/group-member", a.groupMember)
	mux.HandleFunc("/v1/group-join-requests", a.groupJoinRequests)
	mux.HandleFunc("/v1/group-join", a.groupJoin)
	mux.HandleFunc("/v1/devices", a.devices)
	mux.HandleFunc("/v1/me/qr.png", a.meQRPNG)
	mux.HandleFunc("/v1/react", a.react)
	mux.HandleFunc("/v1/favorites", a.favorites)
	mux.HandleFunc("/v1/group-invite-link", a.groupInviteLink)
	mux.HandleFunc("/v1/group-invite.png", a.groupInvitePNG)
	mux.HandleFunc("/v1/group-join-invite", a.groupJoinInvite)
	mux.HandleFunc("/v1/drafts", a.drafts)
	mux.HandleFunc("/v1/pinned", a.pinned)
	mux.HandleFunc("/v1/report", a.report)
	mux.HandleFunc("/v1/settings", a.settings)
	mux.HandleFunc("/v1/auth/forgot", a.forgotPassword)
	mux.HandleFunc("/v1/auth/reset", a.resetPassword)
	mux.HandleFunc("/v1/account-delete", a.deleteAccount)
	mux.HandleFunc("/v1/login-history", a.loginHistory)
	mux.HandleFunc("/v1/group-invite-revoke", a.revokeInvite)
	mux.HandleFunc("/v1/ws", a.ws.handleWS)
	if dir := strings.TrimSpace(a.webDir); dir != "" {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			mux.Handle("/", http.FileServer(http.Dir(dir)))
		}
	}
	return withCORS(mux)
}

func (a *httpAPI) devLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		UID      string `json:"uid"`
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.UID) == "" {
		http.Error(w, `{"error":"uid required"}`, http.StatusBadRequest)
		return
	}
	if a.tooMany(r, "rl:login:"+clientIP(r), 30, time.Minute) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	sess, err := a.issueSession(r.Context(), body.UID, body.DeviceID, devAccessTTL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (a *httpAPI) conversations(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	resp, err := a.core.ListConversations(r.Context(), &imv1.ListConversationsRequest{Uid: uid})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) timeline(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	cid := r.URL.Query().Get("cid")
	if cid == "" {
		http.Error(w, "cid required", http.StatusBadRequest)
		return
	}
	after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	before, _ := strconv.ParseUint(r.URL.Query().Get("before"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	resp, err := a.core.GetTimeline(r.Context(), &imv1.GetTimelineRequest{
		Uid:           uid,
		Cid:           cid,
		AfterConvSeq:  after,
		BeforeConvSeq: before,
		Limit:         uint32(limit),
		Query:         strings.TrimSpace(r.URL.Query().Get("q")),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) friends(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		resp, err := a.core.ListFriends(r.Context(), &imv1.ListFriendsRequest{Uid: uid})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeProtoJSON(w, resp)
	case http.MethodPost:
		var body struct {
			PeerUID string `json:"peer_uid"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.PeerUID) == "" {
			http.Error(w, `{"error":"peer_uid required"}`, http.StatusBadRequest)
			return
		}
		resp, err := a.core.AddFriend(r.Context(), &imv1.AddFriendRequest{Uid: uid, PeerUid: body.PeerUID})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		a.pushRoster(body.PeerUID, uid, "friend_accept", "")
		writeProtoJSON(w, resp)
	case http.MethodDelete:
		var body struct {
			PeerUID string `json:"peer_uid"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.PeerUID) == "" {
			http.Error(w, `{"error":"peer_uid required"}`, http.StatusBadRequest)
			return
		}
		resp, err := a.core.RemoveFriend(r.Context(), &imv1.RemoveFriendRequest{Uid: uid, PeerUid: body.PeerUID})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		a.pushRoster(body.PeerUID, uid, "friend_remove", "")
		writeProtoJSON(w, resp)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *httpAPI) lookupUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.uidFromAuth(w, r); !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q != "" {
		resp, err := a.core.SearchUsers(r.Context(), &imv1.SearchUsersRequest{Query: q, Limit: 20})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		writeProtoJSON(w, resp)
		return
	}
	uid := strings.TrimSpace(r.URL.Query().Get("uid"))
	if uid == "" {
		http.Error(w, "q or uid required", http.StatusBadRequest)
		return
	}
	resp, err := a.core.LookupUser(r.Context(), &imv1.LookupUserRequest{Query: uid})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) groups(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Name    string   `json:"name"`
		Members []string `json:"members"`
		Mode    string   `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		http.Error(w, `{"error":"name required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.CreateGroup(r.Context(), &imv1.CreateGroupRequest{
		OwnerUid: uid, Name: body.Name, MemberUids: body.Members, Mode: body.Mode,
	})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	for _, peer := range body.Members {
		a.pushRoster(peer, uid, "group_invite", resp.GetCid())
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) groupInvite(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		CID     string   `json:"cid"`
		Members []string `json:"members"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.CID) == "" {
		http.Error(w, `{"error":"cid required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.InviteGroup(r.Context(), &imv1.InviteGroupRequest{
		OperatorUid: uid, Cid: body.CID, MemberUids: body.Members,
	})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	for _, peer := range body.Members {
		a.pushRoster(peer, uid, "group_invite", body.CID)
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) groupKick(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		CID             string  `json:"cid"`
		UID             string  `json:"uid"`
		DeleteMessages  bool    `json:"delete_messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CID == "" || body.UID == "" {
		http.Error(w, `{"error":"cid and uid required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.KickGroup(r.Context(), &imv1.KickGroupRequest{
		OperatorUid: uid, Cid: body.CID, MemberUid: body.UID, DeleteMessages: body.DeleteMessages,
	})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) groupUpdate(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		CID          string  `json:"cid"`
		Name         string  `json:"name"`
		AvatarURL    string  `json:"avatar_url"`
		Announcement *string `json:"announcement"`
		JoinApproval *bool   `json:"join_approval"`
		Mode         *string `json:"mode"`
		HistoryDays  *int32  `json:"history_days"`
		AnnounceAck  *bool   `json:"announce_ack"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.CID) == "" {
		http.Error(w, `{"error":"cid required"}`, http.StatusBadRequest)
		return
	}
	req := &imv1.UpdateGroupRequest{OperatorUid: uid, Cid: body.CID, Name: body.Name, AvatarUrl: body.AvatarURL}
	if body.Announcement != nil {
		req.Announcement = *body.Announcement
		req.SetAnnouncement = true
	}
	if body.JoinApproval != nil {
		req.JoinApproval = *body.JoinApproval
		req.SetJoinApproval = true
	}
	if body.Mode != nil {
		req.Mode = *body.Mode
		req.SetMode = true
	}
	if body.HistoryDays != nil {
		req.HistoryDays = *body.HistoryDays
		req.SetHistoryDays = true
	}
	if body.AnnounceAck != nil {
		req.AnnounceAck = *body.AnnounceAck
		req.SetAnnounceAck = true
	}
	resp, err := a.core.UpdateGroup(r.Context(), req)
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) mute(w http.ResponseWriter, r *http.Request) {
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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.CID) == "" {
		http.Error(w, `{"error":"cid required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.SetMute(r.Context(), &imv1.SetMuteRequest{Uid: uid, Cid: body.CID, Muted: body.Muted})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func (a *httpAPI) groupGet(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	cid := strings.TrimSpace(r.URL.Query().Get("cid"))
	if cid == "" {
		http.Error(w, "cid required", http.StatusBadRequest)
		return
	}
	resp, err := a.core.GetGroup(r.Context(), &imv1.GetGroupRequest{Uid: uid, Cid: cid})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoJSON(w, resp)
}

func writeRPCError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.InvalidArgument:
			code = http.StatusBadRequest
		case codes.PermissionDenied:
			code = http.StatusForbidden
		case codes.Unauthenticated:
			code = http.StatusUnauthorized
		}
		http.Error(w, st.Message(), code)
		return
	}
	http.Error(w, err.Error(), code)
}

func (a *httpAPI) uidFromAuth(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return "", false
	}
	claims, err := auth.Parse(a.secret, raw)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return "", false
	}
	return claims.Subject, true
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *httpAPI) pushRoster(toUID, fromUID, kind, extra string) {
	if a.ws == nil || a.ws.hub == nil {
		return
	}
	a.ws.hub.pushRoster(toUID, fromUID, kind, extra)
}

func writeProtoJSON(w http.ResponseWriter, m proto.Message) {
	b, err := protojson.Marshal(m)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
