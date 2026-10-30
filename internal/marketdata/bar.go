// Package marketdata 定义 D2 统一 K 线契约和最小质量门。
package marketdata

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const ShanghaiOffsetSeconds = 8 * 60 * 60

var shanghaiLocation = time.FixedZone("Asia/Shanghai", ShanghaiOffsetSeconds)

type Frequency string

const (
	FrequencyD1  Frequency = "D1"
	FrequencyM30 Frequency = "M30"
	FrequencyM60 Frequency = "M60"
)

func (f Frequency) Valid() bool {
	switch f {
	case FrequencyD1, FrequencyM30, FrequencyM60:
		return true
	default:
		return false
	}
}

func (f Frequency) CZSCName() (string, error) {
	switch f {
	case FrequencyD1:
		return "日线", nil
	case FrequencyM30:
		return "30分钟", nil
	case FrequencyM60:
		return "60分钟", nil
	default:
		return "", fmt.Errorf("V1 不支持周期: %q", f)
	}
}

type AdjustMode string

const (
	AdjustQFQ     AdjustMode = "QFQ"
	AdjustHFQ     AdjustMode = "HFQ"
	AdjustNone    AdjustMode = "NONE"
	AdjustUnknown AdjustMode = "UNKNOWN"
)

type VolumeUnit string

const (
	VolumeShare        VolumeUnit = "SHARE"
	VolumeLot          VolumeUnit = "LOT"
	VolumeSourceNative VolumeUnit = "SOURCE_NATIVE"
	VolumeUnknown      VolumeUnit = "UNKNOWN"
)

// BarTime 永远表示 K 线结束时间，且必须显式处于 UTC+08:00。
// 成交量在量纲确认前允许 SOURCE_NATIVE / UNKNOWN，不伪装成股数。
type Bar struct {
	TsCode     string     `json:"ts_code"`
	Frequency  Frequency  `json:"freq"`
	BarTime    time.Time  `json:"bar_time"`
	Open       float64    `json:"open"`
	High       float64    `json:"high"`
	Low        float64    `json:"low"`
	Close      float64    `json:"close"`
	Volume     float64    `json:"volume"`
	Amount     float64    `json:"amount"`
	AdjustMode AdjustMode `json:"adjust_mode"`
	VolumeUnit VolumeUnit `json:"volume_unit"`
	Source     string     `json:"source"`
	IsComplete bool       `json:"is_complete"`
	FetchedAt  time.Time  `json:"fetched_at"`
}

func ShanghaiLocation() *time.Location { return shanghaiLocation }

// ShanghaiWallTime 使用日期/时分字段重建交易所墙上时间。
// gotdx 的 DateTime 目前由 time.Local 构造，不能简单调用 .In() 修正。
func ShanghaiWallTime(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, shanghaiLocation)
}

func (b Bar) Validate() error {
	if strings.TrimSpace(b.TsCode) == "" {
		return errors.New("ts_code 不能为空")
	}
	if !b.Frequency.Valid() {
		return fmt.Errorf("无效 freq: %q", b.Frequency)
	}
	if b.BarTime.IsZero() || !isShanghaiOffset(b.BarTime) {
		return errors.New("bar_time 必须是带 +08:00 偏移的 K 线结束时间")
	}
	if b.FetchedAt.IsZero() || !isShanghaiOffset(b.FetchedAt) {
		return errors.New("fetched_at 必须是带 +08:00 偏移的采集时间")
	}
	if !finitePositive(b.Open) || !finitePositive(b.High) ||
		!finitePositive(b.Low) || !finitePositive(b.Close) {
		return errors.New("OHLC 必须是有限正数")
	}
	if b.High < math.Max(b.Open, math.Max(b.Close, b.Low)) {
		return errors.New("high 小于 open/close/low 中的最大值")
	}
	if b.Low > math.Min(b.Open, math.Min(b.Close, b.High)) {
		return errors.New("low 大于 open/close/high 中的最小值")
	}
	if !finiteNonNegative(b.Volume) || !finiteNonNegative(b.Amount) {
		return errors.New("volume 和 amount 必须为有限非负数")
	}
	switch b.AdjustMode {
	case AdjustQFQ, AdjustHFQ, AdjustNone, AdjustUnknown:
	default:
		return fmt.Errorf("无效 adjust_mode: %q", b.AdjustMode)
	}
	switch b.VolumeUnit {
	case VolumeShare, VolumeLot, VolumeSourceNative, VolumeUnknown:
	default:
		return fmt.Errorf("无效 volume_unit: %q", b.VolumeUnit)
	}
	if strings.TrimSpace(b.Source) == "" {
		return errors.New("source 不能为空")
	}
	return nil
}

// ValidateBatch 校验单次写入必须属于同一证券、周期、复权、来源和成交量量纲，且时间严格递增。
// 不主动排序或混合多数据源；来源变化应先做重叠区间核对。
func ValidateBatch(bars []Bar) error {
	if len(bars) == 0 {
		return errors.New("K 线批次不能为空")
	}
	first := bars[0]
	if err := first.Validate(); err != nil {
		return fmt.Errorf("bars[0]: %w", err)
	}
	prev := first.BarTime
	for i := 1; i < len(bars); i++ {
		bar := bars[i]
		if err := bar.Validate(); err != nil {
			return fmt.Errorf("bars[%d]: %w", i, err)
		}
		if bar.TsCode != first.TsCode || bar.Frequency != first.Frequency ||
			bar.AdjustMode != first.AdjustMode || bar.Source != first.Source ||
			bar.VolumeUnit != first.VolumeUnit {
			return fmt.Errorf("bars[%d] 与批次首根证券/周期/复权/来源/成交量量纲不一致", i)
		}
		if !bar.BarTime.After(prev) {
			return fmt.Errorf("bars[%d] 时间重复或乱序", i)
		}
		prev = bar.BarTime
	}
	return nil
}

func isShanghaiOffset(t time.Time) bool {
	_, offset := t.Zone()
	return offset == ShanghaiOffsetSeconds
}

func finitePositive(v float64) bool {
	return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}

func finiteNonNegative(v float64) bool {
	return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}
