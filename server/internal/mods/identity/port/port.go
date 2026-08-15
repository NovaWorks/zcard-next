// Package port 为 identity 模块对外契约（零依赖包，跨模块引用的唯一合法入口，规划 §4.4）。
//
// 只允许 import 标准库 / platform/* / api 生成类型；违反即架构测试红灯（§4.10-11）。
package port

import "context"

// AdminAccount 员工账户 DTO（供 audit 等模块取操作者信息；不含密码哈希等敏感字段）。
type AdminAccount struct {
	ID          uint64
	Username    string
	Nickname    string
	Avatar      string
	RoleID      uint64
	Enabled     bool
	TOTPEnabled bool
	LastLoginIP string
}

// AdminReader 其他模块读取员工信息的窄接口（通道 A：需要返回值的同步调用）。
type AdminReader interface {
	Admin(ctx context.Context, id uint64) (*AdminAccount, error)
}

// AdminInput 创建/更新员工参数。
type AdminInput struct {
	Username string
	Password string // 仅创建
	Nickname string
	Avatar   string
	RoleID   uint64
	Remark   string
	Enabled  *bool // 仅更新（Toggle）
}

// AdminMutator 员工管理窄接口（authz API 面消费，数据层在 identity 模块；P0-03 T2）。
type AdminMutator interface {
	List(ctx context.Context) ([]AdminAccount, error)
	Create(ctx context.Context, in AdminInput) (*AdminAccount, error)
	Update(ctx context.Context, id uint64, in AdminInput) (*AdminAccount, error)
	// ExistsRoleInUse 角色是否仍有员工挂载（删除角色前置校验）。
	RoleInUse(ctx context.Context, roleID uint64) (bool, error)
}
