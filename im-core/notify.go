package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/davveo/surge/pkg/mail"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

const notifyQueue = 256
const notifyWorkers = 4
const notifyTimeout = 8 * time.Second

type notifyJob struct {
	uid     string
	preview string
}

type mailer struct {
	smtp    mail.Config
	smsHook string
	store   Store
	client  *http.Client
	jobs    chan notifyJob
}

func newMailer(store Store) *mailer {
	m := &mailer{
		smtp:    mail.FromEnv(),
		smsHook: strings.TrimSpace(os.Getenv("SMS_WEBHOOK")),
		store:   store,
		client:  &http.Client{Timeout: 5 * time.Second},
		jobs:    make(chan notifyJob, notifyQueue),
	}
	if m.enabled() {
		for i := 0; i < notifyWorkers; i++ {
			go m.loop()
		}
	}
	return m
}

func (m *mailer) enabled() bool {
	if m == nil {
		return false
	}
	return (m.smtp.Host != "" && m.smtp.From != "") || m.smsHook != ""
}

func (s *server) notifyOffline(_ context.Context, uid string, gp *imv1.GatewayPush) {
	if s.notify == nil || gp == nil || gp.Push == nil {
		return
	}
	s.notify.NotifyOffline(uid, notifyPreview(gp.Push.GetPayload()))
}

func (m *mailer) NotifyOffline(uid, preview string) {
	if m == nil || uid == "" || !m.enabled() {
		return
	}
	job := notifyJob{uid: uid, preview: preview}
	select {
	case m.jobs <- job:
	default:
		log.Printf("notify queue full, drop uid=%s", uid)
	}
}

func (m *mailer) loop() {
	for job := range m.jobs {
		ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
		m.deliver(ctx, job.uid, job.preview)
		cancel()
	}
}

func (m *mailer) deliver(ctx context.Context, uid, preview string) {
	if m.store == nil {
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
	if p.Email != "" && m.smtp.Host != "" && m.smtp.From != "" {
		if err := mail.Send(m.smtp, p.Email, "Surge 新消息", body); err != nil {
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
