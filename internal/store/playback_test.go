package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPlaybackRecordsFiltersDefaultsAndLimits(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	records := []PlaybackRecord{
		{UID: 1, ItemID: "old", PlayedAt: 10},
		{UID: 2, ItemID: "other", PlayedAt: 20},
		{UID: 1, ItemID: "mid", PlayedAt: 30},
		{UID: 1, ItemID: "new", PlayedAt: 40},
	}
	for _, record := range records {
		if err := st.AddPlaybackRecord(record); err != nil {
			t.Fatal(err)
		}
	}

	uidRecords := st.PlaybackRecords(1, 0, 10)
	if len(uidRecords) != 3 || uidRecords[0].ItemID != "new" || uidRecords[2].ItemID != "old" {
		t.Fatalf("unexpected uid records: %#v", uidRecords)
	}

	recent := st.PlaybackRecords(1, 25, 1)
	if len(recent) != 1 || recent[0].ItemID != "new" {
		t.Fatalf("unexpected recent limited records: %#v", recent)
	}

	if err := st.AddPlaybackRecord(PlaybackRecord{UID: 3, ItemID: "default-time"}); err != nil {
		t.Fatal(err)
	}
	defaulted := st.PlaybackRecords(3, 0, 1)
	if len(defaulted) != 1 || defaulted[0].PlayedAt == 0 {
		t.Fatalf("expected PlayedAt default, got %#v", defaulted)
	}
}

func TestPlaybackRecordsPruneToMax(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	st.mu.Lock()
	st.state.PlaybackRecords = make([]PlaybackRecord, maxStoredPlaybackRecords)
	for i := range st.state.PlaybackRecords {
		st.state.PlaybackRecords[i] = PlaybackRecord{UID: 1, ItemID: "bulk", PlayedAt: int64(maxStoredPlaybackRecords - i)}
	}
	st.mu.Unlock()
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	if err := st.AddPlaybackRecord(PlaybackRecord{UID: 1, ItemID: "new", PlayedAt: int64(maxStoredPlaybackRecords + 1)}); err != nil {
		t.Fatal(err)
	}
	if got := len(st.state.PlaybackRecords); got != maxStoredPlaybackRecords {
		t.Fatalf("expected prune to %d records, got %d", maxStoredPlaybackRecords, got)
	}
	latest := st.PlaybackRecords(1, 0, 1)
	if len(latest) != 1 || latest[0].ItemID != "new" {
		t.Fatalf("unexpected latest record after prune: %#v", latest)
	}
}

func TestUpsertLivePlaybackRecordMergesProgress(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	changed, err := st.UpsertLivePlaybackRecord(PlaybackRecord{UID: 1, ItemID: "episode-live", Title: "第 1 集", MediaType: "Video", Duration: 60, PlayedAt: 1000}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected first live record insert")
	}
	changed, err = st.UpsertLivePlaybackRecord(PlaybackRecord{UID: 1, ItemID: "episode-live", Duration: 120, PlayedAt: 1020}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected live record duration update")
	}
	records := st.PlaybackRecords(1, 0, 10)
	if len(records) != 1 {
		t.Fatalf("expected one merged record, got %d: %#v", len(records), records)
	}
	if records[0].Duration != 120 {
		t.Fatalf("duration=%d want=120", records[0].Duration)
	}
	if records[0].PlayedAt != 1000 {
		t.Fatalf("played_at=%d want=1000", records[0].PlayedAt)
	}
}
