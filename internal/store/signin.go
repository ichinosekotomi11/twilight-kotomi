package store

import "time"

const signinDateLayout = "2006-01-02"

var signinResetLocation = time.FixedZone("Asia/Shanghai", 8*3600)

func signinBusinessDate(now time.Time) string {
	// 签到日界线按北京时间中午 12:00 计算：
	// 例如 2026-06-29 11:59 仍属于 2026-06-28 的签到日，
	// 2026-06-29 12:00 开始才进入 2026-06-29。
	return now.In(signinResetLocation).Add(-12 * time.Hour).Format(signinDateLayout)
}

func (s *Store) Signin(uid int64) Signin {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Signin[uid]
}

func (s *Store) AddSignin(uid int64, points int) (Signin, bool, error) {
	return s.AddSigninWithOptions(uid, points, nil, true)
}

func (s *Store) AddSigninWithOptions(uid int64, dailyPoints int, bonusForStreak func(int) int, resetAfterMiss bool) (Signin, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(); err != nil {
		return Signin{}, false, err
	}
	now := time.Now()
	today := signinBusinessDate(now)
	yesterday := signinBusinessDate(now.AddDate(0, 0, -1))
	si := s.state.Signin[uid]
	if si.UID == 0 {
		si.UID = uid
	}
	if si.LongestStreak < si.Streak {
		si.LongestStreak = si.Streak
	}
	if si.LastSignin == today {
		return si, false, nil
	}
	if si.LastSignin == yesterday {
		si.Streak++
	} else if si.LastSignin != "" && !resetAfterMiss {
		si.Streak++
	} else {
		si.Streak = 1
	}
	if si.Streak > si.LongestStreak {
		si.LongestStreak = si.Streak
	}
	bonusPoints := 0
	if bonusForStreak != nil {
		bonusPoints = bonusForStreak(si.Streak)
	}
	totalPoints := dailyPoints + bonusPoints
	si.LastSignin = today
	si.Points += totalPoints
	si.Records = append(si.Records, SigninRecord{Date: today, Points: dailyPoints, BonusPoints: bonusPoints, Total: totalPoints, Streak: si.Streak, CreatedAt: now.Unix()})
	s.state.Signin[uid] = si
	return si, true, s.saveLocked()
}

func (s *Store) SpendSigninPointsAndUpdateUser(uid int64, cost int, fn func(*User) error) (User, Signin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var updated User
	var updatedSignin Signin
	err := s.mutateAndSaveLocked(func() error {
		u, ok := s.state.Users[uid]
		if !ok {
			return ErrNotFound
		}
		si := s.state.Signin[uid]
		if si.UID == 0 {
			si.UID = uid
		}
		if cost <= 0 {
			return ErrConflict
		}
		if si.Points < cost {
			return ErrInsufficientPoints
		}
		si.Points -= cost
		if fn != nil {
			if err := fn(&u); err != nil {
				return err
			}
		}
		s.state.Signin[uid] = si
		s.state.Users[uid] = u
		updated = u
		updatedSignin = si
		return nil
	})
	if err != nil {
		return User{}, Signin{}, err
	}
	return updated, updatedSignin, nil
}

// SpendSigninPointsAndCreateRegCode atomically deducts points and creates a
// registration code so a failed save can never charge the user without
// issuing the code.
func (s *Store) SpendSigninPointsAndCreateRegCode(uid int64, cost int, code RegCode) (Signin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var updated Signin
	err := s.mutateAndSaveLocked(func() error {
		if _, ok := s.state.Users[uid]; !ok {
			return ErrNotFound
		}
		if cost <= 0 || code.Code == "" {
			return ErrConflict
		}
		if _, exists := s.state.RegCodes[code.Code]; exists {
			return ErrConflict
		}
		if _, exists := s.state.InviteCodes[code.Code]; exists {
			return ErrConflict
		}
		si := s.state.Signin[uid]
		if si.UID == 0 {
			si.UID = uid
		}
		if si.Points < cost {
			return ErrInsufficientPoints
		}
		si.Points -= cost
		now := time.Now().Unix()
		if code.CreatedAt == 0 {
			code.CreatedAt = now
		}
		if code.CreatedTime == 0 {
			code.CreatedTime = code.CreatedAt
		}
		if code.ValidityTime == 0 {
			code.ValidityTime = -1
		}
		if code.UseCountLimit == 0 {
			code.UseCountLimit = 1
		}
		code.Active = true
		code.Source = "points_exchange"
		code.CreatorUID = uid
		s.state.Signin[uid] = si
		s.state.RegCodes[code.Code] = code
		updated = si
		return nil
	})
	if err != nil {
		return Signin{}, err
	}
	return updated, nil
}
