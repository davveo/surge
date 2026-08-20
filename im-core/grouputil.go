package main

import "errors"

var errNotAdmin = errors.New("not group admin")

func roleRank(role string) int {
	switch role {
	case "owner":
		return 3
	case "admin":
		return 2
	default:
		return 1
	}
}

func memberOf(g *groupInfo, uid string) *groupMember {
	if g == nil {
		return nil
	}
	for i := range g.Members {
		if g.Members[i].UID == uid {
			return &g.Members[i]
		}
	}
	return nil
}

func isOwner(g *groupInfo, uid string) bool {
	return g != nil && g.OwnerUID == uid
}

func isManager(g *groupInfo, uid string) bool {
	m := memberOf(g, uid)
	return m != nil && roleRank(m.Role) >= 2
}

func canSpeak(g *groupInfo, uid string) error {
	if g == nil {
		return errInvalid
	}
	m := memberOf(g, uid)
	if m == nil {
		return errNotMember
	}
	if m.Role == "owner" {
		return nil
	}
	if m.Muted {
		return errMutedAll
	}
	if g.MutedAll && m.Role != "admin" {
		return errMutedAll
	}
	return nil
}

func canKick(g *groupInfo, op, target string) error {
	om := memberOf(g, op)
	tm := memberOf(g, target)
	if om == nil {
		return errNotMember
	}
	if tm == nil {
		return errInvalid
	}
	if roleRank(om.Role) < 2 {
		return errNotAdmin
	}
	if roleRank(om.Role) <= roleRank(tm.Role) {
		return errNotOwner
	}
	return nil
}
