package marketdata

import (
	"testing"
	"time"
)

func validTestBar(at time.Time) Bar {
	return Bar{
		TsCode: "600519.SH", Frequency: FrequencyM30, BarTime: at,
		Open: 100, High: 103, Low: 99, Close: 102, Volume: 1000, Amount: 102000,
		AdjustMode: AdjustQFQ, VolumeUnit: VolumeSourceNative, Source: "test",
		IsComplete: true, FetchedAt: time.Date(2026, 10, 30, 16, 0, 0, 0, ShanghaiLocation()),
	}
}

func TestValidateBarAndBatch(t *testing.T) {
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, ShanghaiLocation())
	first := validTestBar(at)
	if err := first.Validate(); err != nil {
		t.Fatalf("有效 K 线被拒绝: %v", err)
	}
	second := validTestBar(at.Add(30 * time.Minute))
	if err := ValidateBatch([]Bar{first, second}); err != nil {
		t.Fatalf("有效批次被拒绝: %v", err)
	}
}

func TestValidateBatchRejectsDuplicateAndMixedSeries(t *testing.T) {
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, ShanghaiLocation())
	first := validTestBar(at)
	duplicate := validTestBar(at)
	if err := ValidateBatch([]Bar{first, duplicate}); err == nil {
		t.Fatal("重复时间应当被拒绝")
	}
	mixed := validTestBar(at.Add(30 * time.Minute))
	mixed.TsCode = "000001.SZ"
	if err := ValidateBatch([]Bar{first, mixed}); err == nil {
		t.Fatal("混合证券批次应当被拒绝")
	}
}

func TestValidateBarRejectsInvalidOHLCAndTimezone(t *testing.T) {
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, ShanghaiLocation())
	bar := validTestBar(at)
	bar.High = 98
	if err := bar.Validate(); err == nil {
		t.Fatal("high 小于 open 应当被拒绝")
	}
	bar = validTestBar(at)
	bar.BarTime = at.UTC()
	if err := bar.Validate(); err == nil {
		t.Fatal("UTC 时间不应直接作为上海时间存储")
	}
}
