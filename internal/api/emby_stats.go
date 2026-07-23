package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/prejudice-studio/twilight/internal/store"
	"go.uber.org/zap"
)

const (
	embyStatsPeriodDaily   = "daily"
	embyStatsPeriodWeekly  = "weekly"
	embyStatsPeriodMonthly = "monthly"
	maxPlaybackDurationSec = 12 * 3600
)

type embyStatsWindow struct {
	Period string
	Start  time.Time
	End    time.Time
	Title  string
}

type embyRankEntry struct {
	Key          string `json:"key,omitempty"`
	Title        string `json:"title"`
	PlayCount    int    `json:"play_count"`
	TotalSeconds int64  `json:"total_seconds"`
	TotalMinutes int64  `json:"total_minutes"`
	MediaType    string `json:"media_type,omitempty"`
}

type embyUserMinutesEntry struct {
	UID          int64  `json:"uid"`
	Username     string `json:"username"`
	EmbyUsername string `json:"emby_username,omitempty"`
	TotalSeconds int64  `json:"total_seconds"`
	TotalMinutes int64  `json:"total_minutes"`
	PlayCount    int    `json:"play_count"`
}

type embyStatsResult struct {
	Window      embyStatsWindow
	SeriesTop   []embyRankEntry
	MovieTop    []embyRankEntry
	UserMinutes []embyUserMinutesEntry
	Debug       map[string]any
}

type embyPlaybackEvent struct {
	UserKey      string
	UserID       string
	ItemID       string
	StartAt      time.Time
	StopAt       time.Time
	TotalSeconds int64
}

type embyItemMetadata struct {
	ID           string `json:"Id"`
	Name         string `json:"Name"`
	Type         string `json:"Type"`
	SeriesID     string `json:"SeriesId"`
	SeriesName   string `json:"SeriesName"`
	ParentID     string `json:"ParentId"`
	ParentIndex  int    `json:"ParentIndexNumber"`
	IndexNumber  int    `json:"IndexNumber"`
	RunTimeTicks int64  `json:"RunTimeTicks"`
}

type embyItemsPayload struct {
	Items []embyItemMetadata `json:"Items"`
}

func (a *App) embyStatsLocation() *time.Location {
	name := strings.TrimSpace(a.cfg().TelegramStatsTimezone)
	if name == "" {
		name = "Asia/Shanghai"
	}
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return time.FixedZone("Asia/Shanghai", 8*3600)
}

func (a *App) embyStatsWindow(period string, now time.Time) embyStatsWindow {
	loc := a.embyStatsLocation()
	now = now.In(loc)
	switch strings.ToLower(strings.TrimSpace(period)) {
	case embyStatsPeriodWeekly:
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(weekday - 1))
		return embyStatsWindow{Period: embyStatsPeriodWeekly, Start: start, End: start.AddDate(0, 0, 7), Title: "本周"}
	case embyStatsPeriodMonthly:
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
		return embyStatsWindow{Period: embyStatsPeriodMonthly, Start: start, End: start.AddDate(0, 1, 0), Title: "本月"}
	default:
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
		return embyStatsWindow{Period: embyStatsPeriodDaily, Start: start, End: start.AddDate(0, 0, 1), Title: "今日"}
	}
}

func (a *App) embyStatsTopLimit(limit int) int {
	if limit <= 0 {
		limit = a.cfg().TelegramStatsTopLimit
	}
	if limit <= 0 {
		limit = 10
	}
	return clamp(limit, 1, 50)
}

func embyPlaybackDurationSeconds(record store.PlaybackRecord) int64 {
	seconds := record.Duration
	if seconds < 0 {
		return 0
	}
	if seconds > maxPlaybackDurationSec {
		return maxPlaybackDurationSec
	}
	return seconds
}

