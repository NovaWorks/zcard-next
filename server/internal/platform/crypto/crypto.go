// Package crypto 数据层加密基建（规划 §4.11.6 / §5.20.2）。
//
// 四把钥匙解耦（卡密密钥/业务数据密钥/JWT 密钥/APP 密钥），env 或 secret 注入，
// 永不进 DB/日志/git。本包只提供算法原语，密钥的装配在启动阶段完成。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// 密钥长度：AES-256。
const keyLen = 32

// ErrDecrypt 解密失败。调用方必须降级处理（凭据列降级为空并提示重配，铁律 5），
// 绝不向上抛 500。
var ErrDecrypt = errors.New("crypto: decrypt failed")

// Box AES-256-GCM 加密盒（卡密内容 / 凭据列 / TOTP 密钥共用）。
// AAD 绑定业务上下文（如 product_id/subsite_id），防密文挪用。
type Box struct {
	aead cipher.AEAD
}

// NewBox 由 32 字节原始密钥构造。密钥来源：ZCARD_CARD_KEY / ZCARD_DATA_KEY（hex）。
func NewBox(key []byte) (*Box, error) {
	if len(key) != keyLen {
		return nil, fmt.Errorf("crypto: key must be %d bytes, got %d", keyLen, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal 加密：输出 nonce || ciphertext+tag。aad 可为 nil。
// 每次加密使用随机 nonce，同一明文两次加密密文不同（不可作去重依据）。
func (b *Box) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("crypto: read nonce: %w", err)
	}
	return b.aead.Seal(nonce, nonce, plaintext, aad), nil
}

// Open 解密；失败返回 ErrDecrypt（调用方降级，不透出密文细节）。
func (b *Box) Open(ciphertext, aad []byte) ([]byte, error) {
	ns := b.aead.NonceSize()
	if len(ciphertext) < ns {
		return nil, ErrDecrypt
	}
	plain, err := b.aead.Open(nil, ciphertext[:ns], ciphertext[ns:], aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plain, nil
}

// KeyedHash keyed hash 去重（铁律 11）：HMAC-SHA256(key, plaintext)。
// 相比裸 sha256 防低熵卡密（短兑换码）的彩虹表反推。
// 注意：密钥轮换（zcard reencrypt-cards）时须与密文重加密同事务重算。
func KeyedHash(key []byte, plaintext []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(plaintext)
	return hex.EncodeToString(mac.Sum(nil))
}

// ParseHexKey 解析 hex 密钥（env 注入形态：64 个 hex 字符 = 32 字节）。
func ParseHexKey(s string) ([]byte, error) {
	key, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("crypto: key is not valid hex: %w", err)
	}
	if len(key) != keyLen {
		return nil, fmt.Errorf("crypto: key must be %d bytes (64 hex chars), got %d bytes", keyLen, len(key))
	}
	return key, nil
}
