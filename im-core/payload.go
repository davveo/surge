package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	imv1 "github.com/davveo/surge/proto/gen/im/v1"
	"google.golang.org/protobuf/proto"
)

var mentionRe = regexp.MustCompile(`@([A-Za-z0-9._@+-]{1,64})`)

func validateSend(fromUID, clientMsgID string, payload *imv1.Payload) error {
	if fromUID == "" || clientMsgID == "" {
		return fmt.Errorf("%w: from_uid and client_msg_id required", errInvalid)
	}
	if len(clientMsgID) > 64 {
		return fmt.Errorf("%w: client_msg_id too long", errInvalid)
	}
	if payload == nil {
		return fmt.Errorf("%w: payload required", errInvalid)
	}
	if utf8.RuneCountInString(payload.Text) > 4000 {
		return fmt.Errorf("%w: text too long", errInvalid)
	}
	switch payload.Type {
	case imv1.Payload_TEXT, imv1.Payload_SYSTEM:
		if strings.TrimSpace(payload.Text) == "" {
			return fmt.Errorf("%w: empty text", errInvalid)
		}
	case imv1.Payload_IMAGE, imv1.Payload_FILE:
		if payload.Media == nil || strings.TrimSpace(payload.Media.ObjectKey) == "" {
			return fmt.Errorf("%w: media object_key required", errInvalid)
		}
		if strings.Contains(payload.Media.ObjectKey, "..") {
			return fmt.Errorf("%w: invalid object_key", errInvalid)
		}
	default:
		return fmt.Errorf("%w: unsupported payload type", errInvalid)
	}
	return nil
}

func previewOf(p *imv1.Payload) string {
	if p == nil {
		return ""
	}
	switch p.Type {
	case imv1.Payload_IMAGE:
		if p.StickerId != "" {
			return "[表情]"
		}
		return "[图片]"
	case imv1.Payload_FILE:
		name := ""
		if p.Media != nil {
			name = p.Media.Filename
		}
		if name == "" {
			return "[文件]"
		}
		return clipText("[文件] "+name, 128)
	case imv1.Payload_RECALL:
		return "已撤回一条消息"
	default:
		if p.E2Ee {
			return "[加密消息]"
		}
		if p.Ephemeral {
			return "[阅后即焚]"
		}
		return clipText(p.GetText(), 128)
	}
}

func extractMentions(text string) []string {
	matches := mentionRe.FindAllStringSubmatch(text, 20)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, m := range matches {
		uid := strings.TrimSpace(m[1])
		if uid == "" {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		out = append(out, uid)
	}
	return out
}

func enrichPayload(p *imv1.Payload, quoteText string) *imv1.Payload {
	if p == nil {
		return nil
	}
	out := proto.Clone(p).(*imv1.Payload)
	if quoteText != "" && strings.TrimSpace(out.QuoteText) == "" {
		out.QuoteText = clipText(quoteText, 200)
	}
	if len(out.MentionUids) == 0 {
		out.MentionUids = extractMentions(out.Text)
	}
	return out
}

func marshalPayloadBlob(p *imv1.Payload) string {
	if p == nil {
		return ""
	}
	blob := payloadBlob{}
	if m := p.Media; m != nil && m.ObjectKey != "" {
		blob.ObjectKey = m.ObjectKey
		blob.ThumbKey = m.ThumbKey
		blob.ContentType = m.ContentType
		blob.Filename = m.Filename
		blob.Size = m.Size
		blob.Width = m.Width
		blob.Height = m.Height
		blob.URL = m.Url
		blob.ThumbURL = m.ThumbUrl
	}
	if l := p.Link; l != nil && strings.TrimSpace(l.Url) != "" {
		blob.Link = &linkJSON{
			URL:         l.Url,
			Title:       l.Title,
			Description: l.Description,
			Image:       l.Image,
		}
	}
	blob.Mentions = p.MentionUids
	blob.QuoteText = p.QuoteText
	blob.Ephemeral = p.Ephemeral
	blob.E2EE = p.E2Ee
	blob.StickerID = p.StickerId
	blob.Burned = false
	if blob.ObjectKey == "" && blob.Link == nil && len(blob.Mentions) == 0 && blob.QuoteText == "" && !blob.Ephemeral && !blob.E2EE && blob.StickerID == "" {
		return ""
	}
	b, err := json.Marshal(blob)
	if err != nil {
		return ""
	}
	return string(b)
}

func payloadFromCols(ptype int32, text, mediaJSON string, recalled bool) *imv1.Payload {
	blob := unmarshalPayloadBlob(mediaJSON)
	if recalled {
		if blob.Burned {
			return &imv1.Payload{Type: imv1.Payload_RECALL, Text: "已销毁"}
		}
		return &imv1.Payload{Type: imv1.Payload_RECALL}
	}
	p := &imv1.Payload{
		Type:      imv1.Payload_Type(ptype),
		Text:      text,
		Ephemeral: blob.Ephemeral,
		E2Ee:      blob.E2EE,
		StickerId: blob.StickerID,
	}
	if blob.ObjectKey != "" {
		p.Media = &imv1.Media{
			ObjectKey:   blob.ObjectKey,
			ThumbKey:    blob.ThumbKey,
			ContentType: blob.ContentType,
			Filename:    blob.Filename,
			Size:        blob.Size,
			Width:       blob.Width,
			Height:      blob.Height,
			Url:         blob.URL,
			ThumbUrl:    blob.ThumbURL,
		}
	}
	if blob.Link != nil && blob.Link.URL != "" {
		p.Link = &imv1.LinkPreview{
			Url:         blob.Link.URL,
			Title:       blob.Link.Title,
			Description: blob.Link.Description,
			Image:       blob.Link.Image,
		}
	}
	p.MentionUids = blob.Mentions
	p.QuoteText = blob.QuoteText
	return p
}

func unmarshalPayloadBlob(raw string) payloadBlob {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return payloadBlob{}
	}
	var blob payloadBlob
	_ = json.Unmarshal([]byte(raw), &blob)
	return blob
}

type payloadBlob struct {
	mediaJSON
	Link      *linkJSON `json:"link,omitempty"`
	Mentions  []string  `json:"mentions,omitempty"`
	QuoteText string    `json:"quoteText,omitempty"`
	Ephemeral bool      `json:"ephemeral,omitempty"`
	E2EE      bool      `json:"e2ee,omitempty"`
	StickerID string    `json:"stickerId,omitempty"`
	Burned    bool      `json:"burned,omitempty"`
}

type linkJSON struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Image       string `json:"image"`
}

type mediaJSON struct {
	ObjectKey   string `json:"objectKey"`
	ThumbKey    string `json:"thumbKey"`
	ContentType string `json:"contentType"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	Width       int32  `json:"width"`
	Height      int32  `json:"height"`
	URL         string `json:"url"`
	ThumbURL    string `json:"thumbUrl"`
}