func embyPlaybackTitle(record store.PlaybackRecord) string {
	mediaType := strings.ToLower(strings.TrimSpace(record.MediaType))
	if mediaType == "episode" || mediaType == "series" {
		return firstNonEmpty(strings.TrimSpace(record.SeriesName), strings.TrimSpace(record.Title), strings.TrimSpace(record.ItemID), "未知剧集")
	}
	return firstNonEmpty(strings.TrimSpace(record.Title), strings.TrimSpace(record.SeriesName), strings.TrimSpace(record.ItemID), "未知媒体")
}

func embyMediaRankType(record store.PlaybackRecord) string {
	switch strings.ToLower(strings.TrimSpace(record.MediaType)) {
	case "movie":
		return "movie"
	case "episode", "series":
		return "series"
	default:
		return ""
	}
}

func (a *App) embyStatsData(ctx context.Context, period string, now time.Time, limit int) (embyStatsWindow, []embyRankEntry, []embyRankEntry, []embyUserMinutesEntry) {
	result := a.embyStatsDataDetailed(ctx, period, now, limit)
	return result.Window, result.SeriesTop, result.MovieTop, result.UserMinutes
}

func (a *App) embyStatsDataDetailed(ctx context.Context, period string, now time.Time, limit int) embyStatsResult {
	window := a.embyStatsWindow(period, now)
	limit = a.embyStatsTopLimit(limit)
	cacheKey := fmt.Sprintf("%s|%d|%d", window.Period, window.Start.Unix(), limit)
	records := a.store().PlaybackRecords(0, window.Start.Unix(), 10000)
	localRecords := filterPlaybackRecordsForWindow(records, window)
	source := "local_playback_records"
	seriesBuckets := map[string]*embyRankBucket{}
	movieBuckets := map[string]*embyRankBucket{}
	userBuckets := map[int64]*embyUserBucket{}
	userByUID := map[int64]store.User{}
	userByActivityKey := map[string]store.User{}
	for _, u := range a.store().ListUsers() {
		userByUID[u.UID] = u
		for _, key := range []string{u.EmbyID, u.EmbyUsername, u.Username} {
			if normalized := normalizeActivityPlaybackUser(key); normalized != "" {
				userByActivityKey[normalized] = u
			}
		}
	}
	addPlaybackRecordsToStats(localRecords, userByUID, seriesBuckets, movieBuckets, userBuckets)
	toRankEntries := func(buckets map[string]*embyRankBucket) []embyRankEntry {
		items := make([]embyRankEntry, 0, len(buckets))
		for _, bucket := range buckets {
			items = append(items, embyRankEntry{
				Key:          bucket.Key,
				Title:        bucket.Title,
				PlayCount:    bucket.PlayCount,
				TotalSeconds: bucket.TotalSeconds,
				TotalMinutes: (bucket.TotalSeconds + 59) / 60,
				MediaType:    bucket.MediaType,
			})
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].PlayCount != items[j].PlayCount {
				return items[i].PlayCount > items[j].PlayCount
			}
			if items[i].TotalSeconds != items[j].TotalSeconds {
				return items[i].TotalSeconds > items[j].TotalSeconds
			}
			return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
		})
		if len(items) > limit {
			items = items[:limit]
		}
		return items
	}
	userItems := make([]embyUserMinutesEntry, 0, len(userBuckets))
	for _, bucket := range userBuckets {
		userItems = append(userItems, embyUserMinutesEntry{
			UID:          bucket.UID,
			Username:     firstNonEmpty(bucket.Username, fmt.Sprintf("UID %d", bucket.UID)),
			EmbyUsername: bucket.EmbyUsername,
			PlayCount:    bucket.PlayCount,
			TotalSeconds: bucket.TotalSeconds,
			TotalMinutes: (bucket.TotalSeconds + 59) / 60,
		})
	}
	sort.Slice(userItems, func(i, j int) bool {
		if userItems[i].TotalSeconds != userItems[j].TotalSeconds {
			return userItems[i].TotalSeconds > userItems[j].TotalSeconds
		}
		if userItems[i].PlayCount != userItems[j].PlayCount {
			return userItems[i].PlayCount > userItems[j].PlayCount
		}
		return strings.ToLower(userItems[i].Username) < strings.ToLower(userItems[j].Username)
	})
	if len(userItems) > limit {
		userItems = userItems[:limit]
	}
	seriesTop := toRankEntries(seriesBuckets)
	movieTop := toRankEntries(movieBuckets)
	result := embyStatsResult{
		Window:      window,
		SeriesTop:   seriesTop,
		MovieTop:    movieTop,
		UserMinutes: userItems,
		Debug: map[string]any{
			"source":                  source,
			"timezone":                window.Start.Location().String(),
			"local_records_raw":       len(records),
			"local_records_in_window": len(localRecords),
			"activity_entries":        0,
			"activity_events":         0,
			"activity_disabled":       true,
			"series_buckets":          len(seriesBuckets),
			"movie_buckets":           len(movieBuckets),
			"user_buckets":            len(userBuckets),
		},
	}
	zap.L().Info(
		"emby stats built",
		zap.String("period", window.Period),
		zap.String("source", source),
		zap.Time("start", window.Start),
		zap.Time("end", window.End),
		zap.Int("local_records", len(localRecords)),
		zap.Bool("activity_disabled", true),
		zap.Int("series_buckets", len(seriesBuckets)),
		zap.Int("movie_buckets", len(movieBuckets)),
		zap.Int("user_buckets", len(userBuckets)),
	)
	a.storeEmbyStats(cacheKey, result, now)
	return result
}

