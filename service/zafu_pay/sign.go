package zafu_pay

import (
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"sort"
	"strings"
)

// Sign 按平台规则计算 MD5 签名：非空参数（排除 sign 本身）按参数名 ASCII
// 字典序拼接为 key1=value1&key2=value2，末尾追加 &key=商户密钥，
// 对拼接结果计算 32 位 MD5 并输出小写十六进制。
func Sign(params map[string]string, merchantKey string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(params[k])
		b.WriteByte('&')
	}
	b.WriteString("key=")
	b.WriteString(merchantKey)

	sum := md5.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// Verify 校验报文中的 sign 字段。对传入的 sign 做 lowercase 归一后进行
// 常量时间比较，避免对端大小写差异导致误杀。
func Verify(params map[string]string, merchantKey string) bool {
	sign := params["sign"]
	if sign == "" {
		return false
	}
	expected := Sign(params, merchantKey)
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(sign)), []byte(expected)) == 1
}
