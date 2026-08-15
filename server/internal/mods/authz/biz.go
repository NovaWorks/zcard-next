// Package authz 自建 RBAC（M0）：admin_roles + role_permissions，权限目录自动生成。
//
// 发卡系统权限模型简单，自建 RBAC 比 Casbin 更轻可控（规划 §3.1）；
// 「域内角色」（分站主只见自己分站）由 subsite_id 租户隔离实现，不做双套权限源。
package authz

import (
	"context"
	"sync"
	"time"
)

// RoleRepo 角色仓储（模块内端口，实现于 data.go）。
type RoleRepo interface {
	// PermissionCodes 返回角色的全部权限点编码。
	PermissionCodes(ctx context.Context, roleID uint64) ([]string, error)
	// Role 角色名（不存在返回空串）。
	Role(ctx context.Context, roleID uint64) (name string, err error)
}

// RbacUsecase 鉴权用例：权限点进程内缓存（30s TTL，角色变更后自然过期；
// M3 多员工实时吊销走 sessions/版本号）。
type RbacUsecase struct {
	repo RoleRepo

	mu    sync.RWMutex
	cache map[uint64]cacheEntry
}

type cacheEntry struct {
	codes   []string
	name    string
	expires time.Time
}

const cacheTTL = 30 * time.Second

// NewRbacUsecase 构造。
func NewRbacUsecase(repo RoleRepo) *RbacUsecase {
	return &RbacUsecase{repo: repo, cache: map[uint64]cacheEntry{}}
}

// Allowed 判定权限；* 通配（super_admin 种子）。
func (uc *RbacUsecase) Allowed(ctx context.Context, roleID uint64, permission string) bool {
	codes := uc.codes(ctx, roleID)
	for _, c := range codes {
		if c == permission || c == "*" {
			return true
		}
	}
	return false
}

// PermissionsOf 权限点清单（含缓存）。
func (uc *RbacUsecase) PermissionsOf(ctx context.Context, roleID uint64) ([]string, error) {
	return uc.codes(ctx, roleID), nil
}

// RoleName 角色名（含缓存）。
func (uc *RbacUsecase) RoleName(ctx context.Context, roleID uint64) string {
	uc.mu.RLock()
	e, ok := uc.cache[roleID]
	uc.mu.RUnlock()
	if ok && time.Now().Before(e.expires) {
		return e.name
	}
	name, err := uc.repo.Role(ctx, roleID)
	if err != nil {
		return ""
	}
	uc.store(roleID, uc.codes(ctx, roleID), name)
	return name
}

func (uc *RbacUsecase) codes(ctx context.Context, roleID uint64) []string {
	uc.mu.RLock()
	e, ok := uc.cache[roleID]
	uc.mu.RUnlock()
	if ok && time.Now().Before(e.expires) {
		return e.codes
	}
	codes, err := uc.repo.PermissionCodes(ctx, roleID)
	if err != nil {
		return nil
	}
	var name string
	name, _ = uc.repo.Role(ctx, roleID)
	uc.store(roleID, codes, name)
	return codes
}

func (uc *RbacUsecase) store(roleID uint64, codes []string, name string) {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	uc.cache[roleID] = cacheEntry{codes: codes, name: name, expires: time.Now().Add(cacheTTL)}
}
