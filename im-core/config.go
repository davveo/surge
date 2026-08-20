package main

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type config struct {
	GRPCAddr    string
	MySQLDSN    string
	RedisAddr   string
	RedisPass   string
	RedisDB     int
	GatewayID   string // unused here; routes live in Redis
	ReadTimeout time.Duration
}

func loadConfig() config {
	cfg := config{
		GRPCAddr:    env("IMCORE_ADDR", ":9000"),
		MySQLDSN:    env("MYSQL_DSN", "surge:surge@tcp(127.0.0.1:3306)/surge?parseTime=true&charset=utf8mb4"),
		RedisAddr:   env("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPass:   env("REDIS_PASS", ""),
		RedisDB:     envInt("REDIS_DB", 0),
		ReadTimeout: 5 * time.Second,
	}
	return cfg
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
