package auth_test

import (
	"testing"
	"time"

	"sis/pkg/auth"
)

func TestHashAndCheck_Valid(t *testing.T) {
	hash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !auth.CheckPassword(hash, "hunter2") {
		t.Error("expected CheckPassword true for correct password")
	}
}

func TestCheckPassword_Wrong(t *testing.T) {
	hash, _ := auth.HashPassword("correct")
	if auth.CheckPassword(hash, "wrong") {
		t.Error("expected CheckPassword false for wrong password")
	}
}

func TestGenerateAndValidate_Token(t *testing.T) {
	secret := "test-secret"
	userID := "user-uuid-123"
	tok, err := auth.GenerateToken(userID, secret, 24*time.Hour)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got, err := auth.ValidateToken(tok, secret)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got != userID {
		t.Errorf("got userID=%q, want %q", got, userID)
	}
}

func TestValidateToken_WrongSecret(t *testing.T) {
	tok, _ := auth.GenerateToken("uid", "secret1", time.Hour)
	_, err := auth.ValidateToken(tok, "secret2")
	if err == nil {
		t.Error("expected error for wrong secret")
	}
}

func TestValidateToken_Expired(t *testing.T) {
	tok, _ := auth.GenerateToken("uid", "secret", -time.Second)
	_, err := auth.ValidateToken(tok, "secret")
	if err == nil {
		t.Error("expected error for expired token")
	}
}
