package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/zafu_pay"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type ZafuPayRequest struct {
	Amount        int64  `json:"amount"`
	PaymentMethod string `json:"payment_method"`
}

func getZafuPayMinTopup() int64 {
	minTopup := setting.ZafuPayMinTopUp
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		dMinTopup := decimal.NewFromInt(int64(minTopup))
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		minTopup = common.QuotaFromDecimal(dMinTopup.Mul(dQuotaPerUnit))
	}
	return int64(minTopup)
}

// RequestZafuPayAmount 计算一卡通支付应付金额（元），逻辑与易支付一致。
func RequestZafuPayAmount(c *gin.Context) {
	var req AmountRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}

	if req.Amount < getZafuPayMinTopup() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", getZafuPayMinTopup())})
		return
	}
	id := c.GetInt("id")
	if rejectInvalidTopUpQuota(c, id, req.Amount) {
		return
	}
	if checkZafuPayDailyLimit(c, id, req.Amount) {
		return
	}
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	payMoney := getPayMoney(req.Amount, group)
	if payMoney < 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低，最低支付金额为 0.01 元"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": strconv.FormatFloat(payMoney, 'f', 2, 64)})
}

func getZafuPayConfig() zafu_pay.Config {
	return zafu_pay.Config{
		Address:     setting.ZafuPayAddress,
		MerchantKey: setting.ZafuPayKey,
	}
}

// zafuPayAmountToBase 把用户输入的充值数量换算为落库/限额比对用的币种单位：
// tokens 展示类型下前端传的是 tokens，需除以 QuotaPerUnit 折算为币种数量；
// 其余类型（USD/CNY/CUSTOM）前端传的就是币种数量，原样返回。
func zafuPayAmountToBase(amount int64) int64 {
	if operation_setting.GetQuotaDisplayType() != operation_setting.QuotaDisplayTypeTokens {
		return amount
	}
	dAmount := decimal.NewFromInt(amount)
	dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
	return dAmount.Div(dQuotaPerUnit).IntPart()
}

// zafuPayBaseToDisplay 把币种单位数量换算为当前展示单位数量（zafuPayAmountToBase 的逆运算）。
func zafuPayBaseToDisplay(baseAmount int64) int64 {
	if operation_setting.GetQuotaDisplayType() != operation_setting.QuotaDisplayTypeTokens {
		return baseAmount
	}
	dBase := decimal.NewFromInt(baseAmount)
	dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
	return dBase.Mul(dQuotaPerUnit).IntPart()
}

// zafuPayDayStartTs 返回服务器本地时区当天 0 点的秒级 Unix 时间戳，
// 作为一卡通单日充值限额的自然日窗口起点。
func zafuPayDayStartTs() int64 {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
}

// checkZafuPayDailyLimit 在用户发起一卡通充值前检查单日（自然日）充值限额。
// 限额未配置（<=0）时放行；否则「当日已成功充值 + 本次」超过上限即拒绝。
// 配置值以展示单位存储（与 ZafuPayMinTopUp 同单位），比对前经 zafuPayAmountToBase
// 折算为币种单位，与落库的 amount（币种单位）及本次充值保持同一单位。
func checkZafuPayDailyLimit(c *gin.Context, userId int, amount int64) bool {
	if setting.ZafuPayDailyLimit <= 0 {
		return false
	}
	limitBase := zafuPayAmountToBase(int64(setting.ZafuPayDailyLimit))
	usedBase, err := model.SumZafuPayTopUpAmountSince(userId, zafuPayDayStartTs())
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("一卡通支付 查询当日充值累计失败 user_id=%d error=%q", userId, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值限额校验失败，请稍后重试"})
		return true
	}
	if usedBase+zafuPayAmountToBase(amount) > limitBase {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("今日一卡通充值已达上限 %d，请明日再试", int64(setting.ZafuPayDailyLimit))})
		return true
	}
	return false
}

