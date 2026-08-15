// Package id 雪花 ID 生成器（规划 §4.11.4 ID 策略）。
//
// 对外业务单号（订单/工单/退款单）必须不可枚举（防 IDOR 取货，铁律 12），
// 且跨库导出/合并不冲突；内部主键保持数据库自增，二者严格分离。
package id

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	epochMs    int64 = 1735689600000 // 2025-01-01T00:00:00Z（毫秒）
	workerBits uint  = 10            // 1024 实例
	seqBits    uint  = 12            // 单机每毫秒 4096 个
	maxWorker  int64 = (1 << workerBits) - 1
	maxSeq     int64 = (1 << seqBits) - 1
)

// Generator 雪花 ID 生成器（41 位毫秒 + 10 位机器 + 12 位序列，并发安全）。
type Generator struct {
	mu       sync.Mutex
	workerID int64
	lastMs   int64
	seq      int64
}

// NewGenerator 构造。workerID 取值 [0, 1023]；Schema/Database 模式下由实例 ID 派生，
// 保证跨实例唯一（§4.11.4）。
func NewGenerator(workerID int64) (*Generator, error) {
	if workerID < 0 || workerID > maxWorker {
		return nil, fmt.Errorf("id: workerID %d 超出 [0,%d]", workerID, maxWorker)
	}
	return &Generator{workerID: workerID}, nil
}

// Next 生成下一个 ID；时钟回拨在同一毫秒内由序列位吸收，跨毫秒回拨返回错误（拒绝生成重复号）。
func (g *Generator) Next() (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	ms := time.Now().UnixMilli()
	if ms < g.lastMs {
		return 0, errors.New("id: 时钟回拨，拒绝生成（防重复单号）")
	}
	if ms == g.lastMs {
		g.seq++
		if g.seq > maxSeq {
			// 本毫秒序列耗尽：等待下一毫秒
			for ms <= g.lastMs {
				ms = time.Now().UnixMilli()
			}
			g.seq = 0
		}
	} else {
		g.seq = 0
	}
	g.lastMs = ms

	ts := ms - epochMs
	return ts<<(workerBits+seqBits) | g.workerID<<seqBits | g.seq, nil
}

// FormatNo 拼接对外单号：prefix + 雪花 ID（如订单 "S1234567890123456789"，前缀可配）。
func FormatNo(prefix string, snowflakeID int64) string {
	return fmt.Sprintf("%s%d", prefix, snowflakeID)
}
