package store

import "time"

const maxStoredPlaybackRecords = 10000

func (s *Store) AddPlaybackRecord(record PlaybackRecord) error {
	_, err := s.AddPlaybackRecordIdempotent(record)
	return err
}

// AddPlaybackRecordIdempotent 在 (UID, ItemID, PlayedAt) 已经存在时跳过写入，
// 返回 inserted=false。bangumi webhook 没有 timestamp + nonce 强签名，
// 攻击者重放同一份合法请求体本来会让 PlaybackRecords 不停堆积；这里以
// (uid + 媒体条目 + 播放秒) 作为天然幂等键阻断 replay 放大写入。
//
// 真实业务里同一用户在同一秒 stop 同一条目的概率为 0：webhook 由 Emby
// "PlaybackStopped" 事件触发，事件之间至少相隔几秒。即使因网络重试导致同
// 一事件重发，时间戳也会被 server 端的 time.Now() 在重试间隔内推进——所
// 以受害的并不是合法用户，幂等去重不会丢失任何真实播放。
//
// 注意：本方法仍然假定调用方已经做过身份校验。它只阻止"已校验请求"被
// 多次重放——签名伪造 / token 泄露这类外部信任问题不在这里处理。
func (s *Store) AddPlaybackRecordIdempotent(record PlaybackRecord) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(); err != nil {
		return false, err
	}
	if record.PlayedAt == 0 {
		record.PlayedAt = time.Now().Unix()
	}
	// 只对 (UID, ItemID, PlayedAt) 三元组都齐的记录做幂等检查。ItemID 为空
	// 是 admin 手动注入的特殊路径（"测试事件"），保留旧行为允许多写。
	if record.UID != 0 && record.ItemID != "" {
		for _, existing := range s.state.PlaybackRecords {
			if existing.UID == record.UID && existing.ItemID == record.ItemID && existing.PlayedAt == record.PlayedAt {
				return false, nil
			}
		}
	}
	s.state.PlaybackRecords = append([]PlaybackRecord{record}, s.state.PlaybackRecords...)
	if len(s.state.PlaybackRecords) > maxStoredPlaybackRecords {
		s.state.PlaybackRecords = s.state.PlaybackRecords[:maxStoredPlaybackRecords]
	}
	if err := s.saveLocked(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) UpsertLivePlaybackRecord(record PlaybackRecord, mergeWindow time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(); err != nil {
		return false, err
	}
	if record.UID == 0 || record.ItemID == "" || record.Duration <= 0 {
		return false, nil
	}
	if record.PlayedAt == 0 {
		record.PlayedAt = time.Now().Unix()
	}
	if mergeWindow <= 0 {
		mergeWindow = 12 * time.Hour
	}
	mergeSeconds := int64(mergeWindow / time.Second)
	for i, existing := range s.state.PlaybackRecords {
		if existing.UID != record.UID || existing.ItemID != record.ItemID {
			continue
		}
		delta := existing.PlayedAt - record.PlayedAt
		if delta < 0 {
			delta = -delta
		}
		if delta > mergeSeconds {
			continue
		}
		changed := false
		if record.Duration > existing.Duration {
			s.state.PlaybackRecords[i].Duration = record.Duration
			changed = true
		}
		if record.PlayedAt < existing.PlayedAt {
			s.state.PlaybackRecords[i].PlayedAt = record.PlayedAt
			changed = true
		}
		if record.Title != "" && existing.Title == "" {
			s.state.PlaybackRecords[i].Title = record.Title
			changed = true
		}
		if record.SeriesName != "" && existing.SeriesName == "" {
			s.state.PlaybackRecords[i].SeriesName = record.SeriesName
			changed = true
		}
		if record.MediaType != "" && existing.MediaType == "" {
			s.state.PlaybackRecords[i].MediaType = record.MediaType
			changed = true
		}
		if record.IndexNumber != 0 && existing.IndexNumber == 0 {
			s.state.PlaybackRecords[i].IndexNumber = record.IndexNumber
			changed = true
		}
		if !changed {
			return false, nil
		}
		updated := s.state.PlaybackRecords[i]
		copy(s.state.PlaybackRecords[1:i+1], s.state.PlaybackRecords[0:i])
		s.state.PlaybackRecords[0] = updated
		if err := s.saveLocked(); err != nil {
			return false, err
		}
		return true, nil
	}
	s.state.PlaybackRecords = append([]PlaybackRecord{record}, s.state.PlaybackRecords...)
	if len(s.state.PlaybackRecords) > maxStoredPlaybackRecords {
		s.state.PlaybackRecords = s.state.PlaybackRecords[:maxStoredPlaybackRecords]
	}
	if err := s.saveLocked(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) PlaybackRecords(uid int64, since int64, limit int) []PlaybackRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > maxStoredPlaybackRecords {
		limit = maxStoredPlaybackRecords
	}
	out := make([]PlaybackRecord, 0, minInt(limit, len(s.state.PlaybackRecords)))
	for _, record := range s.state.PlaybackRecords {
		if uid != 0 && record.UID != uid {
			continue
		}
		if since > 0 && record.PlayedAt < since {
			continue
		}
		out = append(out, record)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