func (a *App) cachedEmbyStats(key string, now time.Time) (embyStatsResult, bool) {
	a.embyStatsMu.Lock()
	defer a.embyStatsMu.Unlock()
	entry, ok := a.embyStatsCache[key]
	if !ok {
		return embyStatsResult{}, false
	}
	if now.Sub(entry.checked) > 2*time.Hour {
		delete(a.embyStatsCache, key)
		return embyStatsResult{}, false
	}
	return entry.result, true
}

func (a *App) storeEmbyStats(key string, result embyStatsResult, now time.Time) {
	a.embyStatsMu.Lock()
	defer a.embyStatsMu.Unlock()
	a.embyStatsCache[key] = embyStatsCacheEntry{checked: now, result: result}
}

func filterPlaybackRecordsForWindow(records []store.PlaybackRecord, window embyStatsWindow) []store.PlaybackRecord {
	out := make([]store.PlaybackRecord, 0, len(records))
	for _, record := range records {
		playedAt := time.Unix(record.PlayedAt, 0).In(window.Start.Location())
		if playedAt.Before(window.Start) || !playedAt.Before(window.End) {
			continue
		}
		out = append(out, record)
	}
	return out
}

type embyRankBucket struct {
	Key          string
	Title        string
	PlayCount    int
	TotalSeconds int64
	MediaType    string
}

type embyUserBucket struct {
	UID          int64
	Username     string
	EmbyUsername string
	PlayCount    int
	TotalSeconds int64
}

func addPlaybackRecordsToStats(records []store.PlaybackRecord, userByUID map[int64]store.User, seriesBuckets map[string]*embyRankBucket, movieBuckets map[string]*embyRankBucket, userBuckets map[int64]*embyUserBucket) {
	for _, record := range records {
		duration := embyPlaybackDurationSeconds(record)
		kind := embyMediaRankType(record)
		if kind == "" {
			continue
		}
		title := embyPlaybackTitle(record)
		if title == "" {
			continue
		}
		var mediaKey string
		if kind == "series" {
			mediaKey = "series:" + title
			bucket := seriesBuckets[mediaKey]
			if bucket == nil {
				bucket = &embyRankBucket{Key: mediaKey, Title: title, MediaType: "series"}
				seriesBuckets[mediaKey] = bucket
			}
			bucket.PlayCount++
			bucket.TotalSeconds += duration
		} else {
			mediaKey = "movie:" + firstNonEmpty(strings.TrimSpace(record.ItemID), title)
			bucket := movieBuckets[mediaKey]
			if bucket == nil {
				bucket = &embyRankBucket{Key: mediaKey, Title: title, MediaType: "movie"}
				movieBuckets[mediaKey] = bucket
			}
			bucket.PlayCount++
			bucket.TotalSeconds += duration
		}
		userInfo := userByUID[record.UID]
		userAgg := userBuckets[record.UID]
		if userAgg == nil {
			userAgg = &embyUserBucket{UID: record.UID, Username: userInfo.Username, EmbyUsername: userInfo.EmbyUsername}
			userBuckets[record.UID] = userAgg
		}
		userAgg.PlayCount++
		userAgg.TotalSeconds += duration
	}
}

