package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/prejudice-studio/twilight/internal/store"
)

func (a *App) requireMoviePilotAccess(w http.ResponseWriter, r *http.Request) bool {
	user := current(r).User
	if user.Role != 0 && user.Role != 2 {
		failWithCode(w, http.StatusForbidden, ErrForbidden, "MoviePilot 仅对白名单开放")
		return false
	}
	if !a.cfg().MoviePilotEnabled || strings.TrimSpace(a.cfg().MoviePilotURL) == "" {
		failWithCode(w, http.StatusServiceUnavailable, ErrServiceUnavailable, "MoviePilot 未启用")
		return false
	}
	return true
}

func (a *App) handleMoviePilotStatus(w http.ResponseWriter, r *http.Request, _ Params) {
	if !a.requireMoviePilotAccess(w, r) {
		return
	}
	ok(w, "OK", map[string]any{"enabled": true})
}

func (a *App) handleMoviePilotSearch(w http.ResponseWriter, r *http.Request, _ Params) {
	if !a.requireMoviePilotAccess(w, r) {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		failWithCode(w, http.StatusBadRequest, ErrBadRequest, "搜索关键词不能为空")
		return
	}
	var result any
	if err := a.moviePilotRequestWithTimeout(r.Context(), http.MethodGet, "/api/v1/search/title?keyword="+url.QueryEscape(query), nil, &result, 45*time.Second); err != nil {
		failWithCode(w, http.StatusBadGateway, ErrServiceUnavailable, "MoviePilot 搜索失败")
		return
	}
	ok(w, "OK", result)
}

func (a *App) handleMoviePilotDownload(w http.ResponseWriter, r *http.Request, _ Params) {
	if !a.requireMoviePilotAccess(w, r) {
		return
	}
	payload := decodeMap(r)
	if len(payload) == 0 {
		failWithCode(w, http.StatusBadRequest, ErrBadRequest, "下载参数不能为空")
		return
	}
	user := current(r).User
	cost := 0
	if a.cfg().MoviePilotDownloadPointsEnabled && user.Role != store.RoleAdmin {
		cost = a.moviePilotDownloadCost(payload)
		si := a.store().Signin(user.UID)
		if si.Points < cost {
			failWithCode(w, http.StatusConflict, ErrSigninInsufficientPoints, "小兔不足，无法提交下载")
			return
		}
	}
	requestPayload := normalizeMoviePilotDownloadPayload(payload)
	var result any
	if err := a.moviePilotRequestWithTimeout(r.Context(), http.MethodPost, "/api/v1/download/add", requestPayload, &result, 30*time.Second); err != nil {
		failWithCode(w, http.StatusBadGateway, ErrServiceUnavailable, "MoviePilot 添加下载任务失败")
		return
	}
	if ok, message := moviePilotResponseStatus(result); !ok {
		failWithCode(w, http.StatusBadGateway, ErrServiceUnavailable, firstNonEmpty(message, "MoviePilot 添加下载任务失败"))
		return
	}
	remainingPoints := a.store().Signin(user.UID).Points
	if cost > 0 {
		_, si, err := a.store().SpendSigninPointsAndUpdateUser(user.UID, cost, nil)
		if err != nil {
			if errors.Is(err, store.ErrInsufficientPoints) {
				failWithCode(w, http.StatusConflict, ErrSigninInsufficientPoints, "小兔不足，无法提交下载")
				return
			}
			statusFromError(w, err)
			return
		}
		remainingPoints = si.Points
	}
	a.audit(r, "moviepilot_download", "media", 0, map[string]any{"points_cost": cost, "remaining_points": remainingPoints})
	ok(w, "下载任务已提交", map[string]any{"result": result, "points_cost": cost, "remaining_points": remainingPoints})
}

func (a *App) moviePilotRequest(ctx context.Context, method, apiPath string, body any, dst any) error {
	return a.moviePilotRequestWithTimeout(ctx, method, apiPath, body, dst, 10*time.Second)
}

func (a *App) moviePilotRequestWithTimeout(ctx context.Context, method, apiPath string, body any, dst any, timeout time.Duration) error {
	base, err := moviePilotBaseURL(a.cfg().MoviePilotURL)
	if err != nil {
		return err
	}
	token, err := a.moviePilotToken(ctx, base)
	if err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+apiPath, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", token)
	return doJSONRequestWithTimeout(req, dst, timeout)
}

