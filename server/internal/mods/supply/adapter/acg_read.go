package adapter

import (
	"encoding/json"
	"fmt"
	"strings"
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
	switch message {
	case "当前商品已停售", "商品已停售", "商品已下架":
		return nil, fmt.Errorf("%w: ACG 商品已停售", ErrProductUnavailable)
	case "商品不存在", "当前商品不存在":
		return nil, fmt.Errorf("%w: ACG 商品不存在", ErrProductDeleted)
	}
	return nil, err
}
