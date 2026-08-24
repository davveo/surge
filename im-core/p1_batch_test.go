package main

import (
	"context"
	"testing"

	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

func TestAreFriendsMany(t *testing.T) {
	st := newMemoryStore(newMemSeq())
	ctx := context.Background()
	if _, err := st.AddFriend(ctx, "u1", "u2"); err != nil {
		t.Fatal(err)
	}
	got, err := st.AreFriendsMany(ctx, "u1", []string{"u2", "u3", "filehelper"})
	if err != nil {
		t.Fatal(err)
	}
	if !got["u2"] {
		t.Fatal("u2 should be friend")
	}
	if got["u3"] {
		t.Fatal("u3 should not be friend")
	}
	if !got["filehelper"] {
		t.Fatal("filehelper should pass friend check")
	}
}

func TestHideLastSeenBatch(t *testing.T) {
	st := newMemoryStore(newMemSeq())
	ctx := context.Background()
	if _, err := st.SetSettings(ctx, &imv1.UserSettings{Uid: "u2", HideLastSeen: true}); err != nil {
		t.Fatal(err)
	}
	got, err := st.HideLastSeenMap(ctx, []string{"u2", "u3"})
	if err != nil {
		t.Fatal(err)
	}
	if !got["u2"] || got["u3"] {
		t.Fatalf("hide map %+v", got)
	}
	srv := newServer(st, nil)
	resp, err := srv.GetSettingsBatch(ctx, &imv1.GetProfilesRequest{Uids: []string{"u2", "u3"}})
	if err != nil {
		t.Fatal(err)
	}
	hide := map[string]bool{}
	for _, s := range resp.GetSettings() {
		hide[s.GetUid()] = s.GetHideLastSeen()
	}
	if !hide["u2"] || hide["u3"] {
		t.Fatalf("batch %+v", hide)
	}
}
