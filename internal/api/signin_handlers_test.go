package api

import (
	"testing"
	"time"

	"github.com/prejudice-studio/twilight/internal/config"
	"github.com/prejudice-studio/twilight/internal/store"
)

func TestSigninSummaryPayloadAtUsesBeijingBusinessDate(t *testing.T) {
	t.Setenv("TZ", "UTC")

	now := time.Date(2026, 6, 30, 0, 30, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	si := store.Signin{
		UID:        1,
		LastSignin: "2026-06-30",
		Streak:     3,
	}

	payload := signinSummaryPayloadAt(config.Config{SigninEnabled: true}, si, now)
	if got, ok := payload["today_signed"].(bool); !ok || !got {
		t.Fatalf("today_signed=%v, want true with Beijing business date", payload["today_signed"])
	}
}
