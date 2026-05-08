# 智能总结编辑功能 - Review 汇总报告

> CC (后端架构维度) + Codex (前端全栈维度) 双路独立 Review

---

## 🔴 阻塞问题（共 6 项，必须解决后才能开工）

### 1. 乐观锁参数缺失 [CC+Codex 一致]
PUT request body 缺少 `base_version` 字段。后端无法判断客户端基于哪个版本编辑，409 无法可靠产生。

**修复**：请求体增加 `base_version`：
```json
{
    "content": "...",
    "base_version": 3,
    "citations": [...],
    "reason": ""
}
```

### 2. version 字段语义冲突 [CC]
现有 `SummaryResult.version` 表示"第几次 AI 生成"（regenerate 递增），设计方案复用它做编辑版本号。混合语义会导致 `GetNextVersion()` 在 regenerate 时取到编辑版本号，版本含义混乱。

**建议**：分离两个计数器 — 保留 `version` 给 regeneration，新增 `edit_version` 专用于乐观锁。或者统一为单计数器但明确"编辑也是一种新版本"的产品定义。

### 3. PersonalResult 缺少版本字段 [CC+Codex 一致]
现有 `PersonalResult` 没有 `version`/`edit_version` 列，无法支撑个人总结的乐观锁校验。

**修复**：migration 中补充 `ALTER TABLE summary_personal_result ADD COLUMN edit_version INT NOT NULL DEFAULT 0`。

### 4. Regenerate 与编辑历史的交互未定义 [CC]
Regenerate 物理删除 `SummaryResult` 行 → `edit_history.target_id` 成悬空引用。Worker 生成新行 ID 不同 → 历史无法关联。

**建议**：
- Regenerate 事务中清理相关 edit_history（产品上：regenerate = 完全重来，编辑历史归零）
- 或改为 soft-delete + archived 标记
- PersonalResult 的 `edited_at`/`edited_by` 也需在 regenerate reset 列表中

### 5. 详情接口字段不足 [Codex]
当前 `GET /summaries/:id` 的 `personal_result` 缺少 `id`、`citations`、`version`、`edited_at`、`edited_by`。前端保存后无法正确刷新状态，历史接口需要 `target_id` 但前端拿不到。

**修复**：详情接口 personal_result 补齐必要字段。

### 6. 团队汇总 TeamCitations 处理未覆盖 [Codex]
`SummaryResult` 同时有 `CitationsJSON`（消息引用 [n]）和 `TeamCitationsJSON`（成员引用 [Pn]）。设计只描述 `[n]` 清理，`[Pn]` 如何处理未定义。

**修复**：明确编辑时 TeamCitations 的处理策略（建议：不清理 TeamCitations，仅清理普通 Citations）。

---

## 🟡 建议改进（共 12 项）

| # | 问题 | 来源 | 建议 |
|---|------|------|------|
| 1 | CitationText 不应承载编辑态 | Codex | 编辑态直接用 TextArea 编辑原始 Markdown，CitationText 只负责阅读渲染 |
| 2 | 保存后应重新拉详情 | Codex | `editing → saving → PUT → GET detail → done`，不要只 patch 本地状态 |
| 3 | 前端错误处理策略缺失 | Codex | 40003 隐藏入口；40009 弹窗提示过期；5xx 保留编辑内容不退出 |
| 4 | Re-indexing 可能改变用户输入 | CC | 用户提交 [1][3][5] 被重编号为 [1][2][3]，建议仅清理无引用的 citation 不 re-index |
| 5 | 编辑历史接口应分页/懒加载 | CC+Codex | 列表只返回 meta，点击版本再拉完整 content |
| 6 | 缺少 content 无变更检测 | CC | 内容未修改时不应写入 history、不递增 version |
| 7 | content 最大长度未限制 | CC | Handler 层加限制（如 500KB），防恶意大文本 |
| 8 | 权限建议后端返回 permissions 对象 | Codex | `{ can_edit_result, can_edit_personal, can_view_history }` 减少前端规则重复 |
| 9 | 编辑历史 version 需唯一约束 | CC | 加 `UNIQUE INDEX (task_id, target_type, target_id, version)` |
| 10 | Migration 建议拆分 | CC | CREATE TABLE 和 ALTER TABLE 分两个文件，便于部分失败定位 |
| 11 | UX 补齐 | Codex | 未修改禁用保存；取消时二次确认；离开页面提示未保存；TextArea 自动高度 |
| 12 | 前端路径确认 | Codex | 设计写 `packages/dmworksummary/`，但后端仓库 docs 引用的是 `packages/dmworkbase/`，需确认实际落点 |

---

## ✅ 设计优良点（共 6 项）

1. **textarea 方向正确** — Markdown 纯文本编辑，技术风险低，与 react-markdown 渲染链路兼容
2. **后端做引用清理** — 防御性编程好实践，不依赖前端保证数据完整性
3. **事务原子性** — history 写入 + result 更新在同一事务，保证一致性
4. **状态限制合理** — 只允许 COMPLETED 编辑，避免中间态产生不可解释数据
5. **增量交付策略** — P0-P3 分层，核心能力可独立上线
6. **WS/Hub 复用** — 无需新增基础设施

---

## 📊 工作量重估

| Phase | 范围 | 原估计 | 修正估计 |
|-------|------|--------|---------|
| P0 | 后端模型/API/乐观锁/详情字段补齐 | 1天 | **1.5-2天** |
| P1 | 前端编辑态 + 保存 + 刷新 + 异常处理 | 1天 | **1.5天** |
| P2 | 编辑历史（后端 + 前端弹窗基础版） | 0.5天 | **1天** |
| P3 | Citation 清理 + 并发/边界测试 + WS | 0.5天 | **1-1.5天** |
| **合计** | | **3天** | **5-6天** |

---

## 🎯 建议执行顺序

1. **先解决阻塞项 1-4**（明确 version 模型 + 乐观锁 + regenerate 交互）
2. **P0 最小闭环**：团队汇总编辑 + base_version + 保存后刷新详情
3. 验证通过后扩展个人总结编辑
4. 最后做历史弹窗和 Citation 清理

---

_Review by: CC (Claude Opus) + Codex | 2026-05-08_
