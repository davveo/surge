package mail

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"
)

type Config struct {
	Host       string
	From       string
	Username   string
	Password   string
	RequireTLS bool
}

func FromEnv() Config {
	host := firstEnv("SMTP_HOST", "SMTP_HOST")
	from := firstEnv("SMTP_FROM", "SMTP_FROM")
	user := firstEnv("SMTP_USER", "SMTP_USERNAME")
	pass := firstEnv("SMTP_PASS", "SMTP_PASSWORD")
	mode := strings.ToLower(firstEnv("SMTP_TLS", ""))
	require := mode == "require" || mode == "starttls" || user != ""
	if mode == "off" || mode == "false" {
		require = false
	}
	return Config{Host: host, From: from, Username: user, Password: pass, RequireTLS: require}
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func ResolveAddr(host, username string, requireTLS bool) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if strings.Contains(host, ":") {
		return host
	}
	if username != "" || requireTLS {
		return host + ":587"
	}
	return host + ":25"
}

func Send(cfg Config, to, subject, body string) error {
	to = strings.TrimSpace(to)
	addr := ResolveAddr(cfg.Host, cfg.Username, cfg.RequireTLS)
	if addr == "" || cfg.From == "" || to == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	msg := strings.Join([]string{
		"From: " + cfg.From,
		"To: " + to,
		"Subject: " + strings.ReplaceAll(subject, "\n", " "),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"",
		body,
	}, "\r\n")
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	} else if cfg.RequireTLS {
		return fmt.Errorf("smtp: STARTTLS required but not offered by %s", host)
	}
	if cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, host)); err != nil {
			return err
		}
	}
	if err := c.Mail(cfg.From); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
