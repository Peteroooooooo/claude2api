package repository

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestTPSUsesWholeRequestAndExcludesReplay(t *testing.T) {
	for _, tc := range []struct {
		name string
		log  APILog
		want float64
	}{
		{"buffered answer from log 54", APILog{OutputTokens: 142, FirstTokenMs: 5890, DurationMs: 5964, TPS: 1918.9}, 142000.0 / 5964},
		{"replay from log 32", APILog{OutputTokens: 17, DurationMs: 1, SessionAction: "replay", TPS: 17000}, 0},
		{"empty response", APILog{DurationMs: 2047, TPS: 123}, 0},
		{"missing elapsed time", APILog{OutputTokens: 100, TPS: 123}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.log.calculateTPS()
			if math.Abs(tc.log.TPS-tc.want) > 0.000001 {
				t.Fatalf("TPS=%.6f want %.6f", tc.log.TPS, tc.want)
			}
		})
	}
}

func TestAPILogStatsCoverRetainedLogsAcrossPages(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "logs.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	oldDB := db
	db = conn
	t.Cleanup(func() { _ = CloseDB(); db = oldDB })
	if err := db.AutoMigrate(&APILog{}); err != nil {
		t.Fatal(err)
	}
	check := func(want APILogStats) {
		t.Helper()
		got, err := GetAPILogStats()
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("stats=%+v want %+v", got, want)
		}
	}
	check(APILogStats{})
	for _, row := range []APILog{
		{Success: true, InputTokens: 100, OutputTokens: 10},
		{Success: false, InputTokens: 200},
		{Success: true, InputTokens: 300, OutputTokens: 20, SessionAction: "replay"},
	} {
		InsertAPILog(row)
	}
	if page := ListAPILogs(1, 1); len(page) != 1 || page[0].InputTokens != 200 {
		t.Fatalf("unexpected page: %+v", page)
	}
	check(APILogStats{Calls: 3, Success: 2, Failed: 1, InputTokens: 600, OutputTokens: 30, TotalTokens: 630})
	if removed := DeleteAPILogs([]int64{2}); removed != 1 {
		t.Fatalf("removed=%d", removed)
	}
	check(APILogStats{Calls: 2, Success: 2, InputTokens: 400, OutputTokens: 30, TotalTokens: 430})
	if removed := TrimAPILogs(1); removed != 1 {
		t.Fatalf("trimmed=%d", removed)
	}
	check(APILogStats{Calls: 1, Success: 1, InputTokens: 300, OutputTokens: 20, TotalTokens: 320})
	TrimAPILogs(0)
	check(APILogStats{})
}
