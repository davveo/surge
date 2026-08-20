package main

import (
	"context"
	"testing"

	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

func TestSendIdempotentAndTimeline(t *testing.T) {
	st := newMemoryStore(newMemSeq())
	ctx := context.Background()
	text := &imv1.Payload{Type: imv1.Payload_TEXT, Text: "hello"}

	a, err := st.Send(ctx, "u1", "c1", "", "u2", text)
	if err != nil {
		t.Fatal(err)
	}
	if a.ack.ConvSeq != 1 || a.ack.Duplicate || a.peerUID != "u2" {
		t.Fatalf("first send: %+v", a.ack)
	}
	if a.peerPush == nil || a.peerPush.SyncSeq != 1 {
		t.Fatalf("peer push: %+v", a.peerPush)
	}

	b, err := st.Send(ctx, "u1", "c1", "", "u2", text)
	if err != nil {
		t.Fatal(err)
	}
	if !b.ack.Duplicate || b.ack.MsgId != a.ack.MsgId || b.ack.ConvSeq != 1 {
		t.Fatalf("dup: %+v", b.ack)
	}

	c, err := st.Send(ctx, "u2", "c2", a.ack.Cid, "", &imv1.Payload{Type: imv1.Payload_TEXT, Text: "world"})
	if err != nil {
		t.Fatal(err)
	}
	if c.ack.ConvSeq != 2 {
		t.Fatalf("reply seq=%d", c.ack.ConvSeq)
	}

	_, msgs, err := st.Timeline(ctx, "u1", a.ack.Cid, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Payload.Text != "hello" || msgs[1].Payload.Text != "world" {
		t.Fatalf("timeline: %+v", msgs)
	}

	sync, err := st.Sync(ctx, "u2", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sync.Events) != 2 {
		t.Fatalf("sync: %+v", sync)
	}

	wm, err := st.Watermark(ctx, "u2")
	if err != nil {
		t.Fatal(err)
	}
	if wm != sync.LastSyncSeq {
		t.Fatalf("watermark %d vs %d", wm, sync.LastSyncSeq)
	}

	list, err := st.ListConversations(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Unread != 0 {
		t.Fatalf("convs after timeline: %+v", list)
	}
}

func TestRejectSelfChat(t *testing.T) {
	st := newMemoryStore(newMemSeq())
	_, err := st.Send(context.Background(), "u1", "c1", "", "u1", &imv1.Payload{Type: imv1.Payload_TEXT, Text: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
}