func (a *App) addActivityEventsToStats(ctx context.Context, events []embyPlaybackEvent, userByActivityKey map[string]store.User, seriesBuckets map[string]*embyRankBucket, movieBuckets map[string]*embyRankBucket, userBuckets map[int64]*embyUserBucket) {
	itemCache := map[string]embyItemMetadata{}
	unknownUIDByKey := map[string]int64{}
	for _, event := range events {
		if event.TotalSeconds <= 0 || strings.TrimSpace(event.ItemID) == "" {
			continue
		}
		meta, ok := itemCache[event.ItemID]
		if !ok {
			meta = a.embyItemMetadata(ctx, event.ItemID, event.UserID)
			itemCache[event.ItemID] = meta
		}
		kind := strings.ToLower(strings.TrimSpace(meta.Type))
		switch kind {
		case "episode", "series":
			key := "series:" + firstNonEmpty(meta.SeriesID, meta.ID, meta.SeriesName, meta.Name, event.ItemID)
			title := firstNonEmpty(meta.SeriesName, meta.Name, event.ItemID, "未知剧集")
			bucket := seriesBuckets[key]
			if bucket == nil {
				bucket = &embyRankBucket{Key: key, Title: title, MediaType: "series"}
				seriesBuckets[key] = bucket
			}
			bucket.PlayCount++
			bucket.TotalSeconds += event.TotalSeconds
		case "movie":
			key := "movie:" + firstNonEmpty(meta.ID, event.ItemID, meta.Name)
			title := firstNonEmpty(meta.Name, event.ItemID, "未知电影")
			bucket := movieBuckets[key]
			if bucket == nil {
				bucket = &embyRankBucket{Key: key, Title: title, MediaType: "movie"}
				movieBuckets[key] = bucket
			}
			bucket.PlayCount++
			bucket.TotalSeconds += event.TotalSeconds
		default:
			continue
		}
		localUser := userByActivityKey[normalizeActivityPlaybackUser(event.UserKey)]
		uid := localUser.UID
		if uid == 0 {
			normalizedUserKey := normalizeActivityPlaybackUser(event.UserKey)
			if existingUID, ok := unknownUIDByKey[normalizedUserKey]; ok {
				uid = existingUID
			} else {
				uid = -int64(len(unknownUIDByKey) + 1)
				unknownUIDByKey[normalizedUserKey] = uid
			}
		}
		userAgg := userBuckets[uid]
		if userAgg == nil {
			userAgg = &embyUserBucket{
				UID:          uid,
				Username:     firstNonEmpty(localUser.Username, event.UserKey, "未知用户"),
				EmbyUsername: firstNonEmpty(localUser.EmbyUsername, event.UserKey),
			}
			userBuckets[uid] = userAgg
		}
		userAgg.PlayCount++
		userAgg.TotalSeconds += event.TotalSeconds
	}
}

func (a *App) embyItemMetadata(ctx context.Context, itemID string, userID string) embyItemMetadata {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return embyItemMetadata{}
	}
	var payload embyItemsPayload
	if err := a.embyGet(ctx, "/Items?Ids="+urlPathEscape(itemID)+"&Recursive=true", &payload); err == nil && len(payload.Items) > 0 {
		meta := payload.Items[0]
		if meta.ID == "" {
			meta.ID = itemID
		}
		return meta
	}
	var meta embyItemMetadata
	if err := a.embyGet(ctx, "/Items/"+urlPathEscape(itemID), &meta); err != nil {
		zap.L().Warn("failed to read Emby item metadata for stats", zap.String("item_id", itemID), zap.String("user_id", userID), zap.Error(err))
		return embyItemMetadata{ID: itemID}
	}
	if meta.ID == "" {
		meta.ID = itemID
	}
	return meta
}

