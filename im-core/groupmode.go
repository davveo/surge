package main

import (
	"crypto/sha256"
	"encoding/binary"
	"strconv"
	"strings"

	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

const (
	groupModeNormal     = "normal"
	groupModeVerify     = "verify"
	groupModePrivate    = "private"
	groupModeBroadcast  = "broadcast"
	groupModeAnonymous  = "anonymous"
	groupModeEphemeral  = "ephemeral"
)

func normalizeGroupMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case groupModeVerify, "approval", "join":
		return groupModeVerify
	case groupModePrivate, "secret":
		return groupModePrivate
	case groupModeBroadcast, "notice", "muted":
		return groupModeBroadcast
	case groupModeAnonymous, "anon":
		return groupModeAnonymous
	case groupModeEphemeral, "burn":
		return groupModeEphemeral
	default:
		return groupModeNormal
	}
}

func applyGroupMode(g *groupInfo, raw string) {
	if g == nil {
		return
	}
	g.Mode = normalizeGroupMode(raw)
	g.JoinApproval = g.Mode == groupModeVerify
	g.MutedAll = g.Mode == groupModeBroadcast
}

func canInvite(g *groupInfo, uid string) error {
	if g == nil {
		return errInvalid
	}
	if memberOf(g, uid) == nil {
		return errNotMember
	}
	if g.Mode == groupModePrivate && !isOwner(g, uid) {
		return errNotOwner
	}
	return nil
}

func applyEphemeralMode(g *groupInfo, payload *imv1.Payload) {
	if g == nil || payload == nil || g.Mode != groupModeEphemeral {
		return
	}
	if payload.Type == imv1.Payload_SYSTEM {
		return
	}
	payload.Ephemeral = true
}

func anonLabel(cid, uid string) string {
	sum := sha256.Sum256([]byte(cid + "|" + uid))
	n := binary.BigEndian.Uint32(sum[:4])%9000 + 1000
	return "匿名" + strconv.Itoa(int(n))
}

func groupPeerProfile(g *groupInfo) *imv1.UserProfile {
	if g == nil {
		return nil
	}
	mode := g.Mode
	if mode == "" {
		mode = groupModeNormal
	}
	return &imv1.UserProfile{AvatarUrl: g.AvatarURL, Email: "grp:" + mode}
}
