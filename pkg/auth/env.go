package auth

import (
	"fmt"
	"os"
	"strings"
)

const DevJWTSecret = "surge-dev-secret"

func IsProduction() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	if v == "" {
		v = strings.ToLower(strings.TrimSpace(os.Getenv("ENV")))
	}
	if v == "" {
		v = strings.ToLower(strings.TrimSpace(os.Getenv("SURGE_ENV")))
	}
	return v == "prod" || v == "production"
}

func IsInsecureSecret(secret string) bool {
	s := strings.TrimSpace(secret)
	return s == "" || s == DevJWTSecret
}

func CheckProductionSecret(secret string) error {
	if !IsProduction() {
		return nil
	}
	if IsInsecureSecret(secret) {
		return fmt.Errorf("JWT_SECRET must be set to a non-default value when APP_ENV=production")
	}
	return nil
}