func embyPlaybackEventsFromActivity(entries []embyActivityLogEntry, start, end time.Time, loc *time.Location) []embyPlaybackEvent {
	type stopPoint struct {
		at     time.Time
		itemID string
		user   string
		userID string
	}
	pendingStops := make(map[string][]stopPoint)
	events := []embyPlaybackEvent{}
	for _, entry := range entries {
		userKey := activityPlaybackUserKey(entry)
		itemID := strings.TrimSpace(entry.ItemID)
		if !strings.HasPrefix(entry.Type, "playback.") || userKey == "" || itemID == "" {
			continue
		}
		playedAt, err := parseEmbyActivityTime(entry.Date, loc)
		if err != nil {
			continue
		}
		key := userKey + "|" + itemID
		switch entry.Type {
		case "playback.stop":
			if playedAt.Before(start) || !playedAt.Before(end) {
				continue
			}
			pendingStops[key] = append(pendingStops[key], stopPoint{at: playedAt, itemID: itemID, user: userKey, userID: strings.TrimSpace(entry.UserID)})
		case "playback.start":
			stops := pendingStops[key]
			if len(stops) == 0 {
				continue
			}
			stopAt := stops[len(stops)-1]
			pendingStops[key] = stops[:len(stops)-1]
			startAt := playedAt
			if startAt.Before(start) {
				startAt = start
			}
			if !startAt.Before(end) || !stopAt.at.After(startAt) {
				continue
			}
			seconds := int64(stopAt.at.Sub(startAt).Seconds())
			if seconds > maxPlaybackDurationSec {
				seconds = maxPlaybackDurationSec
			}
			events = append(events, embyPlaybackEvent{UserKey: userKey, UserID: strings.TrimSpace(entry.UserID), ItemID: itemID, StartAt: startAt, StopAt: stopAt.at, TotalSeconds: seconds})
		}
	}
	for _, stops := range pendingStops {
		for _, stopAt := range stops {
			if !stopAt.at.After(start) || stopAt.at.Sub(start) > 12*time.Hour {
				continue
			}
			seconds := int64(stopAt.at.Sub(start).Seconds())
			if seconds > maxPlaybackDurationSec {
				seconds = maxPlaybackDurationSec
			}
			events = append(events, embyPlaybackEvent{UserKey: stopAt.user, UserID: stopAt.userID, ItemID: stopAt.itemID, StartAt: start, StopAt: stopAt.at, TotalSeconds: seconds})
		}
	}
	return events
}

func (a *App) embyLinesPayload(u store.User) map[string]any {
	if u.Role == store.RoleNormal && u.EmbyID == "" && !u.PendingEmby {
		return map[string]any{"lines": []any{}, "whitelist_lines": []any{}, "requires_emby_account": true, "requires_renewal": false, "emby_disabled_by_expiry": false}
	}
	if u.Role == store.RoleNormal && u.ExpiredAt > 0 && u.ExpiredAt < time.Now().Unix() {
		return map[string]any{"lines": []any{}, "whitelist_lines": []any{}, "requires_emby_account": false, "requires_renewal": true, "emby_disabled_by_expiry": true}
	}
	if u.Role == store.RoleNormal && !u.Active {
		return map[string]any{"lines": []any{}, "whitelist_lines": []any{}, "requires_emby_account": false, "requires_renewal": false, "emby_disabled_by_expiry": false}
	}
	lines := make([]map[string]string, 0, len(a.cfg().EmbyURLList)+1)
	for _, line := range a.cfg().EmbyURLList {
		lines = append(lines, map[string]string{"name": line.Name, "url": line.URL})
	}
	if a.cfg().EmbyPublicURL != "" {
		lines = append(lines, map[string]string{"name": "默认线路", "url": a.cfg().EmbyPublicURL})
	}
	whitelist := []map[string]string{}
	if u.Role == store.RoleAdmin || u.Role == store.RoleWhitelist {
		for _, line := range a.cfg().EmbyWhitelistURLList {
			whitelist = append(whitelist, map[string]string{"name": line.Name, "url": line.URL})
		}
		if a.cfg().EmbyWhitelistURL != "" {
			whitelist = append(whitelist, map[string]string{"name": "whitelist route", "url": a.cfg().EmbyWhitelistURL})
		}
	}
	return map[string]any{"lines": lines, "whitelist_lines": whitelist, "requires_emby_account": false, "requires_renewal": false, "emby_disabled_by_expiry": false}
}