// RequestZafuPay 创建校园一卡通支付订单并同步扣款。
// 一卡通消费接口（/pay/order/unifiedOrder）同步返回扣款结果：
// 成功则立即入账；识别号固定按个人编号（id_type=4）上送，取当前登录用户的
// username，无需用户手工填写卡号。
func RequestZafuPay(c *gin.Context) {
	var req ZafuPayRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	if req.PaymentMethod != model.PaymentMethodZafuPay {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "不支持的支付渠道"})
		return
	}
	if req.Amount < getZafuPayMinTopup() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", getZafuPayMinTopup())})
		return
	}
	if !isZafuPayWebhookConfigured() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "当前管理员未配置支付信息"})
		return
	}

	id := c.GetInt("id")
	if rejectInvalidTopUpQuota(c, id, req.Amount) {
		return
	}
	if checkZafuPayDailyLimit(c, id, req.Amount) {
		return
	}

	user, err := model.GetUserById(id, false)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户信息失败"})
		return
	}
	idNo := strings.TrimSpace(user.Username)
	if idNo == "" {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "当前账号缺少用户名，无法使用一卡通支付"})
		return
	}

	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	payMoney := getPayMoney(req.Amount, group)
	if payMoney < 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低，最低支付金额为 0.01 元"})
		return
	}

	// 平台金额单位为分，按两位小数元金额四舍五入转换
	totalAmountFen := decimal.NewFromFloat(payMoney).Shift(2).Round(0).IntPart()

	// 平台要求商户订单号仅含字母、数字、中划线、下划线等半角字符
	tradeNo := fmt.Sprintf("%s%d", common.GetRandomString(6), time.Now().Unix())
	tradeNo = fmt.Sprintf("USR%dNO%s", id, tradeNo)

	amount := zafuPayAmountToBase(req.Amount)
	topUp := &model.TopUp{
		UserId:          id,
		Amount:          amount,
		Money:           payMoney,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodZafuPay,
		PaymentProvider: model.PaymentProviderZafuPay,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	// 先落库再扣款：扣款请求发出后任何环节失败，都有本地订单可对账
	if err = topUp.Insert(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("一卡通支付 创建充值订单失败 user_id=%d trade_no=%s amount=%d error=%q", id, tradeNo, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	result, err := zafu_pay.UnifiedOrder(c.Request.Context(), getZafuPayConfig(), zafu_pay.UnifiedOrderRequest{
		SceneID:        setting.ZafuPayMyAppId,
		OutTradeNo:     tradeNo,
		IdNo:           idNo,
		TotalAmountFen: totalAmountFen,
	})
	if err != nil {
		handleZafuPayFailure(c, topUp, err)
		return
	}

	completeZafuPayOrder(c, topUp, result)
}

// completeZafuPayOrder 在扣款确认成功后入账并应答用户。
func completeZafuPayOrder(c *gin.Context, topUp *model.TopUp, result *zafu_pay.UnifiedOrderResult) {
	alreadyDone, err := model.RechargeZafuPay(topUp.TradeNo, zafu_pay.PaymentTypeZycard, c.ClientIP())
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("一卡通支付 扣款成功但入账失败 trade_no=%s error=%q", topUp.TradeNo, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付成功但入账失败，请联系管理员"})
		return
	}
	if alreadyDone {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("一卡通支付 订单重复入账幂等忽略 trade_no=%s", topUp.TradeNo))
	} else {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("一卡通支付 充值成功 trade_no=%s transaction_id=%s", topUp.TradeNo, result.TransactionID))
	}
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": gin.H{"transaction_id": result.TransactionID}})
}

