package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ogTitleRe   = regexp.MustCompile(`(?i)<meta[^>]+property=["']og:title["'][^>]+content=["']([^"']+)["']`)
	ogDescRe    = regexp.MustCompile(`(?i)<meta[^>]+property=["']og:description["'][^>]+content=["']([^"']+)["']`)
	ogImgRe     = regexp.MustCompile(`(?i)<meta[^>]+property=["']og:image["'][^>]+content=["']([^"']+)["']`)
	htmlTitleRe = regexp.MustCompile(`(?is)<title[^>]*>\s*([^<]+)\s*</title>`)
)

type linkPreviewOut struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Image       string `json:"image"`
}

func (a *httpAPI) linkPreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.uidFromAuth(w, r); !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.URL) == "" {
		http.Error(w, `{"error":"url required"}`, http.StatusBadRequest)
		return
	}
	out, err := fetchLinkPreview(r.Context(), body.URL)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func fetchLinkPreview(ctx context.Context, raw string) (*linkPreviewOut, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return nil, errPreview("invalid url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errPreview("only http(s)")
	}
	if err := assertPublicURL(u); err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DialContext: safeDial,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 2 {
				return http.ErrUseLastResponse
			}
			if err := assertPublicURL(req.URL); err != nil {
				return err
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "SurgeIM/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, errPreview("fetch failed")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	html := string(body)
	out := &linkPreviewOut{URL: u.String()}
	out.Title = firstMatch(ogTitleRe, html)
	if out.Title == "" {
		out.Title = firstMatch(htmlTitleRe, html)
	}
	out.Description = firstMatch(ogDescRe, html)
	out.Image = firstMatch(ogImgRe, html)
	out.Title = clipRunes(strings.TrimSpace(out.Title), 120)
	out.Description = clipRunes(strings.TrimSpace(out.Description), 240)
	if out.Title == "" {
		out.Title = u.Host
	}
	return out, nil
}

func safeDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var picked net.IP
	for _, ip := range ips {
		if isBadIP(ip.IP) {
			continue
		}
		picked = ip.IP
		break
	}
	if picked == nil {
		return nil, errPreview("private address blocked")
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	return d.DialContext(ctx, network, net.JoinHostPort(picked.String(), port))
}

func assertPublicURL(u *url.URL) error {
	if u == nil {
		return errPreview("invalid url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errPreview("only http(s)")
	}
	host := u.Hostname()
	if host == "" || strings.EqualFold(host, "localhost") {
		return errPreview("private address blocked")
	}
	if ip := net.ParseIP(host); ip != nil && isBadIP(ip) {
		return errPreview("private address blocked")
	}
	return nil
}

func isBadIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast()
}

func firstMatch(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func clipRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	i := 0
	for idx := range s {
		if i == max {
			return s[:idx]
		}
		i++
	}
	return s
}

type previewError string

func (e previewError) Error() string { return string(e) }

func errPreview(msg string) error { return previewError(msg) }
