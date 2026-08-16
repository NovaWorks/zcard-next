package data

// Ent 租户拦截器（P1-01 T5 预留）：框架级 subsite_id 行级隔离。
// M1 落地：需要 ent feature intercept + runtime 注册——当前 data 层的显式条件
// （product.SubsiteID(tc.SubsiteID)）已保证隔离，拦截器作为 M1b 框架级强化项。
// 专项测试：两租户数据互不可见（随拦截器交付）。
//
// 接入计划：
//   1. ent feature: intercept（生成 interceptors 包）
//   2. internal/data/ent/runtime.go 注册 DomainInterceptor
//   3. 拦截器读 tenancy.Context 自动追加过滤
//   4. 移除 data 层显式条件（保留拦截器为唯一租户入口）
