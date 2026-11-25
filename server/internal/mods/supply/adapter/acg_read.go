package adapter

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Normalize only explicit read failures. Purchase result classification stays
// on its existing path, where an uncertain response must never be replayed.
func parseAcgReadResp(raw []byte) (json.RawMessage, error) {
	data, err := parseResp(raw)
	if err == nil {
		return data, nil
	}
	var reply acgResp
	if json.Unmarshal(raw, &reply) != nil || reply.Code == 401 || reply.Code == 403 {
		return nil, err
	}
	message := strings.TrimSpace(reply.Msg)
	if reply.Code == 429 || strings.Contains(message, "服务器频繁") || strings.Contains(message, "请求过于频繁") || strings.Contains(message, "操作过于频繁") {
		return nil, fmt.Errorf("%w: ACG 读取请求过于频繁", ErrRateLimited)
	}
	if acgReadNotice(message, "当前商品已停售", "商品已停售", "该商品已停售", "当前商品已下架", "商品已下架", "该商品已下架", "商品已停止销售", "该商品已停止销售") {
		return nil, fmt.Errorf("%w: ACG 商品已停售", ErrProductUnavailable)
	}
	if acgReadNotice(message, "商品不存在", "当前商品不存在", "该商品不存在", "商品已删除", "该商品已删除") {
		return nil, fmt.Errorf("%w: ACG 商品不存在", ErrProductDeleted)
	}
	return nil, err
}

// Accept punctuation/help text after an explicit notice, not arbitrary mentions
// such as "商品已下架检测失败" or a quoted phrase in an authentication error.
func acgReadNotice(message string, notices ...string) bool {
	for _, notice := range notices {
		if message == notice {
			return true
		}
		if suffix, ok := strings.CutPrefix(message, notice); ok && suffix != "" {
			first, _ := utf8.DecodeRuneInString(suffix)
			if unicode.IsSpace(first) || strings.ContainsRune("，,。.!！?？:：;；", first) {
				return true
			}
		}
	}
	return false
}
