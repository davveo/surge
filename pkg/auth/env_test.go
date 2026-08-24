package auth

import "testing"

func TestCheckProductionSecret(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("ENV", "")
	t.Setenv("SURGE_ENV", "")
	if err := CheckProductionSecret(DevJWTSecret); err != nil {
		t.Fatalf("dev env should allow default secret: %v", err)
	}
	t.Setenv("APP_ENV", "production")
	if err := CheckProductionSecret(DevJWTSecret); err == nil {
		t.Fatal("production must reject default JWT secret")
	}
	if err := CheckProductionSecret("a-long-random-production-secret"); err != nil {
		t.Fatal(err)
	}
}

func TestIsProduction(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("ENV", "")
	t.Setenv("SURGE_ENV", "")
	if !IsProduction() {
		t.Fatal("expected prod")
	}
}
