package supplier

// 签名原语（对外供货协议， 口径钉死）：
// 签名串 = METHOD\nPATH(不含query)\ntimestamp\nnonce\nmd5(body) —— 旧口径
// 签名串 = ...\nmd5(body)\nmd5(rawQuery) —— 新口径（v1.12.90+）
// 服务端双口径验签（先旧后新）；客户端一律新口径。
// hex_lower(HMAC_SHA256(api_secret, 签名串))；常数时间比较（hash_equals 语义）。

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

func md5HexBytes(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

func hmacSha256Hex(secret, message string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

// signStringOld 旧口径签名串。
func signStringOld(method, path, timestamp, nonce string, body []byte) string {
	return strings.Join([]string{method, path, timestamp, nonce, md5HexBytes(body)}, "\n")
}

// signStringNew 新口径签名串（含 query md5 段）。
func signStringNew(method, path, rawQuery, timestamp, nonce string, body []byte) string {
	return strings.Join([]string{method, path, timestamp, nonce, md5HexBytes(body), md5HexBytes([]byte(rawQuery))}, "\n")
}

// supplySign 新口径签名（服务端/回调转发共用）。
func supplySign(secret, method, path, rawQuery, timestamp, nonce string, body []byte) string {
	return hmacSha256Hex(secret, signStringNew(method, path, rawQuery, timestamp, nonce, body))
}

// verifyDual 双口径验签（常数时间；先旧后新——1.x 兼容语义）。
func verifyDual(secret, method, path, rawQuery, timestamp, nonce string, body []byte, signature string) bool {
	old := hmacSha256Hex(secret, signStringOld(method, path, timestamp, nonce, body))
	new := hmacSha256Hex(secret, signStringNew(method, path, rawQuery, timestamp, nonce, body))
	return subtle.ConstantTimeCompare([]byte(old), []byte(signature)) == 1 ||
		subtle.ConstantTimeCompare([]byte(new), []byte(signature)) == 1
}

// ── B/C 兼容层签名原语（golden 向量见 data_compat_test.go）──

// dujiaoSign dujiao-next 3 头协议签名：
// hex_lower(HMAC_SHA256(secret, "METHOD\nPATH(不含query)\nts\nmd5(body)"))。
func dujiaoSign(secret, method, path, timestamp string, body []byte) string {
	msg := strings.Join([]string{method, path, timestamp, md5HexBytes(body)}, "\n")
	return hmacSha256Hex(secret, msg)
}

// acgFakaSignVerify acg-faka body MD5 验签：
// md5(urldecode(http_build_query(ksort 升序、剔除空字符串值、除 sign 外全部参数)) + "&key=" + appKey)。
// 与 PHP ksort/http_build_query 语义对齐（值经 rawurldecode 后拼接）。
func acgFakaSignVerify(params map[string]string, appKey, sign string) bool {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "sign" || params[k] == "" {
			continue // sign 与空字符串值不参与（0/"0" 保留）
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+params[k])
	}
	str := strings.Join(pairs, "&") + "&key=" + appKey
	return subtle.ConstantTimeCompare([]byte(fmt.Sprintf("%x", md5.Sum([]byte(str)))), []byte(sign)) == 1
}
