package gotdxadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bensema/gotdx/proto"
	"github.com/bensema/gotdx/types"
	"github.com/780662833/aShare-mvp/internal/market/universe"
	"github.com/780662833/aShare-mvp/internal/marketdata"
)

type fakeClient struct {
	listByMarket map[uint8][]proto.Security
	listErr      map[uint8]error
	bars         []proto.SecurityBar
	barsErr      error
}

func (f *fakeClient) StockAll(market uint8) ([]proto.Security, error) {
	if err := f.listErr[market]; err != nil {
		return nil, err
	}
	return f.listByMarket[market], nil
}

func (f *fakeClient) StockKLine(uint16, uint8, string, uint16, uint16, uint16, uint16) ([]proto.SecurityBar, error) {
	return f.bars, f.barsErr
}

func (f *fakeClient) Disconnect() {}

func TestFetchUniverseReportsPerMarketCompleteness(t *testing.T) {
	client := &fakeClient{
		listByMarket: map[uint8][]proto.Security{
			types.MarketSH.Uint8(): {{Code: "600519", Name: "贵州茅台"}},
			types.MarketSZ.Uint8(): {{Code: "000001", Name: "平安银行"}},
			types.MarketBJ.Uint8(): {{Code: "920001", Name: "示例北交所股票"}},
		},
	}
	adapter, err := NewWithClient(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := adapter.FetchUniverse(context.Background())
	if err != nil {
		t.Fatalf("FetchUniverse 失败: %v", err)
	}
	if !got.Complete() || len(got.Markets) != 3 {
		t.Fatalf("市场完整性状态错误: %+v", got.Markets)
	}
	if len(got.Securities) != 3 {
		t.Fatalf("普通股票数量不符: %d", len(got.Securities))
	}
	for _, item := range got.Securities {
		if item.ListStatus != universe.ListingUnknown {
			t.Fatalf("TDX 不应伪造上市状态: %+v", item)
		}
	}
}

func TestFetchUniverseDoesNotTreatPartialListAsComplete(t *testing.T) {
	client := &fakeClient{
		listByMarket: map[uint8][]proto.Security{
			types.MarketSH.Uint8(): {{Code: "600519", Name: "贵州茅台"}},
			types.MarketBJ.Uint8(): {{Code: "920001", Name: "示例北交所股票"}},
		},
		listErr: map[uint8]error{types.MarketSZ.Uint8(): errors.New("timeout")},
	}
	adapter, _ := NewWithClient(client, Config{})
	got, err := adapter.FetchUniverse(context.Background())
	if err == nil {
		t.Fatal("市场失败时必须返回错误")
	}
	if got.Complete() {
		t.Fatal("部分名单不能标记为完整")
	}
	if len(got.Markets) != 3 {
		t.Fatalf("必须记录三个市场结果: %+v", got.Markets)
	}
}

func TestFetchBarsRebuildsShanghaiWallTimeAndMarksFormingBar(t *testing.T) {
	client := &fakeClient{
		bars: []proto.SecurityBar{
			{Year: 2026, Month: 10, Day: 30, Hour: 10, Minute: 0,
				Open: 100, High: 103, Low: 99, Close: 102, Vol: 1000, Amount: 102000},
			{Year: 2026, Month: 10, Day: 30, Hour: 10, Minute: 30,
				Open: 102, High: 104, Low: 101, Close: 103, Vol: 1200, Amount: 123600},
		},
	}
	adapter, err := NewWithClient(client, Config{SettleDelay: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	adapter.now = func() time.Time {
		return time.Date(2026, 10, 30, 10, 31, 30, 0, marketdata.ShanghaiLocation())
	}
	got, err := adapter.FetchBars(context.Background(), "600519.SH", marketdata.FrequencyM30, 250)
	if err != nil {
		t.Fatalf("FetchBars 失败: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("K 线数量错误: %d", len(got))
	}
	if got[0].BarTime.Format(time.RFC3339) != "2026-10-30T10:00:00+08:00" {
		t.Fatalf("未按上海墙上时间规范化: %s", got[0].BarTime.Format(time.RFC3339))
	}
	if !got[0].IsComplete || got[1].IsComplete {
		t.Fatalf("完成状态判断错误: first=%v second=%v", got[0].IsComplete, got[1].IsComplete)
	}
	if got[0].VolumeUnit != marketdata.VolumeSourceNative || got[0].AdjustMode != marketdata.AdjustQFQ {
		t.Fatalf("复权或成交量单位错误: %+v", got[0])
	}
}

func TestFrequencyMappingAndCodeParsing(t *testing.T) {
	if _, err := categoryForFrequency(marketdata.FrequencyD1); err != nil {
		t.Fatal(err)
	}
	if _, err := categoryForFrequency(marketdata.FrequencyM30); err != nil {
		t.Fatal(err)
	}
	if _, err := categoryForFrequency(marketdata.FrequencyM60); err != nil {
		t.Fatal(err)
	}
	if _, _, err := parseTsCode("600519"); err == nil {
		t.Fatal("不带交易所后缀的代码应被拒绝")
	}
}
