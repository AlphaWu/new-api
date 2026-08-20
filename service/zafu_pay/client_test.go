package zafu_pay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testConfig(addr string) Config {
	return Config{Address: addr, MerchantKey: "test-key"}
}

func testOrderRequest() UnifiedOrderRequest {
	return UnifiedOrderRequest{
		SceneID:        "scene",
		OutTradeNo:     "T123",
		IdNo:           "tester",
		TotalAmountFen: 100,
	}
}

// 网关前置（WAF/白名单拦截页/错误页）返回 HTML 时，错误信息必须携带
// HTTP 状态码与报文片段，否则线上无法区分平台故障与链路被拦截。
func TestUnifiedOrderHTMLResponseKeepsEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, unifiedOrderPath, r.URL.Path)
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<html><body>Access Denied</body></html>"))
	}))
	defer server.Close()

	_, err := UnifiedOrder(context.Background(), testConfig(server.URL), testOrderRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http_status=403")
	assert.Contains(t, err.Error(), "Access Denied")
}

func TestUnifiedOrderSuccessEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":200,"msg":"SUCCESS","data":{"transaction_id":"TX9","dealer_num":"1234","opt_num":"5678"}}`))
	}))
	defer server.Close()

	result, err := UnifiedOrder(context.Background(), testConfig(server.URL), testOrderRequest())
	require.NoError(t, err)
	assert.Equal(t, "TX9", result.TransactionID)
	assert.Equal(t, "1234", result.DealerNum)
	assert.Equal(t, "5678", result.OptNum)
}

// 请求报文必须包含接口文档要求的全部必传参数（含固定 id_type=4 与
// payment_type=zycard），且签名可由商户密钥复算校验。
func TestUnifiedOrderRequestParams(t *testing.T) {
	var captured map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":200,"msg":"SUCCESS","data":{"transaction_id":"TX9"}}`))
	}))
	defer server.Close()

	_, err := UnifiedOrder(context.Background(), testConfig(server.URL), testOrderRequest())
	require.NoError(t, err)

	for _, field := range []string{"myapp_id", "out_trade_no", "total_amount", "payment_type", "id_type", "id_no", "nonce_str", "time_stamp", "sign"} {
		assert.Contains(t, captured, field)
	}
	assert.Equal(t, "scene", captured["myapp_id"])
	assert.Equal(t, "T123", captured["out_trade_no"])
	assert.Equal(t, "100", captured["total_amount"])
	assert.Equal(t, PaymentTypeZycard, captured["payment_type"])
	assert.Equal(t, IdTypePersonalNo, captured["id_type"])
	assert.Equal(t, "tester", captured["id_no"])
	assert.NotContains(t, captured, "subject")

	params := make(map[string]string, len(captured))
	for k, v := range captured {
		if s, ok := v.(string); ok {
			params[k] = s
		}
	}
	assert.True(t, Verify(params, "test-key"), "请求签名应可通过商户密钥验签")
}

func TestUnifiedOrderPlatformError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":66600004,"msg":"账户不存在或未启用"}`))
	}))
	defer server.Close()

	_, err := UnifiedOrder(context.Background(), testConfig(server.URL), testOrderRequest())
	require.Error(t, err)
	var platformErr *PlatformError
	require.ErrorAs(t, err, &platformErr)
	assert.Equal(t, 66600004, platformErr.Status)
	assert.False(t, IsPayTimeoutStatus(platformErr.Status))
}

func TestIsPayTimeoutStatus(t *testing.T) {
	for _, status := range []int{66600013, 66600015, 66600016} {
		assert.True(t, IsPayTimeoutStatus(status))
	}
	assert.False(t, IsPayTimeoutStatus(200))
}

func TestUnifiedOrderValidation(t *testing.T) {
	cfg := testConfig("http://127.0.0.1:1")

	_, err := UnifiedOrder(context.Background(), Config{Address: cfg.Address}, testOrderRequest())
	require.Error(t, err, "缺少商户密钥应报错")

	req := testOrderRequest()
	req.OutTradeNo = ""
	_, err = UnifiedOrder(context.Background(), cfg, req)
	require.Error(t, err, "缺少商户订单号应报错")

	req = testOrderRequest()
	req.IdNo = ""
	_, err = UnifiedOrder(context.Background(), cfg, req)
	require.Error(t, err, "缺少识别号应报错")

	req = testOrderRequest()
	req.TotalAmountFen = 0
	_, err = UnifiedOrder(context.Background(), cfg, req)
	require.Error(t, err, "金额必须大于 0")
}
