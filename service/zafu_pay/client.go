package zafu_pay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	// DefaultGatewayAddress 正式服支付平台 API 域名
	DefaultGatewayAddress = "https://payment.zafu.edu.cn"
	unifiedOrderPath      = "/pay/order/unifiedOrder"

	// StatusSuccess 平台统一返回封装中的成功状态码
	StatusSuccess = 200

	// PaymentTypeZycard 一卡通支付方式固定值
	PaymentTypeZycard = "zycard"

	// IdTypePersonalNo 识别号类别：4-个人编号。
	// 接口支持 1帐号/2卡号/3卡内编号/4个人编号/5第三方对接号/6证件号，
	// 本项目固定使用个人编号（即系统用户名）。
	IdTypePersonalNo = "4"
)

// 平台返回的错误码：支付超时类，订单真实结果未知
const (
	statusPayTimeoutQuery     = "66600013" // 支付超时，请调用订单查询接口确认订单状态
	statusPayTimeoutRevoked   = "66600015" // 支付超时，订单已撤销
	statusPayTimeoutRevokeErr = "66600016" // 支付超时，订单撤销异常
)

// Config 一次调用所需的网关配置，由控制层从全局设置读取后传入。
type Config struct {
	Address     string // 网关地址，为空时使用 DefaultGatewayAddress
	MerchantKey string // 商户密钥（签名 key）
}

// UnifiedOrderRequest 一卡通消费（/pay/order/unifiedOrder）参数。
type UnifiedOrderRequest struct {
	SceneID        string // myapp_id：支付平台分配的场景id
	OutTradeNo     string // out_trade_no：商户订单号
	IdNo           string // id_no：对接点识别号（固定按个人编号上送）
	TotalAmountFen int64  // total_amount：订单金额，单位分
	Subject        string // subject：订单标题，可选，为空时平台默认显示支付场景名称
}

// UnifiedOrderResult 一卡通消费接口业务数据。
type UnifiedOrderResult struct {
	TransactionID string // 正元一卡通返回的流水号
	DealerNum     string // 一卡通商户编号
	OptNum        string // 操作员编号
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

const (
	// maxResponseBodyBytes 平台响应体读取上限，防止异常响应撑爆内存
	maxResponseBodyBytes = 1 << 20
	// responseSnippetLen 解析失败时记入错误信息的响应报文片段长度（字符数）
	responseSnippetLen = 200
)

// UnifiedOrder 调用一卡通消费接口，从校园卡账户同步扣款。
// 成功返回表示平台已确认扣款；网络超时或平台返回支付超时类错误码时
// 订单真实结果未知，调用方不得直接向用户报告失败。
func UnifiedOrder(ctx context.Context, cfg Config, req UnifiedOrderRequest) (*UnifiedOrderResult, error) {
	if cfg.MerchantKey == "" {
		return nil, fmt.Errorf("商户密钥未配置")
	}
	if req.SceneID == "" {
		return nil, fmt.Errorf("场景id不能为空")
	}
	if req.OutTradeNo == "" {
		return nil, fmt.Errorf("商户订单号不能为空")
	}
	if req.IdNo == "" {
		return nil, fmt.Errorf("识别号不能为空")
	}
	if req.TotalAmountFen <= 0 {
		return nil, fmt.Errorf("订单金额必须大于 0")
	}

	params := map[string]string{
		"myapp_id":     req.SceneID,
		"out_trade_no": req.OutTradeNo,
		"total_amount": strconv.FormatInt(req.TotalAmountFen, 10),
		"payment_type": PaymentTypeZycard,
		"id_type":      IdTypePersonalNo,
		"id_no":        req.IdNo,
		"nonce_str":    common.GetRandomString(16),
		"time_stamp":   strconv.FormatInt(time.Now().UnixMilli(), 10),
	}
	if req.Subject != "" {
		params["subject"] = req.Subject
	}
	params["sign"] = Sign(params, cfg.MerchantKey)

	body, err := common.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	address := strings.TrimRight(cfg.Address, "/")
	if address == "" {
		address = DefaultGatewayAddress
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, address+unifiedOrderPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("构建请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("请求支付平台失败: %w", err)
	}
	defer resp.Body.Close()

	// 网关前置（WAF/反向代理/IP 白名单拦截页）可能返回 HTML 错误页而非
	// JSON。先读原始报文，解析失败时在错误中保留 HTTP 状态码与报文片段，
	// 便于定位是平台故障还是链路被拦截。
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("读取支付平台响应失败: %w", err)
	}

	var envelope struct {
		Status json.RawMessage `json:"status"`
		Msg    string          `json:"msg"`
		Data   json.RawMessage `json:"data"`
	}
	if err := common.Unmarshal(respBody, &envelope); err != nil {
		runes := []rune(string(respBody))
		if len(runes) > responseSnippetLen {
			runes = runes[:responseSnippetLen]
		}
		return nil, fmt.Errorf("解析支付平台响应失败: http_status=%d body=%q: %w", resp.StatusCode, strings.TrimSpace(string(runes)), err)
	}

	status, err := parseStatus(envelope.Status)
	if err != nil {
		return nil, fmt.Errorf("支付平台响应状态码格式异常: %q", string(envelope.Status))
	}
	if status != StatusSuccess {
		return nil, &PlatformError{Status: status, Msg: envelope.Msg}
	}

	return parseUnifiedOrderData(envelope.Data)
}

// IsPayTimeoutStatus 判断平台错误码是否为“支付超时、结果未知”类。
// 此类状态订单真实结果未知，不能直接向用户报告失败。
func IsPayTimeoutStatus(status int) bool {
	s := strconv.Itoa(status)
	return s == statusPayTimeoutQuery || s == statusPayTimeoutRevoked || s == statusPayTimeoutRevokeErr
}

// PlatformError 平台业务错误，保留状态码以便调用方区分
// “明确失败”与“支付超时结果未知”两类情况。
type PlatformError struct {
	Status int
	Msg    string
}

func (e *PlatformError) Error() string {
	return fmt.Sprintf("支付平台返回错误 status=%d msg=%s", e.Status, e.Msg)
}

// parseStatus 兼容平台返回封装中 status 既可能是数字也可能是字符串的情况。
func parseStatus(raw json.RawMessage) (int, error) {
	return strconv.Atoi(strings.Trim(string(raw), `"`))
}

func parseUnifiedOrderData(raw json.RawMessage) (*UnifiedOrderResult, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("支付平台响应缺少 data 字段")
	}
	var data map[string]interface{}
	if err := common.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("解析响应数据失败: %w", err)
	}

	return &UnifiedOrderResult{
		TransactionID: stringField(data, "transaction_id"),
		DealerNum:     stringField(data, "dealer_num"),
		OptNum:        stringField(data, "opt_num"),
	}, nil
}

func stringField(data map[string]interface{}, key string) string {
	if v, ok := data[key].(string); ok {
		return v
	}
	return ""
}
