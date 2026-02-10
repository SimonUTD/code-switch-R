package services

import (
	"testing"
	"time"

	"github.com/daodao97/xgo/xdb"
)

func TestLogServiceStatsSinceRange_DailyBuckets(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	if err := InitDatabase(); err != nil {
		t.Fatalf("InitDatabase: %v", err)
	}

	db, err := xdb.DB("default")
	if err != nil {
		t.Fatalf("xdb.DB: %v", err)
	}
	if _, err := db.Exec("DELETE FROM request_log"); err != nil {
		t.Fatalf("clear request_log: %v", err)
	}

	now := time.Now()
	day0 := startOfDay(now)
	day1 := day0.Add(-24 * time.Hour)
	day2 := day0.Add(-48 * time.Hour)

	insert := func(provider string, at time.Time, input, output int) {
		_, err := db.Exec(`
			INSERT INTO request_log (
				platform, model, provider, http_code,
				input_tokens, output_tokens, cache_create_tokens, cache_read_tokens,
				reasoning_tokens, is_stream, duration_sec, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			"claude",
			"test-model",
			provider,
			200,
			input,
			output,
			0,
			0,
			0,
			0,
			0.1,
			at.Format(timeLayout),
		)
		if err != nil {
			t.Fatalf("insert request_log: %v", err)
		}
	}

	insert("p1", day2.Add(2*time.Hour), 10, 1)
	insert("p1", day1.Add(3*time.Hour), 20, 2)
	insert("p2", day0.Add(4*time.Hour), 30, 3)

	ls := &LogService{}
	stats, err := ls.StatsSinceRange("claude", 3)
	if err != nil {
		t.Fatalf("StatsSinceRange: %v", err)
	}

	if stats.TotalRequests != 3 {
		t.Fatalf("expected total_requests=3, got %d", stats.TotalRequests)
	}
	if len(stats.Series) != 3 {
		t.Fatalf("expected 3 series buckets, got %d (%#v)", len(stats.Series), stats.Series)
	}

	wantDays := []string{
		day2.Format("2006-01-02"),
		day1.Format("2006-01-02"),
		day0.Format("2006-01-02"),
	}

	for i, day := range wantDays {
		if stats.Series[i].Day != day {
			t.Fatalf("expected series[%d].day=%q, got %q", i, day, stats.Series[i].Day)
		}
		if stats.Series[i].TotalRequests != 1 {
			t.Fatalf("expected series[%d].total_requests=1, got %d", i, stats.Series[i].TotalRequests)
		}
	}

	perProvider, err := ls.ProviderStatsSinceRange("claude", 3)
	if err != nil {
		t.Fatalf("ProviderStatsSinceRange: %v", err)
	}
	if len(perProvider) != 2 {
		t.Fatalf("expected 2 providers, got %d (%#v)", len(perProvider), perProvider)
	}
	if perProvider[0].Provider != "p1" || perProvider[0].TotalRequests != 2 {
		t.Fatalf("expected provider p1 with 2 requests first, got %#v", perProvider[0])
	}
	if perProvider[1].Provider != "p2" || perProvider[1].TotalRequests != 1 {
		t.Fatalf("expected provider p2 with 1 request second, got %#v", perProvider[1])
	}
}
