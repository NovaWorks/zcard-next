// Package laracrypt ZCard 1.x（Laravel）加密载荷的解密原语（migrate-from-v1 专用）。
//
// 1.x 两类密文（与《数据迁移工具开发计划》附录 B 对齐）：
//   - Laravel Crypt（APP_KEY）：supply_sources.credentials / supplier_accounts.api_secret
//     / payment_channels.config（双层）/ settings SECRET_KEYS（双层）
//   - CardCipher（独立 CARD_ENCRYPTION_KEY）：cards.content，开关可关 → 存在历史明文，
//     必须经 LooksEncrypted 形态识别后直通
//
// 载荷格式（Illuminate\Encryption\Encrypter::encryptString）：
//
//	base64( json{"iv": b64(iv16), "value": b64(AES-256-CBC 密文), "mac": hex} )
//	mac = HMAC-SHA256(key, b64(iv)字符串 + b64(value)字符串)   ← 参与运算的是 base64 文本
//
// 正确性由 testdata/v1_crypto_fixtures.json（真实 Laravel 加密器生成的 golden vectors）
// 钉死；密钥形态由 ParseKey 兼容（base64: 前缀 / 64 hex / 32 字节原文）。
package laracrypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// 哨兵错误：调用方按 errors.Is 分流（密钥错配告警 / 明文直通 / 通用失败）。
var (
	// ErrNotEncrypted 载荷不是 Laravel 加密形态（历史明文应直通，不应视为错误）。
	ErrNotEncrypted = errors.New("laracrypt: 非加密载荷形态")
	// ErrBadMac MAC 校验失败——最典型的成因是密钥不对（错配的 key 解不出可读明文，
	// 必须拒收而非静默产出乱码）。
	ErrBadMac = errors.New("laracrypt: MAC 校验失败（疑似密钥错配或载荷损坏）")
	// ErrDecrypt 解密失败（结构/填充/序列化异常）。
	ErrDecrypt = errors.New("laracrypt: 解密失败")
)

const keyLen = 32 // AES-256

type cryptPayload struct {
	IV    string `json:"iv"`
	Value string `json:"value"`
	Mac   string `json:"mac"`
}

// ParseKey 解析 1.x 密钥串：`base64:` 前缀（APP_KEY 约定）→ base64 解码；
// 64 位 hex → hex 解码；32 字节原文直用。三者都要求恰好 32 字节。
func ParseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("laracrypt: 密钥为空")
	}
	if rest, ok := strings.CutPrefix(s, "base64:"); ok {
		key, err := base64.StdEncoding.DecodeString(rest)
		if err != nil {
			return nil, fmt.Errorf("laracrypt: base64 密钥解码失败: %w", err)
		}
		if len(key) != keyLen {
			return nil, fmt.Errorf("laracrypt: 密钥须 %d 字节，base64 解码后为 %d 字节", keyLen, len(key))
		}
		return key, nil
	}
	if len(s) == 64 {
		if key, err := hex.DecodeString(s); err == nil {
			return key, nil
		}
	}
	if len(s) == keyLen {
		return []byte(s), nil
	}
	return nil, errors.New("laracrypt: 密钥形态不识别（期望 base64: 前缀 / 64 位 hex / 32 字节原文）")
}

// LooksEncrypted 形态识别，与 1.x CardCipher::looksEncrypted 同口径：
// base64 解出 JSON 对象且 iv/value/mac 三键齐备。
func LooksEncrypted(v string) bool {
	if v == "" {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(raw) == 0 {
		return false
	}
	var p cryptPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return false
	}
	return p.IV != "" && p.Value != "" && p.Mac != ""
}

// Crypt Laravel Crypt 解密器（APP_KEY 或 CARD_ENCRYPTION_KEY 均可用本类型）。
type Crypt struct {
	key []byte
}

// New 构造（key 必须 32 字节）。
func New(key []byte) (*Crypt, error) {
	if len(key) != keyLen {
		return nil, fmt.Errorf("laracrypt: AES-256 密钥须 %d 字节，实际 %d", keyLen, len(key))
	}
	return &Crypt{key: key}, nil
}

// OpenString 解密 Crypt::encryptString 产物（含 MAC 校验 + PKCS7 + maybe-unserialize）。
func (c *Crypt) OpenString(payload string) (string, error) {
	b, err := c.open(payload, true)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// OpenStringSkipMAC 免 MAC 校验解密（仅限 --skip-mac 抢救场景；正常路径必须校验）。
func (c *Crypt) OpenStringSkipMAC(payload string) (string, error) {
	b, err := c.open(payload, false)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (c *Crypt) open(payload string, verifyMac bool) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: 载荷 base64 解码失败: %v", ErrDecrypt, err)
	}
	var p cryptPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.IV == "" || p.Value == "" {
		return nil, fmt.Errorf("%w: 载荷 JSON 结构异常", ErrNotEncrypted)
	}
	iv, err := base64.StdEncoding.DecodeString(p.IV)
	if err != nil || len(iv) != aes.BlockSize {
		return nil, fmt.Errorf("%w: iv 非法", ErrDecrypt)
	}
	value, err := base64.StdEncoding.DecodeString(p.Value)
	if err != nil {
		return nil, fmt.Errorf("%w: 密文 base64 解码失败", ErrDecrypt)
	}
	if verifyMac {
		mac := hmac.New(sha256.New, c.key)
		mac.Write([]byte(p.IV))
		mac.Write([]byte(p.Value))
		want := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(strings.ToLower(p.Mac)), []byte(want)) {
			return nil, ErrBadMac
		}
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, err
	}
	if len(value) == 0 || len(value)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("%w: 密文长度非块对齐", ErrDecrypt)
	}
	dst := make([]byte, len(value))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(dst, value)
	out, err := pkcs7Unpad(dst)
	if err != nil {
		return nil, err
	}
	return maybeUnserialize(out)
}

