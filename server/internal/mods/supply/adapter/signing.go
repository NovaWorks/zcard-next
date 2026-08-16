package adapter

// 三协议签名原语（golden vector 契约测试的固定目标，改动必须同步更新向量）。
//
// 核心不变式（1.x CLAUDE.md 教训）：签名哈希的字节 === 实际发出的字节。
// 所有签名函数接收「原始 body 字节」，md5 只在内部做一次，禁止调用方预哈希。

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"net/url"
	"strings"
)

// md5Hex 小写 hex md5（PHP md5() 口径）。
func md5Hex(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

// HmacSHA256Hex 小写 hex HMAC-SHA256（PHP hash_hmac 口径）。
func HmacSHA256Hex(secret, message string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

// ---- zcard（自家 Supply v2，4 头 HMAC）----

// ZCardSignString 组装签名串（v1.12.90+ 新口径，含 query md5 段）。
// 签名串 = METHOD\nPATH(不含query)\ntimestamp\nnonce\nmd5(body)\nmd5(rawQuery)
// 服务端（P2-03 authware）双口径兼容验签：先旧口径（无第 6 段）后新口径。
func ZCardSignString(method, path, rawQuery, timestamp, nonce string, body []byte) string {
	return strings.Join([]string{
		method,
		path,
		timestamp,
		nonce,
		md5Hex(body),
		md5Hex([]byte(rawQuery)),
	}, "\n")
}

// ZCardSign 计算 4 头签名（hex 小写）。
func ZCardSign(secret, method, path, rawQuery, timestamp, nonce string, body []byte) string {
	return HmacSHA256Hex(secret, ZCardSignString(method, path, rawQuery, timestamp, nonce, body))
}

// ---- dujiao_next（3 头 HMAC）----

// DujiaoSignString 签名串 = METHOD\nPATH(不含query)\ntimestamp\nmd5(body)
func DujiaoSignString(method, path, timestamp string, body []byte) string {
	return strings.Join([]string{method, path, timestamp, md5Hex(body)}, "\n")
}

// DujiaoSign 计算 3 头签名。
func DujiaoSign(secret, method, path, timestamp string, body []byte) string {
	return HmacSHA256Hex(secret, DujiaoSignString(method, path, timestamp, body))
}

// ---- acg_faka（body 内 MD5，对齐官方 Str::generateSignature）----

// AcgFakaSign 计算 body 内 sign：
// 参数（含 app_id/app_key，去 sign 与空值）ksort → http_build_query →
// urldecode → 末尾接 &key=app_key → md5。
func AcgFakaSign(params map[string]string, appKey string) string {
	// ksort（字典序，PHP 默认 SORT_REGULAR 对字符串按字典序；Go map 需显式排序）
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "sign" {
			continue
		}
		if params[k] == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	vals := make(url.Values, len(keys))
	for _, k := range keys {
		vals.Set(k, params[k])
	}
	// http_build_query → urldecode：编码后整体解码一次
	encoded := vals.Encode()
	decoded, err := url.QueryUnescape(encoded)
	if err != nil {
		decoded = encoded // 理论不可达（Encode 产物合法），防御性兜底
	}
	return md5Hex([]byte(decoded + "&key=" + appKey))
}
