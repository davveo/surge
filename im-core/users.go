package main

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	imv1 "github.com/davveo/surge/proto/gen/im/v1"
	"golang.org/x/crypto/bcrypt"
)

type userRec struct {
	UID          string
	PasswordHash string
	DisplayName  string
	AvatarURL    string
	Email        string
	Phone        string
	PublicKey    string
}

func validUID(uid string) error {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return fmt.Errorf("%w: uid required", errInvalid)
	}
	if len(uid) < 2 || len(uid) > 64 {
		return fmt.Errorf("%w: uid length 2-64", errInvalid)
	}
	for _, r := range uid {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' || r == '@' || r == '+' {
			continue
		}
		return fmt.Errorf("%w: invalid uid character", errInvalid)
	}
	return nil
}

func profileOf(u *userRec) *imv1.UserProfile {
	if u == nil {
		return nil
	}
	name := u.DisplayName
	if name == "" {
		name = u.UID
	}
	return &imv1.UserProfile{Uid: u.UID, DisplayName: name, AvatarUrl: u.AvatarURL, Email: u.Email, Phone: u.Phone, PublicKey: u.PublicKey}
}

func hashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (s *memoryStore) EnsureUser(_ context.Context, uid string) error {
	if err := validUID(uid); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.users[uid] == nil {
		s.users[uid] = &userRec{UID: uid, DisplayName: uid}
	}
	return nil
}

func (s *memoryStore) Register(_ context.Context, uid, password string) (*imv1.UserProfile, error) {
	if err := validUID(uid); err != nil {
		return nil, err
	}
	password = strings.TrimSpace(password)
	if password == "" {
		return nil, fmt.Errorf("%w: password required", errInvalid)
	}
	if len(password) < 6 {
		return nil, fmt.Errorf("%w: password too short", errInvalid)
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.users[uid]
	if cur != nil && cur.PasswordHash != "" {
		return nil, fmt.Errorf("%w: already registered", errInvalid)
	}
	if cur == nil {
		cur = &userRec{UID: uid, DisplayName: uid}
		s.users[uid] = cur
	}
	cur.PasswordHash = hash
	return profileOf(cur), nil
}

func (s *memoryStore) VerifyPassword(_ context.Context, uid, password string) (*imv1.UserProfile, error) {
	if err := validUID(uid); err != nil {
		return nil, err
	}
	s.mu.Lock()
	u := s.users[uid]
	s.mu.Unlock()
	if u == nil || u.PasswordHash == "" {
		return nil, fmt.Errorf("%w: bad credentials", errAuth)
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, fmt.Errorf("%w: bad credentials", errAuth)
	}
	return profileOf(u), nil
}

func (s *memoryStore) GetProfile(_ context.Context, uid string) (*imv1.UserProfile, error) {
	if err := validUID(uid); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[uid]
	if u == nil {
		return nil, fmt.Errorf("%w: user not found", errInvalid)
	}
	return profileOf(u), nil
}

func (s *memoryStore) UpdateProfile(_ context.Context, uid, displayName, avatarURL string) (*imv1.UserProfile, error) {
	if err := validUID(uid); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[uid]
	if u == nil {
		u = &userRec{UID: uid, DisplayName: uid}
		s.users[uid] = u
	}
	if n := strings.TrimSpace(displayName); n != "" {
		u.DisplayName = clipText(n, 64)
	}
	if a := strings.TrimSpace(avatarURL); a != "" {
		u.AvatarURL = clipText(a, 512)
	}
	return profileOf(u), nil
}

func (s *memoryStore) SearchUsers(_ context.Context, query string, limit int) ([]*imv1.UserProfile, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, fmt.Errorf("%w: query required", errInvalid)
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*imv1.UserProfile
	for _, u := range s.users {
		if strings.HasPrefix(strings.ToLower(u.UID), query) || strings.Contains(strings.ToLower(u.DisplayName), query) ||
			strings.Contains(strings.ToLower(u.Email), query) || strings.Contains(u.Phone, query) {
			out = append(out, profileOf(u))
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (s *memoryStore) UpdateGroup(_ context.Context, operatorUID, cid, name, avatarURL, announcement string, setAnnouncement bool, joinApproval *bool) (*groupInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.groups[cid]
	if g == nil {
		return nil, fmt.Errorf("%w: group not found", errInvalid)
	}
	if !isManager(g, operatorUID) {
		return nil, errNotAdmin
	}
	if n := strings.TrimSpace(name); n != "" {
		g.Name = clipText(n, 128)
		for _, convs := range s.convs {
			if c := convs[cid]; c != nil {
				c.Title = g.Name
			}
		}
	}
	if a := strings.TrimSpace(avatarURL); a != "" {
		g.AvatarURL = clipText(a, 512)
	}
	if setAnnouncement {
		g.Announcement = clipText(announcement, 2000)
	}
	if joinApproval != nil {
		if g.OwnerUID != operatorUID {
			return nil, errNotOwner
		}
		g.JoinApproval = *joinApproval
	}
	cp := *g
	cp.Members = append([]groupMember{}, g.Members...)
	return &cp, nil
}

func (s *memoryStore) SetMute(_ context.Context, uid, cid string, muted bool) error {
	uid = strings.TrimSpace(uid)
	cid = strings.TrimSpace(cid)
	if uid == "" || cid == "" {
		return fmt.Errorf("%w: uid and cid required", errInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mutes[uid] == nil {
		s.mutes[uid] = map[string]bool{}
	}
	if muted {
		s.mutes[uid][cid] = true
	} else {
		delete(s.mutes[uid], cid)
	}
	return nil
}

func (s *memoryStore) ListMutes(_ context.Context, uid string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for cid, on := range s.mutes[uid] {
		if on {
			out = append(out, cid)
		}
	}
	return out, nil
}

func (s *memoryStore) lookupQuoteTextLocked(cid, quoteMsgID string) string {
	if quoteMsgID == "" {
		return ""
	}
	row := s.byID[quoteMsgID]
	if row == nil || !containsCID(s.byCID[cid], quoteMsgID) {
		return ""
	}
	return previewOf(row.payload)
}