func (a *App) handleEmbyOnline(w http.ResponseWriter, r *http.Request, _ Params) {
	if !a.embyConfigured() {
		ok(w, "OK", map[string]any{"online": false, "current_online": 0, "users": []any{}, "message": "Emby not configured"})
		return
	}
	u := current(r).User
	canSeeDetails := u.Role == store.RoleAdmin || u.Role == store.RoleWhitelist
	var sessions []map[string]any
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := a.embyGet(ctx, "/Sessions", &sessions); err != nil {
		failWithCode(w, http.StatusBadGateway, ErrEmbyRemoteSessionsFail, "获取 Emby 在线会话失败")
		return
	}
	currentOnline := 0
	users := make([]map[string]any, 0, len(sessions))
	for _, session := range sessions {
		nowPlaying, _ := session["NowPlayingItem"].(map[string]any)
		if nowPlaying == nil {
			continue
		}
		currentOnline++
		if !canSeeDetails {
			continue
		}
		users = append(users, map[string]any{
			"username":      firstNonEmpty(asString(session["UserName"]), asString(session["UserId"])),
			"item_name":     firstNonEmpty(asString(nowPlaying["SeriesName"]), asString(nowPlaying["Name"])),
			"media_type":    asString(nowPlaying["Type"]),
			"client":        firstNonEmpty(asString(session["Client"]), asString(session["AppName"])),
			"device_name":   asString(session["DeviceName"]),
			"last_activity": asString(session["LastActivityDate"]),
		})
	}
	ok(w, "OK", map[string]any{
		"online":         true,
		"current_online": currentOnline,
		"users":          users,
		"message":        "OK",
	})
}

func (a *App) handleEmbyLines(w http.ResponseWriter, r *http.Request, _ Params) {
	ok(w, "OK", a.embyLinesPayload(current(r).User))
}

func (a *App) handleEmbyStatsToday(w http.ResponseWriter, r *http.Request, _ Params) {
	window, seriesTop, movieTop, userMinutes := a.embyStatsData(r.Context(), embyStatsPeriodDaily, time.Now(), queryInt(r, "limit", 0))
	ok(w, "OK", map[string]any{
		"period":           window.Period,
		"start_at":         window.Start.Unix(),
		"end_at":           window.End.Unix(),
		"series_top":       seriesTop,
		"movie_top":        movieTop,
		"user_minutes_top": userMinutes,
	})
}

func (a *App) handleEmbyStatsRank(w http.ResponseWriter, r *http.Request, _ Params) {
	period := firstNonEmpty(r.URL.Query().Get("period"), embyStatsPeriodDaily)
	mediaType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))
	window, seriesTop, movieTop, _ := a.embyStatsData(r.Context(), period, time.Now(), queryInt(r, "limit", 0))
	items := seriesTop
	if mediaType == "movie" {
		items = movieTop
	} else if mediaType != "series" {
		failWithCode(w, http.StatusBadRequest, ErrBadRequest, "type must be movie or series")
		return
	}
	ok(w, "OK", map[string]any{
		"period":   window.Period,
		"type":     mediaType,
		"start_at": window.Start.Unix(),
		"end_at":   window.End.Unix(),
		"items":    items,
	})
}

