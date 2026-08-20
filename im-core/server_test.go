package main

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/davveo/surge/pkg/route"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

func TestSendRequiresFriend(t *testing.T) {
	st := newMemoryStore(newMemSeq())
	srv := newServer(st, nil)
	ctx := context.Background()
	req := &imv1.SendMessageRequest{
		FromUid:     "u1",
		ClientMsgId: "m1",
		PeerUid:     "u2",
		Payload:     &imv1.Payload{Type: imv1.Payload_TEXT, Text: "hi"},
	}
	_, err := srv.Send(ctx, req)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("want PermissionDenied, got %v", err)
	}
	if _, err := srv.AddFriend(ctx, &imv1.AddFriendRequest{Uid: "u1", PeerUid: "u2"}); err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Send(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetAck().GetCid() != "p2p:u1:u2" {
		t.Fatalf("ack %+v", resp.GetAck())
	}
}

func TestGroupSendAndRecall(t *testing.T) {
	st := newMemoryStore(newMemSeq())
	srv := newServer(st, nil)
	ctx := context.Background()
	if _, err := srv.AddFriend(ctx, &imv1.AddFriendRequest{Uid: "u1", PeerUid: "u2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.AddFriend(ctx, &imv1.AddFriendRequest{Uid: "u1", PeerUid: "u3"}); err != nil {
		t.Fatal(err)
	}
	g, err := srv.CreateGroup(ctx, &imv1.CreateGroupRequest{OwnerUid: "u1", Name: "t", MemberUids: []string{"u2", "u3"}})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Send(ctx, &imv1.SendMessageRequest{
		FromUid: "u1", ClientMsgId: "g1", Cid: g.Cid,
		Payload: &imv1.Payload{Type: imv1.Payload_TEXT, Text: "hello group"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetAck().GetCid() != g.Cid {
		t.Fatalf("cid %s", resp.GetAck().GetCid())
	}
	sync, err := st.Sync(ctx, "u3", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sync.Events) == 0 {
		t.Fatal("u3 got no inbox")
	}
	_, err = srv.Recall(ctx, &imv1.RecallMessageRequest{Uid: "u1", Cid: g.Cid, MsgId: resp.GetAck().GetMsgId()})
	if err != nil {
		t.Fatal(err)
	}
	_, msgs, err := st.Timeline(ctx, "u2", g.Cid, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range msgs {
		if m.MsgId == resp.GetAck().GetMsgId() && m.Recalled {
			found = true
		}
	}
	if !found {
		t.Fatalf("want recalled msg %s in %+v", resp.GetAck().GetMsgId(), msgs)
	}
}

func TestGroupMemberEvents(t *testing.T) {
	st := newMemoryStore(newMemSeq())
	srv := newServer(st, nil)
	ctx := context.Background()
	if _, err := srv.AddFriend(ctx, &imv1.AddFriendRequest{Uid: "u1", PeerUid: "u2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.AddFriend(ctx, &imv1.AddFriendRequest{Uid: "u1", PeerUid: "u3"}); err != nil {
		t.Fatal(err)
	}
	g, err := srv.CreateGroup(ctx, &imv1.CreateGroupRequest{OwnerUid: "u1", Name: "t", MemberUids: []string{"u2"}})
	if err != nil {
		t.Fatal(err)
	}
	sync, err := st.Sync(ctx, "u2", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(sync.Events) == 0 || sync.Events[0].Payload.Type != imv1.Payload_SYSTEM {
		t.Fatalf("create should fanout system event: %+v", sync.Events)
	}
	if _, err := srv.InviteGroup(ctx, &imv1.InviteGroupRequest{OperatorUid: "u1", Cid: g.Cid, MemberUids: []string{"u3"}}); err != nil {
		t.Fatal(err)
	}
	sync3, err := st.Sync(ctx, "u3", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(sync3.Events) == 0 {
		t.Fatal("invite should write inbox for new member")
	}
	if _, err := srv.KickGroup(ctx, &imv1.KickGroupRequest{OperatorUid: "u1", Cid: g.Cid, MemberUid: "u3"}); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListConversations(ctx, "u3")
	if err != nil || len(list) == 0 {
		t.Fatalf("kicked user should keep conversation: %v %+v", err, list)
	}
}

func TestRecallWindow(t *testing.T) {
	st := newMemoryStore(newMemSeq())
	ctx := context.Background()
	if _, err := st.AddFriend(ctx, "u1", "u2"); err != nil {
		t.Fatal(err)
	}
	res, err := st.Send(ctx, "u1", "c1", "", "u2", &imv1.Payload{Type: imv1.Payload_TEXT, Text: "old"}, "")
	if err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	st.byID[res.ack.MsgId].createdAt = 1
	st.mu.Unlock()
	_, _, err = st.Recall(ctx, "u1", res.ack.Cid, res.ack.MsgId)
	if err == nil {
		t.Fatal("expected recall window error")
	}
}

func TestCreateGroupRequiresFriend(t *testing.T) {
	st := newMemoryStore(newMemSeq())
	_, err := st.CreateGroup(context.Background(), "u1", "g", []string{"u2"})
	if err == nil {
		t.Fatal("expected not friends")
	}
}

func TestAddFriendIdempotent(t *testing.T) {
	st := newMemoryStore(newMemSeq())
	ctx := context.Background()
	a, err := st.AddFriend(ctx, "u1", "u2")
	if err != nil || a {
		t.Fatalf("first already=%v err=%v", a, err)
	}
	b, err := st.AddFriend(ctx, "u1", "u2")
	if err != nil || !b {
		t.Fatalf("second already=%v err=%v", b, err)
	}
	ok, err := st.AreFriends(ctx, "u2", "u1")
	if err != nil || !ok {
		t.Fatal("not mutual")
	}
}

func TestRegisterAndSearch(t *testing.T) {
	st := newMemoryStore(newMemSeq())
	srv := newServer(st, nil)
	ctx := context.Background()
	if _, err := srv.Register(ctx, &imv1.RegisterRequest{Uid: "alice", Password: "secret1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.VerifyPassword(ctx, &imv1.LoginRequest{Uid: "alice", Password: "wrong"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("want unauthenticated, got %v", err)
	}
	if _, err := srv.VerifyPassword(ctx, &imv1.LoginRequest{Uid: "alice", Password: "secret1"}); err != nil {
		t.Fatal(err)
	}
	lu, err := srv.LookupUser(ctx, &imv1.LookupUserRequest{Query: "nobody"})
	if err != nil || lu.Found {
		t.Fatalf("lookup nobody: %+v %v", lu, err)
	}
	found, err := srv.LookupUser(ctx, &imv1.LookupUserRequest{Query: "alice"})
	if err != nil || !found.Found {
		t.Fatalf("lookup alice: %+v %v", found, err)
	}
	sr, err := srv.SearchUsers(ctx, &imv1.SearchUsersRequest{Query: "ali"})
	if err != nil || len(sr.Users) == 0 {
		t.Fatalf("search: %+v %v", sr, err)
	}
}

func TestQuoteMentionsAndMute(t *testing.T) {
	st := newMemoryStore(newMemSeq())
	ctx := context.Background()
	if _, err := st.AddFriend(ctx, "u1", "u2"); err != nil {
		t.Fatal(err)
	}
	first, err := st.Send(ctx, "u1", "q1", "", "u2", &imv1.Payload{Type: imv1.Payload_TEXT, Text: "origin line"}, "")
	if err != nil {
		t.Fatal(err)
	}
	quoted, err := st.Send(ctx, "u2", "q2", "", "u1", &imv1.Payload{Type: imv1.Payload_TEXT, Text: "reply @u1 see"}, first.ack.MsgId)
	if err != nil {
		t.Fatal(err)
	}
	_, msgs, err := st.Timeline(ctx, "u1", first.ack.Cid, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var hit *imv1.TimelineMessage
	for _, m := range msgs {
		if m.MsgId == quoted.ack.MsgId {
			hit = m
		}
	}
	if hit == nil || hit.Payload.QuoteText != "origin line" {
		t.Fatalf("quote text %+v", hit)
	}
	if len(hit.Payload.MentionUids) == 0 || hit.Payload.MentionUids[0] != "u1" {
		t.Fatalf("mentions %+v", hit.Payload.MentionUids)
	}
	if err := st.SetMute(ctx, "u1", first.ack.Cid, true); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListConversations(ctx, "u1")
	if err != nil || len(list) == 0 || !list[0].Muted {
		t.Fatalf("muted conv %+v %v", list, err)
	}
}

func TestFriendRequestBlockAndHide(t *testing.T) {
	st := newMemoryStore(newMemSeq())
	srv := newServer(st, nil)
	ctx := context.Background()
	stt, err := srv.RequestFriend(ctx, &imv1.AddFriendRequest{Uid: "u1", PeerUid: "u2"})
	if err != nil || stt.Status != "pending" {
		t.Fatalf("request %+v %v", stt, err)
	}
	in, out, err := st.ListFriendRequests(ctx, "u2")
	if err != nil || len(in) != 1 || in[0][0] != "u1" || len(out) != 0 {
		t.Fatalf("incoming %+v outgoing %+v %v", in, out, err)
	}
	if _, err := srv.DeclineFriend(ctx, &imv1.AddFriendRequest{Uid: "u2", PeerUid: "u1"}); err != nil {
		t.Fatal(err)
	}
	ok, _ := st.AreFriends(ctx, "u1", "u2")
	if ok {
		t.Fatal("decline should not add friends")
	}
	if _, err := srv.RequestFriend(ctx, &imv1.AddFriendRequest{Uid: "u1", PeerUid: "u2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.AcceptFriend(ctx, &imv1.AddFriendRequest{Uid: "u2", PeerUid: "u1"}); err != nil {
		t.Fatal(err)
	}
	ok, _ = st.AreFriends(ctx, "u1", "u2")
	if !ok {
		t.Fatal("expected friends after accept")
	}
	if _, err := srv.SetRemark(ctx, &imv1.SetRemarkRequest{Uid: "u1", PeerUid: "u2", Remark: "小二"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetRemark(ctx, "u1", "u2"); got != "小二" {
		t.Fatalf("remark %q", got)
	}
	if _, err := srv.BlockUser(ctx, &imv1.BlockUserRequest{Uid: "u2", PeerUid: "u1"}); err != nil {
		t.Fatal(err)
	}
	_, err = srv.Send(ctx, &imv1.SendMessageRequest{
		FromUid: "u1", ClientMsgId: "b1", PeerUid: "u2",
		Payload: &imv1.Payload{Type: imv1.Payload_TEXT, Text: "nope"},
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("want blocked, got %v", err)
	}
	blocks, err := st.ListBlocks(ctx, "u2")
	if err != nil || len(blocks) != 1 || blocks[0] != "u1" {
		t.Fatalf("blocks %+v %v", blocks, err)
	}
	if _, err := srv.UnblockUser(ctx, &imv1.BlockUserRequest{Uid: "u2", PeerUid: "u1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.AddFriend(ctx, &imv1.AddFriendRequest{Uid: "u1", PeerUid: "u2"}); err != nil {
		t.Fatal(err)
	}
	sent, err := srv.Send(ctx, &imv1.SendMessageRequest{
		FromUid: "u1", ClientMsgId: "b2", PeerUid: "u2",
		Payload: &imv1.Payload{Type: imv1.Payload_TEXT, Text: "ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.HideConversation(ctx, "u1", sent.GetAck().GetCid()); err != nil {
		t.Fatal(err)
	}
	list, _ := st.ListConversations(ctx, "u1")
	if len(list) != 0 {
		t.Fatalf("hidden still listed %+v", list)
	}
	if _, err := srv.RemoveFriend(ctx, &imv1.RemoveFriendRequest{Uid: "u1", PeerUid: "u2"}); err != nil {
		t.Fatal(err)
	}
	ok, _ = st.AreFriends(ctx, "u1", "u2")
	if ok {
		t.Fatal("expected unfriended")
	}
}

func TestLeaveAndPin(t *testing.T) {
	st := newMemoryStore(newMemSeq())
	srv := newServer(st, nil)
	ctx := context.Background()
	if _, err := st.AddFriend(ctx, "u1", "u2"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddFriend(ctx, "u1", "u3"); err != nil {
		t.Fatal(err)
	}
	g, err := st.CreateGroup(ctx, "u1", "g", []string{"u2", "u3"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.LeaveGroup(ctx, "u1", g.CID); err == nil {
		t.Fatal("owner must transfer before leave")
	}
	if err := st.DissolveGroup(ctx, "u2", g.CID); err == nil {
		t.Fatal("only owner can dissolve")
	}
	if _, err := st.LeaveGroup(ctx, "u2", g.CID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPin(ctx, "u1", g.CID, true); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListConversations(ctx, "u1")
	if err != nil || len(list) == 0 || !list[0].Pinned {
		t.Fatalf("pin %+v %v", list, err)
	}
	sent, err := srv.Send(ctx, &imv1.SendMessageRequest{
		FromUid: "u1", ClientMsgId: "r1", Cid: g.CID,
		Payload: &imv1.Payload{Type: imv1.Payload_TEXT, Text: "hi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	seq := sent.GetAck().GetConvSeq()
	n, members, readers, err := st.GetReadState(ctx, "u1", g.CID, seq)
	if err != nil || n != 0 || members != 2 {
		t.Fatalf("read before peer %+v n=%d members=%d %v", readers, n, members, err)
	}
	if err := st.MarkRead(ctx, "u3", g.CID, seq); err != nil {
		t.Fatal(err)
	}
	n, members, readers, err = st.GetReadState(ctx, "u1", g.CID, seq)
	if err != nil || n != 1 || members != 2 || (len(readers) != 1 || readers[0] != "u3") {
		t.Fatalf("read after peer n=%d members=%d readers=%+v %v", n, members, readers, err)
	}
	if _, err := st.TransferOwner(ctx, "u1", g.CID, "u3"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LeaveGroup(ctx, "u1", g.CID); err != nil {
		t.Fatal(err)
	}
	if err := st.HideConversation(ctx, "u3", g.CID); err != nil {
		t.Fatal(err)
	}
	list, _ = st.ListConversations(ctx, "u3")
	if len(list) != 0 {
		t.Fatalf("hidden still listed %+v", list)
	}
	if err := st.DissolveGroup(ctx, "u3", g.CID); err != nil {
		t.Fatal(err)
	}
}

func TestTimelineQuerySearchAndPage(t *testing.T) {
	st := newMemoryStore(newMemSeq())
	ctx := context.Background()
	if _, err := st.AddFriend(ctx, "u1", "u2"); err != nil {
		t.Fatal(err)
	}
	var last uint64
	for i := 0; i < 5; i++ {
		res, err := st.Send(ctx, "u1", "m"+string(rune('a'+i)), "", "u2", &imv1.Payload{Type: imv1.Payload_TEXT, Text: "msg-" + string(rune('a'+i))}, "")
		if err != nil {
			t.Fatal(err)
		}
		last = res.ack.ConvSeq
	}
	cid, msgs, hasMore, err := st.TimelineQuery(ctx, "u1", "p2p:u1:u2", 0, 0, 3, "")
	if err != nil || cid == "" || !hasMore || len(msgs) != 3 || msgs[len(msgs)-1].ConvSeq != last {
		t.Fatalf("latest page %+v hasMore=%v %v", msgs, hasMore, err)
	}
	_, older, olderMore, err := st.TimelineQuery(ctx, "u1", "p2p:u1:u2", 0, msgs[0].ConvSeq, 10, "")
	if err != nil || len(older) != 2 || olderMore {
		t.Fatalf("older %+v more=%v %v", older, olderMore, err)
	}
	_, hit, _, err := st.TimelineQuery(ctx, "u1", "p2p:u1:u2", 0, 0, 10, "msg-c")
	if err != nil || len(hit) != 1 || hit[0].Payload.GetText() != "msg-c" {
		t.Fatalf("search %+v %v", hit, err)
	}
}

func TestUpdateGroupOwnerOnly(t *testing.T) {
	st := newMemoryStore(newMemSeq())
	ctx := context.Background()
	if _, err := st.AddFriend(ctx, "u1", "u2"); err != nil {
		t.Fatal(err)
	}
	g, err := st.CreateGroup(ctx, "u1", "old", []string{"u2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateGroup(ctx, "u2", g.CID, "new", "", "", false, nil); err == nil {
		t.Fatal("member should not rename")
	}
	out, err := st.UpdateGroup(ctx, "u1", g.CID, "new", "http://x/a.png", "", false, nil)
	if err != nil || out.Name != "new" || out.AvatarURL == "" {
		t.Fatalf("update %+v %v", out, err)
	}
}

func TestEmailLoginMuteAllEphemeralSearchTags(t *testing.T) {
	st := newMemoryStore(newMemSeq())
	srv := newServer(st, nil)
	ctx := context.Background()
	p, err := srv.Register(ctx, &imv1.RegisterRequest{Password: "secret1", Email: "alice@x.com"})
	if err != nil || p.Uid == "" {
		t.Fatalf("register %+v %v", p, err)
	}
	if _, err := st.Register(ctx, "u2", "secret1"); err != nil {
		t.Fatal(err)
	}
	got, err := srv.VerifyPassword(ctx, &imv1.LoginRequest{Uid: "alice@x.com", Password: "secret1"})
	if err != nil || got.GetUid() != p.Uid {
		t.Fatalf("login email %+v %v", got, err)
	}
	if _, err := st.AddFriend(ctx, p.Uid, "u2"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFriendTags(ctx, p.Uid, "u2", []string{"同事"}); err != nil {
		t.Fatal(err)
	}
	tags, _ := st.FriendTagsOf(ctx, p.Uid, "u2")
	if len(tags) != 1 || tags[0] != "同事" {
		t.Fatalf("tags %v", tags)
	}
	g, err := st.CreateGroup(ctx, p.Uid, "g", []string{"u2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetGroupMuteAll(ctx, p.Uid, g.CID, true); err != nil {
		t.Fatal(err)
	}
	_, err = srv.Send(ctx, &imv1.SendMessageRequest{
		FromUid: "u2", ClientMsgId: "m1", Cid: g.CID,
		Payload: &imv1.Payload{Type: imv1.Payload_TEXT, Text: "nope"},
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("want muted, got %v", err)
	}
	ack, err := srv.Send(ctx, &imv1.SendMessageRequest{
		FromUid: p.Uid, ClientMsgId: "m2", PeerUid: "u2",
		Payload: &imv1.Payload{Type: imv1.Payload_TEXT, Text: "secret-hello", Ephemeral: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.ConsumeEphemeral(ctx, &imv1.RecallMessageRequest{Uid: "u2", Cid: ack.Ack.Cid, MsgId: ack.Ack.MsgId}); err != nil {
		t.Fatal(err)
	}
	hits, err := st.SearchMessages(ctx, p.Uid, "secret-hello", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("burned still searchable %+v", hits)
	}
	_, err = srv.Send(ctx, &imv1.SendMessageRequest{
		FromUid: p.Uid, ClientMsgId: "m3", PeerUid: "u2",
		Payload: &imv1.Payload{Type: imv1.Payload_TEXT, Text: "find-me"},
	})
	if err != nil {
		t.Fatal(err)
	}
	hits, err = st.SearchMessages(ctx, p.Uid, "find-me", 10)
	if err != nil || len(hits) == 0 {
		t.Fatalf("search %+v %v", hits, err)
	}
}

type stubRouter struct {
	pubs []*imv1.GatewayPush
}

func (r *stubRouter) Lookup(ctx context.Context, uid string) (*route.Record, error) {
	all, err := r.LookupAll(ctx, uid)
	if err != nil || len(all) == 0 {
		return nil, err
	}
	rec := all[0]
	return &rec, nil
}

func (r *stubRouter) LookupAll(_ context.Context, uid string) ([]route.Record, error) {
	return []route.Record{{GatewayID: "gw", ConnID: "c-" + uid}}, nil
}

func (r *stubRouter) Publish(_ context.Context, _ string, push *imv1.GatewayPush) error {
	r.pubs = append(r.pubs, proto.Clone(push).(*imv1.GatewayPush))
	return nil
}

func rosterHits(pubs []*imv1.GatewayPush, uid, kind string) int {
	n := 0
	for _, gp := range pubs {
		p := gp.GetPush()
		if p == nil || gp.GetUid() != uid || p.GetCid() != rosterCID {
			continue
		}
		text := ""
		if p.GetPayload() != nil {
			text = p.GetPayload().GetText()
		}
		if text == kind || strings.HasPrefix(text, kind+" ") {
			n++
		}
	}
	return n
}

func TestRosterNotifyOnFriendAndInvite(t *testing.T) {
	st := newMemoryStore(newMemSeq())
	rt := &stubRouter{}
	srv := newServer(st, rt)
	ctx := context.Background()

	if _, err := srv.RequestFriend(ctx, &imv1.AddFriendRequest{Uid: "u1", PeerUid: "u2"}); err != nil {
		t.Fatal(err)
	}
	if rosterHits(rt.pubs, "u2", "friend_request") != 1 {
		t.Fatalf("want friend_request to u2, got %+v", rt.pubs)
	}

	if _, err := srv.AcceptFriend(ctx, &imv1.AddFriendRequest{Uid: "u2", PeerUid: "u1"}); err != nil {
		t.Fatal(err)
	}
	if rosterHits(rt.pubs, "u1", "friend_accept") != 1 {
		t.Fatalf("want friend_accept to u1, got %+v", rt.pubs)
	}

	if _, err := srv.AddFriend(ctx, &imv1.AddFriendRequest{Uid: "u1", PeerUid: "u3"}); err != nil {
		t.Fatal(err)
	}
	if rosterHits(rt.pubs, "u3", "friend_accept") != 1 {
		t.Fatalf("want friend_accept to u3, got %+v", rt.pubs)
	}

	g, err := srv.CreateGroup(ctx, &imv1.CreateGroupRequest{OwnerUid: "u1", Name: "t", MemberUids: []string{"u2"}})
	if err != nil {
		t.Fatal(err)
	}
	if rosterHits(rt.pubs, "u2", "group_invite") < 1 {
		t.Fatalf("want group_invite to u2, got %+v", rt.pubs)
	}

	if _, err := srv.InviteGroup(ctx, &imv1.InviteGroupRequest{OperatorUid: "u1", Cid: g.Cid, MemberUids: []string{"u3"}}); err != nil {
		t.Fatal(err)
	}
	if rosterHits(rt.pubs, "u3", "group_invite") != 1 {
		t.Fatalf("want group_invite to u3, got %+v", rt.pubs)
	}
}