// handleZafuPayFailure 处理扣款未确认成功的情况。平台明确返回业务错误时报告失败；
// 网络超时或平台返回支付超时类错误码时订单真实结果未知——当前允许的接口文档中
// 没有订单查询接口，无法查单确认，订单保留 pending 待对账，并提示用户切勿重复支付。
func handleZafuPayFailure(c *gin.Context, topUp *model.TopUp, payErr error) {
	tradeNo := topUp.TradeNo

	var platformErr *zafu_pay.PlatformError
	uncertain := !errors.As(payErr, &platformErr) || zafu_pay.IsPayTimeoutStatus(platformErr.Status)
	if !uncertain {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("一卡通支付 扣款失败 trade_no=%s error=%q", tradeNo, payErr.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付失败，请稍后重试"})
		return
	}

	logger.LogError(c.Request.Context(), fmt.Sprintf("一卡通支付 扣款结果未知，无查单接口可用，订单保留待对账 trade_no=%s error=%q", tradeNo, payErr.Error()))
	c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付结果确认中，请稍后查看充值记录，切勿重复支付"})
}

// zafuPayNotifyResponse 按平台规范应答支付结果通知，
// 成功必须返回 {"status":200,"msg":"SUCCESS"}。
func zafuPayNotifyResponse(c *gin.Context, status int, msg string) {
	c.JSON(http.StatusOK, gin.H{"status": status, "msg": msg})
}

// normalizeNotifyParams 将回调 JSON 解析为 string 键值对，兼容平台把数字
// 字段以字符串或 JSON 数字两种形式返回的情况。
func normalizeNotifyParams(raw map[string]interface{}) map[string]string {
	params := make(map[string]string, len(raw))
	for k, v := range raw {
		switch value := v.(type) {
		case string:
			params[k] = value
		case float64:
			params[k] = strconv.FormatFloat(value, 'f', -1, 64)
		case bool:
			params[k] = strconv.FormatBool(value)
		case nil:
			// 空值不参与签名
		default:
			params[k] = fmt.Sprintf("%v", value)
		}
	}
	return params
}

// ZafuPayNotify 接收一卡通支付平台的支付结果通知：验签、金额一致性校验（防假通知）、
// 幂等入账后按规范应答。同步扣款已入账的订单回调时命中幂等分支。
func ZafuPayNotify(c *gin.Context) {
	if !isZafuPayWebhookEnabled() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("一卡通支付 webhook 被拒绝 reason=webhook_disabled path=%q client_ip=%s", c.Request.RequestURI, c.ClientIP()))
		zafuPayNotifyResponse(c, 500, "webhook disabled")
		return
	}

	var raw map[string]interface{}
	if err := common.DecodeJson(c.Request.Body, &raw); err != nil || len(raw) == 0 {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("一卡通支付 webhook 参数解析失败 path=%q client_ip=%s", c.Request.RequestURI, c.ClientIP()))
		zafuPayNotifyResponse(c, 500, "invalid params")
		return
	}
	params := normalizeNotifyParams(raw)
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("一卡通支付 webhook 收到请求 path=%q client_ip=%s params=%q", c.Request.RequestURI, c.ClientIP(), common.GetJsonString(params)))

	if !zafu_pay.Verify(params, setting.ZafuPayKey) {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("一卡通支付 webhook 验签失败 path=%q client_ip=%s", c.Request.RequestURI, c.ClientIP()))
		zafuPayNotifyResponse(c, 500, "invalid sign")
		return
	}

	tradeNo := params["out_trade_no"]
	if tradeNo == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("一卡通支付 webhook 缺少订单号 client_ip=%s", c.ClientIP()))
		zafuPayNotifyResponse(c, 500, "missing out_trade_no")
		return
	}

	topUp := model.GetTopUpByTradeNo(tradeNo)
	if topUp == nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("一卡通支付 回调订单不存在 trade_no=%s client_ip=%s", tradeNo, c.ClientIP()))
		zafuPayNotifyResponse(c, 500, "order not found")
		return
	}

	// 校验回调金额与本地订单金额一致（单位均为分），防止“假通知”造成资金损失
	expectedFen := decimal.NewFromFloat(topUp.Money).Shift(2).Round(0).IntPart()
	orderAmountFen, err := strconv.ParseInt(params["order_amount"], 10, 64)
	if err != nil || orderAmountFen != expectedFen {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("一卡通支付 webhook 金额不一致 trade_no=%s expected_fen=%d got=%q client_ip=%s", tradeNo, expectedFen, params["order_amount"], c.ClientIP()))
		zafuPayNotifyResponse(c, 500, "amount mismatch")
		return
	}

	// 进程内锁只是优化；重复/并发回调的正确性由 RechargeZafuPay 的
	// 数据库行锁 + 事务内状态校验保证（多实例部署下同样安全）。
	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)
	alreadyDone, err := model.RechargeZafuPay(tradeNo, params["payment_type"], c.ClientIP())
	if err != nil {
		switch {
		case errors.Is(err, model.ErrTopUpNotFound):
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("一卡通支付 回调订单不存在 trade_no=%s client_ip=%s", tradeNo, c.ClientIP()))
		case errors.Is(err, model.ErrPaymentMethodMismatch):
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("一卡通支付 订单支付网关不匹配 trade_no=%s client_ip=%s", tradeNo, c.ClientIP()))
		case errors.Is(err, model.ErrTopUpStatusInvalid):
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("一卡通支付 订单状态非法 trade_no=%s client_ip=%s", tradeNo, c.ClientIP()))
		default:
			logger.LogError(c.Request.Context(), fmt.Sprintf("一卡通支付 充值处理失败 trade_no=%s client_ip=%s error=%q", tradeNo, c.ClientIP(), err.Error()))
		}
		zafuPayNotifyResponse(c, 500, "process failed")
		return
	}
	if alreadyDone {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("一卡通支付 重复回调幂等忽略 trade_no=%s client_ip=%s", tradeNo, c.ClientIP()))
	} else {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("一卡通支付 充值成功 trade_no=%s client_ip=%s", tradeNo, c.ClientIP()))
	}
	zafuPayNotifyResponse(c, zafu_pay.StatusSuccess, "SUCCESS")
}
