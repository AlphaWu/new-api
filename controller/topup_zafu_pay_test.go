package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupZafuPayUnitTest(t *testing.T, displayType string, quotaPerUnit float64, dailyLimit int) {
	t.Helper()
	oldQuotaPerUnit := common.QuotaPerUnit
	oldDisplayType := operation_setting.GetGeneralSetting().QuotaDisplayType
	oldLimit := setting.ZafuPayDailyLimit
	common.QuotaPerUnit = quotaPerUnit
	operation_setting.GetGeneralSetting().QuotaDisplayType = displayType
	setting.ZafuPayDailyLimit = dailyLimit
	t.Cleanup(func() {
		common.QuotaPerUnit = oldQuotaPerUnit
		operation_setting.GetGeneralSetting().QuotaDisplayType = oldDisplayType
		setting.ZafuPayDailyLimit = oldLimit
	})
}

func TestZafuPayAmountUnitConversion(t *testing.T) {
	t.Run("currency display type passes through", func(t *testing.T) {
		setupZafuPayUnitTest(t, operation_setting.QuotaDisplayTypeUSD, 500000, 0)
		assert.Equal(t, int64(42), zafuPayAmountToBase(42))
		assert.Equal(t, int64(42), zafuPayBaseToDisplay(42))
	})

	t.Run("tokens display type converts in both directions", func(t *testing.T) {
		setupZafuPayUnitTest(t, operation_setting.QuotaDisplayTypeTokens, 500000, 0)
		// 落库方向：tokens 折算为币种单位，非整倍数向下取整
		assert.Equal(t, int64(2), zafuPayAmountToBase(1_000_000))
		assert.Equal(t, int64(1), zafuPayAmountToBase(999_999))
		// 展示方向：币种单位还原为 tokens
		assert.Equal(t, int64(1_000_000), zafuPayBaseToDisplay(2))
	})
}

func TestCheckZafuPayDailyLimit(t *testing.T) {
	oldDB := model.DB
	oldQuotaPerUnit := common.QuotaPerUnit
	oldDisplayType := operation_setting.GetGeneralSetting().QuotaDisplayType
	oldLimit := setting.ZafuPayDailyLimit
	common.QuotaPerUnit = 500000

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}))
	model.DB = db
	t.Cleanup(func() {
		common.QuotaPerUnit = oldQuotaPerUnit
		operation_setting.GetGeneralSetting().QuotaDisplayType = oldDisplayType
		setting.ZafuPayDailyLimit = oldLimit
		model.DB = oldDB
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})

	dayStart := zafuPayDayStartTs()
	const userId = 42

	newOrder := func(seq int, amount int64, provider, status string, createAt int64) model.TopUp {
		return model.TopUp{
			UserId:          userId,
			Amount:          amount,
			TradeNo:         fmt.Sprintf("USR%dNO-%04d", userId, seq),
			PaymentProvider: provider,
			Status:          status,
			CreateTime:      createAt,
		}
	}

	testCases := []struct {
		name        string
		displayType string
		limit       int
		existing    []model.TopUp
		amount      int64
		wantReject  bool
		wantData    string
	}{
		{
			name:        "limit not configured allows top-up",
			displayType: operation_setting.QuotaDisplayTypeUSD,
			limit:       0,
			existing: []model.TopUp{
				newOrder(1, 9999, model.PaymentProviderZafuPay, common.TopUpStatusSuccess, dayStart+60),
			},
			amount: 100,
		},
		{
			name:        "amount exactly filling remaining quota allowed",
			displayType: operation_setting.QuotaDisplayTypeUSD,
			limit:       10,
			existing: []model.TopUp{
				newOrder(2, 4, model.PaymentProviderZafuPay, common.TopUpStatusSuccess, dayStart+60),
			},
			amount: 6,
		},
		{
			name:        "amount exceeding remaining quota rejected",
			displayType: operation_setting.QuotaDisplayTypeUSD,
			limit:       10,
			existing: []model.TopUp{
				newOrder(3, 9, model.PaymentProviderZafuPay, common.TopUpStatusSuccess, dayStart+60),
			},
			amount:     2,
			wantReject: true,
			wantData:   "今日一卡通充值已达上限 10，请明日再试",
		},
		{
			name:        "orders before today not counted",
			displayType: operation_setting.QuotaDisplayTypeUSD,
			limit:       10,
			existing: []model.TopUp{
				newOrder(4, 999, model.PaymentProviderZafuPay, common.TopUpStatusSuccess, dayStart-1),
			},
			amount: 10,
		},
		{
			name:        "non-success orders not counted",
			displayType: operation_setting.QuotaDisplayTypeUSD,
			limit:       10,
			existing: []model.TopUp{
				newOrder(5, 999, model.PaymentProviderZafuPay, common.TopUpStatusPending, dayStart+60),
				newOrder(6, 999, model.PaymentProviderZafuPay, common.TopUpStatusFailed, dayStart+120),
			},
			amount: 10,
		},
		{
			name:        "other payment providers not counted",
			displayType: operation_setting.QuotaDisplayTypeUSD,
			limit:       10,
			existing: []model.TopUp{
				newOrder(7, 999, model.PaymentProviderEpay, common.TopUpStatusSuccess, dayStart+60),
			},
			amount: 10,
		},
		{
			name:        "tokens display type compares in base currency units",
			displayType: operation_setting.QuotaDisplayTypeTokens,
			limit:       10_000_000, // 展示单位（tokens）→ 折算 20 个币种单位
			existing: []model.TopUp{
				// 已落库 19 个币种单位；本次 1_000_000 tokens 折算 2 个币种单位，19+2 > 20
				newOrder(8, 19, model.PaymentProviderZafuPay, common.TopUpStatusSuccess, dayStart+60),
			},
			amount:     1_000_000,
			wantReject: true,
			wantData:   "今日一卡通充值已达上限 10000000，请明日再试",
		},
		{
			name:        "tokens display type within base quota allowed",
			displayType: operation_setting.QuotaDisplayTypeTokens,
			limit:       10_000_000, // 折算 20 个币种单位
			existing: []model.TopUp{
				newOrder(9, 18, model.PaymentProviderZafuPay, common.TopUpStatusSuccess, dayStart+60),
			},
			amount: 1_000_000, // 折算 2 个币种单位，18+2 == 20
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			operation_setting.GetGeneralSetting().QuotaDisplayType = tc.displayType
			setting.ZafuPayDailyLimit = tc.limit
			require.NoError(t, model.DB.Where("1 = 1").Delete(&model.TopUp{}).Error)
			for i := range tc.existing {
				require.NoError(t, model.DB.Create(&tc.existing[i]).Error)
			}

			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/user/zafu-pay", nil)

			rejected := checkZafuPayDailyLimit(ctx, userId, tc.amount)
			assert.Equal(t, tc.wantReject, rejected)
			if tc.wantReject {
				assert.JSONEq(t, fmt.Sprintf(`{"message":"error","data":%q}`, tc.wantData), recorder.Body.String())
			}
		})
	}
}

func TestSumZafuPayTopUpAmountSinceReturnsZeroWithoutOrders(t *testing.T) {
	oldDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}))
	model.DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})

	total, err := model.SumZafuPayTopUpAmountSince(42, zafuPayDayStartTs())
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
}