func pkcs7Unpad(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, fmt.Errorf("%w: 空明文", ErrDecrypt)
	}
	n := int(b[len(b)-1])
	if n == 0 || n > aes.BlockSize || n > len(b) {
		return nil, fmt.Errorf("%w: PKCS7 填充非法", ErrDecrypt)
	}
	for _, v := range b[len(b)-n:] {
		if int(v) != n {
			return nil, fmt.Errorf("%w: PKCS7 填充非法", ErrDecrypt)
		}
	}
	return b[:len(b)-n], nil
}

// maybeUnserialize 兼容 Encrypter::encrypt($v, serialize=true) 的载荷：
// `s:LEN:"...";` → 原文；其他 serialize 类型（i/b/d/a/O…）在 1.x 数据中不存在，
// 出现即按损坏数据处理；非 serialize 形态原样返回。
func maybeUnserialize(b []byte) ([]byte, error) {
	if len(b) < 2 || b[1] != ':' {
		return b, nil
	}
	switch b[0] {
	case 's':
		rest := b[2:]
		colon := bytes.IndexByte(rest, ':')
		if colon <= 0 {
			return nil, fmt.Errorf("%w: serialize s: 结构异常", ErrDecrypt)
		}
		n, err := strconv.Atoi(string(rest[:colon]))
		if err != nil || n < 0 {
			return nil, fmt.Errorf("%w: serialize 长度非法", ErrDecrypt)
		}
		body := rest[colon+1:] // 形如 `"数据";`，开引号属于定界符而非数据
		if len(body) < n+3 || body[0] != '"' || body[n+1] != '"' || body[n+2] != ';' {
			return nil, fmt.Errorf("%w: serialize 字符串体截断", ErrDecrypt)
		}
		return body[1 : n+1], nil
	case 'i', 'b', 'd', 'a', 'O':
		return nil, fmt.Errorf("%w: 不支持的 serialize 类型 %q（1.x 数据不应出现）", ErrDecrypt, b[0])
	default:
		return b, nil
	}
}

// OpenCard 解密 cards.content（1.x CardCipher::decrypt 等价物，错误策略由调用方决定）。
// 明文直通：wasEncrypted=false 且原样返回（加密开关关闭期的存量明文卡）。
func OpenCard(content string, cardKey []byte, verifyMac bool) (plain string, wasEncrypted bool, err error) {
	if !LooksEncrypted(content) {
		return content, false, nil
	}
	c, err := New(cardKey)
	if err != nil {
		return "", true, err
	}
	if verifyMac {
		plain, err = c.OpenString(content)
	} else {
		plain, err = c.OpenStringSkipMAC(content)
	}
	if err != nil {
		return "", true, err
	}
	return plain, true, nil
}

// OpenPaymentConfig 解密 payment_channels.config 列（双层）：
// 列值是 JSON 字符串（带引号），内层才是 Crypt 密文；历史明文配置（整列即明文 JSON）
// 原样返回 wasEncrypted=false。
func OpenPaymentConfig(col string, appKey []byte, verifyMac bool) (plain string, wasEncrypted bool, err error) {
	var inner string
	if err := json.Unmarshal([]byte(col), &inner); err != nil {
		return col, false, nil // 非 JSON 字符串 → 历史明文配置
	}
	if !LooksEncrypted(inner) {
		return col, false, nil
	}
	c, err := New(appKey)
	if err != nil {
		return "", true, err
	}
	if verifyMac {
		plain, err = c.OpenString(inner)
	} else {
		plain, err = c.OpenStringSkipMAC(inner)
	}
	if err != nil {
		return "", true, err
	}
	return plain, true, nil
}

// OpenSettingSecret 解密 settings 表 SECRET_KEYS 的 value（JSON 编码字符串）：
// 内层 Crypt 密文 → 解密返回；历史明文 → 解引号后原样返回；非字符串 JSON → 原样返回。
func OpenSettingSecret(raw string, appKey []byte, verifyMac bool) (plain string, wasEncrypted bool, err error) {
	var inner string
	if err := json.Unmarshal([]byte(raw), &inner); err != nil {
		return raw, false, nil
	}
	if !LooksEncrypted(inner) {
		return inner, false, nil
	}
	c, err := New(appKey)
	if err != nil {
		return "", true, err
	}
	if verifyMac {
		plain, err = c.OpenString(inner)
	} else {
		plain, err = c.OpenStringSkipMAC(inner)
	}
	if err != nil {
		return "", true, err
	}
	return plain, true, nil
}

// CardKeyFromSetting 从 settings.card_encryption_key 的 value 解析卡密钥匙
// （对应 1.x CardCipher::resolveKey：值本身是 Crypt 密文 → 用 APP_KEY 解出钥匙串；
// 解密失败降级视为历史明文钥匙串）。fromCipher 表示值确实是密文形态。
func CardKeyFromSetting(raw string, appKey []byte, verifyMac bool) (key []byte, fromCipher bool, err error) {
	val, wasEnc, err := OpenSettingSecret(raw, appKey, verifyMac)
	if err != nil {
		return nil, false, err
	}
	key, kerr := ParseKey(val)
	if kerr != nil {
		return nil, wasEnc, fmt.Errorf("settings 中的卡密钥匙无法解析: %w", kerr)
	}
	return key, wasEnc, nil
}
