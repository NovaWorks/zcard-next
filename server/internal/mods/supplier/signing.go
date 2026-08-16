package supplier

// 签名原语（对外供货协议，§5.8 口径钉死）：
//   签名串 = METHOD\nPATH(不含query)\ntimestamp\nnonce\nmd5(body)      —— 旧口径
//   签名串 = ...\nmd5(body)\nmd5(rawQuery)                              —— 新口径（v1.12.90+）
// 服务端双口径验签（先旧后新）；客户端一律新口径。
// hex_lower(HMAC_SHA256(api_secret, 签名串))；常数时间比较（hash_equals 语义）。

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
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
