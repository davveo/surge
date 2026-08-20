package main

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type config struct {
	HTTPAddr       string
	GatewayID      string
	IMCoreAddr     string
	RedisAddr      string
	RedisPass      string
	RedisDB        int
	JWTSecret      string
	IdleTimeout    time.Duration
	WebDir         string
	MinioEndpoint  string
	MinioAccess    string
	MinioSecret    string
	MinioBucket    string
	MinioPublicURL string
	MinioSecure    bool
}

func loadConfig() config {
	id := env("GATEWAY_ID", "")
	if id == "" {
		id = "gw-1"
	}
	return config{
		HTTPAddr:       env("GATEWAY_ADDR", ":8080"),
		GatewayID:      id,
		IMCoreAddr:     env("IMCORE_ADDR", "127.0.0.1:9000"),
		RedisAddr:      env("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPass:      env("REDIS_PASS", ""),
		RedisDB:        envInt("REDIS_DB", 0),
		JWTSecret:      env("JWT_SECRET", "surge-dev-secret"),
		WebDir:         env("WEB_DIR", "web"),
		IdleTimeout:    90 * time.Second,
		MinioEndpoint:  env("MINIO_ENDPOINT", ""),
		MinioAccess:    env("MINIO_ACCESS_KEY", "surge"),
		MinioSecret:    env("MINIO_SECRET_KEY", "surge-minio"),
		MinioBucket:    env("MINIO_BUCKET", "surge"),
		MinioPublicURL: env("MINIO_PUBLIC_URL", "http://127.0.0.1:9001"),
		MinioSecure:    env("MINIO_SECURE", "false") == "true",
	}
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
