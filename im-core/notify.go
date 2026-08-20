package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/smtp"
	"os"
	"strings"
	"time"

	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

type mailer struct {
	host    string
	from    string
	smsHook string
	store   Store
	client  *http.Client
}

func newMailer(store Store) *mailer {
	return &mailer{
		host:    strings.TrimSpace(os.Getenv("SMTP_HOST")),
		from:    strings.TrimSpace(os.Getenv("SMTP_FROM")),
		smsHook: strings.TrimSpace(os.Getenv("SMS_WEBHOOK")),
		store:   store,
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

func (s *server) notifyOffline(ctx context.Context, uid string, gp *imv1.GatewayPush) {
	if s.notify == nil || gp == nil || gp.Push == nil {
		return
	}
	s.notify.NotifyOffline(ctx, uid, previewOf(gp.Push.GetPayload()))
}

func (m *mailer) NotifyOffline(ctx context.Context, uid, preview string) {
	if m == nil || uid == "" {
		return
	}
	p, err := m.store.GetProfile(ctx, uid)
	if err != nil || p == nil {
		return
	}
	body := "你有一条新消息"
	if preview != "" {
		body = preview
	}
	if p.Email != "" && m.host != "" && m.from != "" {
		msg := "From: " + m.from + "\r\nTo: " + p.Email + "\r\nSubject: Surge 新消息\r\n\r\n" + body
		addr := m.host
		if !strings.Contains(addr, ":") {
			addr += ":25"
		}
		if err := smtp.SendMail(addr, nil, m.from, []string{p.Email}, []byte(msg)); err != nil {
			log.Printf("smtp %s: %v", uid, err)
		}
	}
	if p.Phone != "" && m.smsHook != "" {
		form := fmt.Sprintf("phone=%s&text=%s", p.Phone, body)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.smsHook, strings.NewReader(form))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			resp, err := m.client.Do(req)
			if err != nil {
				log.Printf("sms %s: %v", uid, err)
			} else {
				_ = resp.Body.Close()
			}
		}
	}
}
