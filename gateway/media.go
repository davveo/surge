package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	maxUploadBytes = 20 << 20
	thumbMaxPx     = 200
)

type mediaStore struct {
	cli        *minio.Client
	signer     *minio.Client
	bucket     string
	publicBase string
	once       sync.Once
	initErr    error
}

func newMediaStore(cfg config) (*mediaStore, error) {
	if strings.TrimSpace(cfg.MinioEndpoint) == "" {
		return nil, nil
	}
	creds := credentials.NewStaticV4(cfg.MinioAccess, cfg.MinioSecret, "")
	cli, err := minio.New(cfg.MinioEndpoint, &minio.Options{Creds: creds, Secure: cfg.MinioSecure})
	if err != nil {
		return nil, err
	}
	bucket := cfg.MinioBucket
	if bucket == "" {
		bucket = "surge"
	}
	publicBase := strings.TrimRight(cfg.MinioPublicURL, "/")
	signer := cli
	if pub, err := url.Parse(publicBase); err == nil && pub.Host != "" && pub.Host != cfg.MinioEndpoint {
		s, err := minio.New(pub.Host, &minio.Options{Creds: creds, Secure: pub.Scheme == "https"})
		if err != nil {
			return nil, err
		}
		signer = s
	}
	return &mediaStore{cli: cli, signer: signer, bucket: bucket, publicBase: publicBase}, nil
}

func (m *mediaStore) ensure(ctx context.Context) error {
	if m == nil {
		return fmt.Errorf("media not configured")
	}
	m.once.Do(func() {
		ok, err := m.cli.BucketExists(ctx, m.bucket)
		if err != nil {
			m.initErr = err
			return
		}
		if !ok {
			if err := m.cli.MakeBucket(ctx, m.bucket, minio.MakeBucketOptions{}); err != nil {
				m.initErr = err
				return
			}
		}
		policy := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::%s/*"]}]}`, m.bucket)
		m.initErr = m.cli.SetBucketPolicy(ctx, m.bucket, policy)
	})
	return m.initErr
}

func (m *mediaStore) objectURL(key string) string {
	if m == nil || key == "" {
		return ""
	}
	return m.publicBase + "/" + m.bucket + "/" + strings.TrimPrefix(key, "/")
}

type presignOut struct {
	ObjectKey   string `json:"object_key"`
	PutURL      string `json:"put_url"`
	GetURL      string `json:"get_url"`
	ContentType string `json:"content_type"`
}

func (m *mediaStore) presign(ctx context.Context, uid, filename, contentType string) (*presignOut, error) {
	if err := m.ensure(ctx); err != nil {
		return nil, err
	}
	ext := path.Ext(filename)
	if len(ext) > 8 {
		ext = ""
	}
	key := uid + "/" + time.Now().Format("2006/01/02") + "/" + uuid.NewString() + strings.ToLower(ext)
	u, err := m.signer.PresignedPutObject(ctx, m.bucket, key, 15*time.Minute)
	if err != nil {
		return nil, err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return &presignOut{
		ObjectKey:   key,
		PutURL:      u.String(),
		GetURL:      m.objectURL(key),
		ContentType: contentType,
	}, nil
}

type completeOut struct {
	ObjectKey   string `json:"object_key"`
	ThumbKey    string `json:"thumb_key,omitempty"`
	GetURL      string `json:"get_url"`
	ThumbURL    string `json:"thumb_url,omitempty"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Width       int32  `json:"width,omitempty"`
	Height      int32  `json:"height,omitempty"`
	Filename    string `json:"filename,omitempty"`
}

func (m *mediaStore) complete(ctx context.Context, key, filename string) (*completeOut, error) {
	if err := m.ensure(ctx); err != nil {
		return nil, err
	}
	key = strings.TrimSpace(key)
	if key == "" || strings.Contains(key, "..") {
		return nil, fmt.Errorf("invalid object_key")
	}
	info, err := m.cli.StatObject(ctx, m.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return nil, err
	}
	if info.Size > maxUploadBytes {
		return nil, fmt.Errorf("file too large")
	}
	out := &completeOut{
		ObjectKey:   key,
		GetURL:      m.objectURL(key),
		ContentType: info.ContentType,
		Size:        info.Size,
		Filename:    filename,
	}
	if out.ContentType == "" {
		out.ContentType = info.Metadata.Get("Content-Type")
	}
	if !isImageCT(out.ContentType) && !isImageName(filename) {
		return out, nil
	}
	obj, err := m.cli.GetObject(ctx, m.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return out, nil
	}
	defer obj.Close()
	limited := io.LimitReader(obj, maxUploadBytes+1)
	img, _, err := image.Decode(limited)
	if err != nil {
		return out, nil
	}
	b := img.Bounds()
	out.Width = int32(b.Dx())
	out.Height = int32(b.Dy())
	thumb := resizeImage(img, thumbMaxPx)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: 70}); err != nil {
		return out, nil
	}
	thumbKey := key + ".thumb.jpg"
	_, err = m.cli.PutObject(ctx, m.bucket, thumbKey, &buf, int64(buf.Len()), minio.PutObjectOptions{ContentType: "image/jpeg"})
	if err != nil {
		return out, nil
	}
	out.ThumbKey = thumbKey
	out.ThumbURL = m.objectURL(thumbKey)
	return out, nil
}

func isImageCT(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.HasPrefix(ct, "image/jpeg") || strings.HasPrefix(ct, "image/png") || strings.HasPrefix(ct, "image/gif") || ct == "image/jpg"
}

func isImageName(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif":
		return true
	default:
		return false
	}
}

func resizeImage(img image.Image, max int) image.Image {
	r := img.Bounds()
	w, h := r.Dx(), r.Dy()
	if w <= max && h <= max {
		return img
	}
	nw, nh := max, max
	if w > h {
		nh = h * max / w
	} else {
		nw = w * max / h
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		for x := 0; x < nw; x++ {
			dst.Set(x, y, img.At(r.Min.X+x*w/nw, r.Min.Y+y*h/nh))
		}
	}
	return dst
}

func (a *httpAPI) mediaPresign(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.uidFromAuth(w, r)
	if !ok {
		return
	}
	if a.media == nil {
		http.Error(w, "media not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Filename    string `json:"filename"`
		ContentType string `json:"content_type"`
		Size        int64  `json:"size"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}
	if body.Size > maxUploadBytes {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}
	out, err := a.media.presign(r.Context(), uid, body.Filename, body.ContentType)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *httpAPI) mediaComplete(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.uidFromAuth(w, r); !ok {
		return
	}
	if a.media == nil {
		http.Error(w, "media not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ObjectKey string `json:"object_key"`
		Filename  string `json:"filename"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || strings.TrimSpace(body.ObjectKey) == "" {
		http.Error(w, `{"error":"object_key required"}`, http.StatusBadRequest)
		return
	}
	out, err := a.media.complete(r.Context(), body.ObjectKey, body.Filename)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
