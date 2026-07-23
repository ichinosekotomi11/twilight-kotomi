package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/prejudice-studio/twilight/internal/store"
	"go.uber.org/zap"
)

func (a *App) runRetentionExpiryJob(r *http.Request) (map[string]any, []string, error) {
	cfg := *a.cfg()
	now := time.Now()
	nowUnix := now.Unix()
	activityDays := 30
	requiredMinutes := 1
	graceDays := clamp(cfg.RetentionGraceDays, 1, 30)
	cost := cfg.SigninRenewalCost
	renewDays := cfg.SigninRenewalDays
	summary := map[string]any{
		"success": true, "checked": 0, "renewed": 0, "grace_started": 0,
		"grace_waiting": 0, "deleted": 0, "skipped_protected": 0, "failed": 0,
		"activity_days": activityDays, "grace_days": graceDays,
		"required_minutes": requiredMinutes,
		"renewal_cost":     cost, "renewal_days": renewDays,
	}
	inc := func(key string) { summary[key] = int(numeric(summary[key])) + 1 }

	for _, user := range a.store().ListUsers() {
		if err := r.Context().Err(); err != nil {
			summary["success"] = false
			summary["terminated"] = true
			return summary, []string{"retention job terminated"}, err
		}
		if a.userIsProtected(user) {
			if user.EmbyID != "" && (user.RetentionGraceUntil > 0 || (user.ExpiredAt > 0 && user.ExpiredAt <= nowUnix)) {
				inc("skipped_protected")
			}
			continue
		}
		if user.EmbyID == "" {
			continue
		}
		if user.RetentionGraceUntil > 0 {
			inc("checked")
			if renewed, err := a.tryRetentionRenew(r, user, cost, renewDays, now); err != nil {
				inc("failed")
				zap.L().Warn("retention grace renewal failed", zap.Int64("uid", user.UID), zap.Error(err))
			} else if renewed {
				inc("renewed")
			} else if nowUnix >= user.RetentionGraceUntil {
				if err := a.deleteExpiredRetentionEmby(r, user); err != nil {
					inc("failed")
					zap.L().Warn("retention grace deletion failed", zap.Int64("uid", user.UID), zap.Error(err))
				} else {
					inc("deleted")
				}
			} else {
				inc("grace_waiting")
			}
			continue
		}
		if user.ExpiredAt <= 0 || expiryIsPermanent(user.ExpiredAt) || user.ExpiredAt > nowUnix {
			continue
		}
		inc("checked")
		active, _, err := a.embyUserHasRecentPlayback(r, user, now.AddDate(0, 0, -activityDays), requiredMinutes)
		if err != nil {
			// Fail closed against destructive cleanup: an upstream Emby error must
			// never be interpreted as "no playback".
			active = true
			zap.L().Warn("retention playback lookup failed; granting grace", zap.Int64("uid", user.UID), zap.Error(err))
		}
		if !active {
			if err := a.deleteExpiredRetentionEmby(r, user); err != nil {
				inc("failed")
				zap.L().Warn("inactive retention deletion failed", zap.Int64("uid", user.UID), zap.Error(err))
			} else {
				inc("deleted")
			}
			continue
		}
		if renewed, err := a.tryRetentionRenew(r, user, cost, renewDays, now); err != nil {
			inc("failed")
			zap.L().Warn("retention renewal failed", zap.Int64("uid", user.UID), zap.Error(err))
		} else if renewed {
			inc("renewed")
			continue
		}
		graceUntil := now.AddDate(0, 0, graceDays).Unix()
		updated, err := a.store().UpdateUser(user.UID, func(u *store.User) error {
			u.RetentionExpiredAt = u.ExpiredAt
			u.RetentionGraceUntil = graceUntil
			u.ExpiredAt = graceUntil
			u.Active = true
			return nil
		})
		if err != nil {
			inc("failed")
			continue
		}
		sideCtx, cancel := schedulerSideEffectContext(r.Context())
		if err := a.embyApplyEnabledState(sideCtx, updated.UID, updated.EmbyID, false); err != nil {
			inc("failed")
			zap.L().Warn("retention grace Emby disable failed", zap.Int64("uid", user.UID), zap.Error(err))
		} else {
			inc("grace_started")
		}
		cancel()
	}

	message := fmt.Sprintf("retention checked %d users: renewed=%d grace=%d waiting=%d deleted=%d failed=%d",
		int(numeric(summary["checked"])), int(numeric(summary["renewed"])), int(numeric(summary["grace_started"])),
		int(numeric(summary["grace_waiting"])), int(numeric(summary["deleted"])), int(numeric(summary["failed"])))
	return summary, []string{message}, nil
}

func (a *App) tryRetentionRenew(r *http.Request, user store.User, cost, days int, now time.Time) (bool, error) {
	if cost <= 0 || days <= 0 {
		return false, fmt.Errorf("invalid retention renewal settings")
	}
	updated, _, err := a.store().SpendSigninPointsAndUpdateUser(user.UID, cost, func(u *store.User) error {
		u.ExpiredAt = now.AddDate(0, 0, days).Unix()
		u.RetentionGraceUntil = 0
		u.RetentionExpiredAt = 0
		u.Active = true
		return nil
	})
	if errors.Is(err, store.ErrInsufficientPoints) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	sideCtx, cancel := schedulerSideEffectContext(r.Context())
	defer cancel()
	if err := a.embyApplyEnabledState(sideCtx, updated.UID, updated.EmbyID, true); err != nil {
		return true, err
	}
	return true, nil
}

func (a *App) deleteExpiredRetentionEmby(r *http.Request, user store.User) error {
	sideCtx, cancel := schedulerSideEffectContext(r.Context())
	defer cancel()
	updated, err := a.telegramDeleteTargetEmby(sideCtx, user)
	if err != nil {
		return err
	}
	_, err = a.store().UpdateUser(updated.UID, func(u *store.User) error {
		u.ExpiredAt = 0
		u.RetentionGraceUntil = 0
		u.RetentionExpiredAt = 0
		u.EmbyDisabled = false
		u.Active = true
		return nil
	})
	return err
}

func (a *App) embyUserHasRecentPlayback(r *http.Request, user store.User, since time.Time, requiredMinutes int) (bool, int64, error) {
	since = embyPlaybackSince(user, since)
	requiredSeconds := int64(requiredMinutes) * 60
	records := a.store().PlaybackRecords(user.UID, since.Unix(), 10000)
	totalSeconds := int64(0)
	for _, record := range records {
		if record.Duration > 0 {
			totalSeconds += record.Duration
		}
	}
	if requiredSeconds <= 0 {
		if len(records) > 0 {
			return true, totalSeconds / 60, nil
		}
	} else if totalSeconds >= requiredSeconds {
		return true, totalSeconds / 60, nil
	}

	var payload map[string]any
	path := "/Users/" + urlPathEscape(user.EmbyID) +
		"/Items?Recursive=true&IsPlayed=true&SortBy=DatePlayed&SortOrder=Descending&Limit=1&Fields=UserData"
	if err := a.embyGet(r.Context(), path, &payload); err != nil {
		return false, totalSeconds / 60, err
	}
	items, _ := payload["Items"].([]any)
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		userData, _ := item["UserData"].(map[string]any)
		playedAt, ok := parseEmbyTime(asString(userData["LastPlayedDate"]))
		if ok && !playedAt.Before(since) && requiredSeconds <= 0 {
			if totalSeconds > 0 {
				return true, totalSeconds / 60, nil
			}
			return true, 1, nil
		}
	}
	return false, totalSeconds / 60, nil
}
