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
