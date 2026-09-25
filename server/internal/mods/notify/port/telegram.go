package port

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// TelegramTarget identifies a destination; zero TopicID omits message_thread_id.
type TelegramTarget struct {
	ChatID  string `json:"chat_id"`
	TopicID int64  `json:"topic_id,omitempty"`
}

var telegramChatPattern = regexp.MustCompile(`^(-?[0-9]+|@[A-Za-z0-9_]{5,})$`)

func NormalizeTelegramTarget(t TelegramTarget) (TelegramTarget, error) {
	t.ChatID = strings.TrimSpace(t.ChatID)
	if len(t.ChatID) > 64 || !telegramChatPattern.MatchString(t.ChatID) {
		return t, fmt.Errorf("Chat ID 须为非零整数或 @用户名")
	}
	if strings.HasPrefix(t.ChatID, "@") {
		t.ChatID = strings.ToLower(t.ChatID)
	} else {
		id, err := strconv.ParseInt(t.ChatID, 10, 64)
		if err != nil || id == 0 {
			return t, fmt.Errorf("Chat ID 须为非零整数或 @用户名")
		}
		t.ChatID = strconv.FormatInt(id, 10)
	}
	if t.TopicID < 0 || t.TopicID > 2147483647 {
		return t, fmt.Errorf("Topic ID 须为 1–2147483647 的整数，不使用话题请留空")
	}
	return t, nil
}

func ParseTelegramTargets(raw []byte) ([]TelegramTarget, error) {
	var targets []TelegramTarget
	if len(raw) > 8192 || json.Unmarshal(raw, &targets) != nil || targets == nil {
		return nil, fmt.Errorf("接收位置须为列表")
	}
	if len(targets) > 20 {
		return nil, fmt.Errorf("接收位置最多 20 个")
	}
	seen := map[TelegramTarget]bool{}
	for i, t := range targets {
		normalized, err := NormalizeTelegramTarget(t)
		if err != nil {
			return nil, fmt.Errorf("第 %d 个接收位置：%w", i+1, err)
		}
		if seen[normalized] {
			return nil, fmt.Errorf("第 %d 个接收位置重复，请检查 Chat ID 和 Topic ID", i+1)
		}
		seen[normalized] = true
		targets[i] = normalized
	}
	return targets, nil
}
