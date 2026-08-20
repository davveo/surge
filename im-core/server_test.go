package main

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
	if _, err := st.UpdateGroup(ctx, "u2", g.CID, "new", ""); err == nil {
		t.Fatal("member should not rename")
	}
	out, err := st.UpdateGroup(ctx, "u1", g.CID, "new", "http://x/a.png")
	if err != nil || out.Name != "new" || out.AvatarURL == "" {
		t.Fatalf("update %+v %v", out, err)
	}
}
