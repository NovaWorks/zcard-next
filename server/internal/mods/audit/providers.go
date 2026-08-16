package audit

// wire providers（P2-06）。
//
// 跨模块绑定（通道 A）：
//   - AuditRepo 实现 auditport.Auditor（identity/inventory/authz 埋点）
//   - AuditRepo 实现 auditport.RiskGate（order 下单闸门 / fulfillment 取货锁定）

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/audit/port"

	"github.com/google/wire"
)

// ProviderSet audit providers。
var ProviderSet = wire.NewSet(
	NewAuditRepo,
	NewVisitCounter,
	wire.Bind(new(port.Auditor), new(*AuditRepo)),
	wire.Bind(new(port.RiskGate), new(*AuditRepo)),
	NewAdminAuditService,
)
