package api

import (
	"context"
	"strconv"
	"time"

	"github.com/prejudice-studio/twilight/internal/store"
	"go.uber.org/zap"
)

const (
	embyRecentPlaybackDefaultLimit = 20
	embyRecentPlaybackMergeWindow  = 12 * time.Hour
)

type embyRecentPlaybackSyncResult struct {
	Checked  int
	Inserted int
	Updated  int
	Skipped  int
	Failed   int
}

func (r embyRecentPlaybackSyncResult) changed() int {
	return r.Inserted + r.Updated
}

func (r *embyRecentPlaybackSyncResult) add(other embyRecentPlaybackSyncResult) {
	r.Checked += other.Checked
	r.Inserted += other.Inserted
	r.Updated += other.Updated
	r.Skipped += other.Skipped
	r.Failed += other.Failed
}

func (a *App) syncRecentEmbyPlaybackForUsers(ctx context.Context, users []store.User, since time.Time, limit int) embyRecentPlaybackSyncResult {
	var total embyRecentPlaybackSyncResult
	if !a.embyConfigured() {
		return total
	}
	for _, user := range users {
		if err := ctx.Err(); err != nil {
			total.Failed++
			zap.L().Warn("recent Emby playback sync terminated", zap.Error(err))
			return total
		}
		total.add(a.syncRecentEmbyPlaybackForUser(ctx, user, since, limit))
	}
	return total
}

func (a *App) syncRecentEmbyPlaybackForUser(ctx context.Context, user store.User, since time.Time, limit int) embyRecentPlaybackSyncResult {
	var result embyRecentPlaybackSyncResult
	if !a.embyConfigured() || user.EmbyID == "" {
		return result
	}
	if limit <= 0 {
		limit = embyRecentPlaybackDefaultLimit
	}
	path := "/Users/" + urlPathEscape(user.EmbyID) +
		"/Items?Recursive=true&SortBy=DatePlayed&SortOrder=Descending&Limit=" + strconv.Itoa(limit) +
		"&Fields=UserData,RunTimeTicks,SeriesName,ParentIndexNumber,IndexNumber"
	var payload map[string]any
	if err := a.embyGetWithTimeout(ctx, path, &payload, 8*time.Second); err != nil {
		result.Failed++
		zap.L().Warn("failed to sync recent Emby playback", zap.Int64("uid", user.UID), zap.Error(err))
		return result
	}
	items, _ := payload["Items"].([]any)
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item == nil {
			result.Skipped++
			continue
		}
		result.Checked++
		record, ok := embyRecentPlaybackRecord(user.UID, item)
		if !ok {
			result.Skipped++
			continue
		}
		if !since.IsZero() && time.Unix(record.PlayedAt, 0).Before(since) {
			result.Skipped++
			continue
		}
		existed := a.playbackRecordExistsInMergeWindow(user.UID, record.ItemID, record.PlayedAt, embyRecentPlaybackMergeWindow)
		changed, err := a.store().UpsertLivePlaybackRecord(record, embyRecentPlaybackMergeWindow)
		if err != nil {
			result.Failed++
			zap.L().Warn("failed to persist recent Emby playback", zap.Int64("uid", user.UID), zap.String("item_id", record.ItemID), zap.Error(err))
			continue
		}
		if !changed {
			continue
		}
		if existed {
			result.Updated++
		} else {
			result.Inserted++
		}
	}
	return result
}

func (a *App) playbackRecordExistsInMergeWindow(uid int64, itemID string, playedAt int64, mergeWindow time.Duration) bool {
	if uid == 0 || itemID == "" || playedAt <= 0 {
		return false
	}
	mergeSeconds := int64(mergeWindow / time.Second)
	for _, existing := range a.store().PlaybackRecords(uid, playedAt-mergeSeconds, 10000) {
		if existing.ItemID != itemID {
			continue
		}
		delta := existing.PlayedAt - playedAt
		if delta < 0 {
			delta = -delta
		}
		if delta <= mergeSeconds {
			return true
		}
	}
	return false
}

func embyRecentPlaybackRecord(uid int64, item map[string]any) (store.PlaybackRecord, bool) {
	userData, _ := item["UserData"].(map[string]any)
	if userData == nil {
		return store.PlaybackRecord{}, false
	}
	playedAt, ok := parseEmbyTime(asString(userData["LastPlayedDate"]))
	if !ok {
		return store.PlaybackRecord{}, false
	}
	duration := numeric(userData["PlaybackPositionTicks"]) / 10000000
	if duration <= 0 && (boolish(userData["Played"]) || numeric(userData["PlayCount"]) > 0) {
		duration = numeric(item["RunTimeTicks"]) / 10000000
	}
	if duration <= 0 {
		return store.PlaybackRecord{}, false
	}
	return store.PlaybackRecord{
		UID:         uid,
		ItemID:      firstNonEmpty(asString(item["Id"]), asString(item["ID"])),
		Title:       firstNonEmpty(asString(item["Name"]), asString(item["SeriesName"])),
		SeriesName:  asString(item["SeriesName"]),
		MediaType:   asString(item["Type"]),
		IndexNumber: int(intValue(item, "IndexNumber", 0)),
		Duration:    duration,
		PlayedAt:    playedAt.Unix(),
	}, true
}
