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
}

func (a *httpAPI) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/v1/auth/dev-login", a.devLogin)
	mux.HandleFunc("/v1/auth/qr/new", a.qrNew)
	mux.HandleFunc("/v1/auth/qr/status", a.qrStatus)
	mux.HandleFunc("/v1/auth/qr.png", a.qrPNG)
	mux.HandleFunc("/v1/auth/qr/approve", a.qrApprove)
	mux.HandleFunc("/v1/conversations", a.conversations)
	mux.HandleFunc("/v1/timeline", a.timeline)
	mux.HandleFunc("/v1/friends", a.friends)
	mux.HandleFunc("/v1/users", a.lookupUser)
	mux.HandleFunc("/v1/groups", a.groups)
	mux.HandleFunc("/v1/group-invite", a.groupInvite)
	mux.HandleFunc("/v1/group-kick", a.groupKick)
	mux.HandleFunc("/v1/group", a.groupGet)
	mux.HandleFunc("/v1/media/presign", a.mediaPresign)
	mux.HandleFunc("/v1/media/complete", a.mediaComplete)
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
	token, err := auth.Issue(a.secret, body.UID, body.DeviceID, 7*24*time.Hour)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"uid": body.UID, "access_token": token})
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
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	resp, err := a.core.GetTimeline(r.Context(), &imv1.GetTimelineRequest{
		Uid:          uid,
		Cid:          cid,
		AfterConvSeq: after,
		Limit:        uint32(limit),
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
		writeProtoJSON(w, resp)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *httpAPI) lookupUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.uidFromAuth(w, r); !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("uid"))
	if q == "" {
		q = strings.TrimSpace(r.URL.Query().Get("q"))
	}
	resp, err := a.core.LookupUser(r.Context(), &imv1.LookupUserRequest{Query: q})
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
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		http.Error(w, `{"error":"name required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.CreateGroup(r.Context(), &imv1.CreateGroupRequest{
		OwnerUid: uid, Name: body.Name, MemberUids: body.Members,
	})
	if err != nil {
		writeRPCError(w, err)
		return
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
		CID string `json:"cid"`
		UID string `json:"uid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CID == "" || body.UID == "" {
		http.Error(w, `{"error":"cid and uid required"}`, http.StatusBadRequest)
		return
	}
	resp, err := a.core.KickGroup(r.Context(), &imv1.KickGroupRequest{
		OperatorUid: uid, Cid: body.CID, MemberUid: body.UID,
	})
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
