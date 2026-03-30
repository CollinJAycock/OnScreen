package main

import (
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/onscreen/onscreen/internal/auth"
)

func main() {
	secret := os.Getenv("SECRET_KEY")
	if secret == "" {
		secret = "dev-secret-key-change-in-production-32b"
	}
	key := auth.DeriveKey32(secret)
	tm, err := auth.NewTokenMaker(key)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	userID := os.Getenv("USER_ID")
	if userID == "" {
		userID = "00000000-0000-0000-0000-000000000000"
	}
	token, err := tm.IssueAccessToken(auth.Claims{
		UserID:   uuid.MustParse(userID),
		Username: "dev",
		IsAdmin:  true,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(token)
}