func (a *App) moviePilotToken(ctx context.Context, base string) (string, error) {
	if token := strings.TrimSpace(a.cfg().MoviePilotAccessToken); token != "" {
		if !strings.Contains(token, " ") {
			token = "Bearer " + token
		}
		return token, nil
	}
	username := strings.TrimSpace(a.cfg().MoviePilotUsername)
	password := a.cfg().MoviePilotPassword
	if username == "" || password == "" {
		return "", fmt.Errorf("MoviePilot credentials are not configured")
	}
	form := url.Values{"username": {username}, "password": {password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/login/access-token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("MoviePilot login status %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return "", err
	}
	access := asString(payload["access_token"])
	if access == "" {
		return "", fmt.Errorf("MoviePilot login returned no access token")
	}
	tokenType := firstNonEmpty(asString(payload["token_type"]), "Bearer")
	return tokenType + " " + access, nil
}

func moviePilotBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("invalid MoviePilot URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid MoviePilot URL components")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func (a *App) moviePilotDownloadCost(payload map[string]any) int {
	perGB := clamp(a.cfg().MoviePilotDownloadPointsPerGB, 1, 1000000)
	size := readMoviePilotDownloadSize(payload)
	if size <= 0 {
		return perGB
	}
	const gib = int64(1024 * 1024 * 1024)
	gbUnits := int(size / gib)
	if size%gib != 0 {
		gbUnits++
	}
	if gbUnits <= 0 {
		gbUnits = 1
	}
	return gbUnits * perGB
}

func readMoviePilotDownloadSize(payload map[string]any) int64 {
	for _, key := range []string{"size", "torrent_size", "total_size"} {
		if size := int64(numeric(payload[key])); size > 0 {
			return size
		}
		if size := parseMoviePilotSize(payload[key]); size > 0 {
			return size
		}
	}
	if torrent, _ := payload["torrent_info"].(map[string]any); torrent != nil {
		for _, key := range []string{"size", "torrent_size", "total_size"} {
			if size := int64(numeric(torrent[key])); size > 0 {
				return size
			}
			if size := parseMoviePilotSize(torrent[key]); size > 0 {
				return size
			}
		}
	}
	if torrent, _ := payload["torrent_in"].(map[string]any); torrent != nil {
		for _, key := range []string{"size", "torrent_size", "total_size"} {
			if size := int64(numeric(torrent[key])); size > 0 {
				return size
			}
			if size := parseMoviePilotSize(torrent[key]); size > 0 {
				return size
			}
		}
	}
	return 0
}

func parseMoviePilotSize(value any) int64 {
	text, ok := value.(string)
	if !ok {
		return 0
	}
	text = strings.ToUpper(strings.TrimSpace(strings.ReplaceAll(text, " ", "")))
	if text == "" {
		return 0
	}
	units := []struct {
		suffix string
		scale  float64
	}{
		{"TIB", math.Pow(1024, 4)},
		{"TB", math.Pow(1024, 4)},
		{"T", math.Pow(1024, 4)},
		{"GIB", math.Pow(1024, 3)},
		{"GB", math.Pow(1024, 3)},
		{"G", math.Pow(1024, 3)},
		{"MIB", math.Pow(1024, 2)},
		{"MB", math.Pow(1024, 2)},
		{"M", math.Pow(1024, 2)},
		{"KIB", 1024},
		{"KB", 1024},
		{"K", 1024},
		{"B", 1},
	}
	for _, unit := range units {
		if !strings.HasSuffix(text, unit.suffix) {
			continue
		}
		numberText := strings.TrimSpace(strings.TrimSuffix(text, unit.suffix))
		numberText = strings.TrimSuffix(numberText, "/S")
		numberText = strings.TrimSuffix(numberText, "PS")
		number, err := strconv.ParseFloat(numberText, 64)
		if err != nil || number <= 0 {
			return 0
		}
		return int64(number * unit.scale)
	}
	return 0
}

func normalizeMoviePilotDownloadPayload(payload map[string]any) map[string]any {
	if len(payload) == 0 {
		return payload
	}
	if torrent, ok := payload["torrent_in"].(map[string]any); ok && len(torrent) > 0 {
		return payload
	}
	requestPayload := map[string]any{}
	for _, key := range []string{"tmdbid", "doubanid", "downloader", "save_path"} {
		if value, ok := payload[key]; ok {
			requestPayload[key] = value
		}
	}
	requestPayload["torrent_in"] = payload
	return requestPayload
}

func moviePilotResponseStatus(result any) (bool, string) {
	body, ok := result.(map[string]any)
	if !ok || body == nil {
		return true, ""
	}
	success, exists := body["success"]
	if !exists {
		return true, ""
	}
	if boolish(success) {
		return true, readMoviePilotMessage(body["message"])
	}
	return false, readMoviePilotMessage(body["message"])
}

func readMoviePilotMessage(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}