func (a *App) handleEmbyStatsUserMinutes(w http.ResponseWriter, r *http.Request, _ Params) {
	window, _, _, userMinutes := a.embyStatsData(r.Context(), firstNonEmpty(r.URL.Query().Get("period"), embyStatsPeriodDaily), time.Now(), queryInt(r, "limit", 0))
	ok(w, "OK", map[string]any{
		"period":   window.Period,
		"start_at": window.Start.Unix(),
		"end_at":   window.End.Unix(),
		"items":    userMinutes,
	})
}

func (a *App) handleEmbyStatsDebug(w http.ResponseWriter, r *http.Request, _ Params) {
	period := firstNonEmpty(r.URL.Query().Get("period"), embyStatsPeriodDaily)
	result := a.embyStatsDataDetailed(r.Context(), period, time.Now(), queryInt(r, "limit", 0))
	ok(w, "OK", map[string]any{
		"period":           result.Window.Period,
		"start_at":         result.Window.Start.Unix(),
		"end_at":           result.Window.End.Unix(),
		"start_at_text":    result.Window.Start.Format(time.RFC3339),
		"end_at_text":      result.Window.End.Format(time.RFC3339),
		"series_top":       result.SeriesTop,
		"movie_top":        result.MovieTop,
		"user_minutes_top": result.UserMinutes,
		"debug":            result.Debug,
	})
}

func (a *App) telegramStatsPushChatID() any {
	if chatID := strings.TrimSpace(a.cfg().TelegramStatsPushChatID); chatID != "" {
		if id, err := strconv.ParseInt(chatID, 10, 64); err == nil {
			return id
		}
		return chatID
	}
	if len(a.cfg().TelegramAdminIDs) > 0 {
		return a.cfg().TelegramAdminIDs[0]
	}
	return nil
}

func (a *App) telegramStatsPushEnabled() bool {
	return a.cfg().TelegramStatsEnabled && a.telegramAvailable() && a.telegramStatsPushChatID() != nil
}

func (a *App) shouldSendPeriodicStats(period string, now time.Time) bool {
	window := a.embyStatsWindow(period, now)
	switch window.Period {
	case embyStatsPeriodWeekly:
		return now.In(window.Start.Location()).Weekday() == time.Sunday
	case embyStatsPeriodMonthly:
		return now.In(window.Start.Location()).AddDate(0, 0, 1).Month() != now.In(window.Start.Location()).Month()
	default:
		return true
	}
}

func formatEmbyRankMessage(title, sectionMovie, sectionSeries string, movies, series []embyRankEntry) string {
	lines := []string{title, "", sectionSeries}
	if len(series) == 0 {
		lines = append(lines, "暂无电视剧播放记录")
	} else {
		for i, item := range series {
			lines = append(lines, fmt.Sprintf("%d. %s - %d 次 / %d 分钟", i+1, item.Title, item.PlayCount, item.TotalMinutes))
		}
	}
	lines = append(lines, "", sectionMovie)
	if len(movies) == 0 {
		lines = append(lines, "暂无电影播放记录")
	} else {
		for i, item := range movies {
			lines = append(lines, fmt.Sprintf("%d. %s - %d 次 / %d 分钟", i+1, item.Title, item.PlayCount, item.TotalMinutes))
		}
	}
	return strings.Join(lines, "\n")
}

func formatEmbyUserMinutesMessage(title string, items []embyUserMinutesEntry) string {
	lines := []string{title, ""}
	if len(items) == 0 {
		lines = append(lines, "暂无用户播放记录")
	} else {
		for i, item := range items {
			lines = append(lines, fmt.Sprintf("%d. %s - %d 分钟", i+1, firstNonEmpty(item.EmbyUsername, item.Username), item.TotalMinutes))
		}
	}
	return strings.Join(lines, "\n")
}
