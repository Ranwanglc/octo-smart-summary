# 智能总结编辑功能 - 实现方案（简化版）

## 一、需求概述

为已完成的总结增加用户手动编辑能力。用户（任务创建者）可以直接修改总结内容并保存。

**范围限定**：仅支持单人模式，不保留编辑历史，不做版本管理。

---

## 二、功能边界

| 维度 | 说明 |
|------|------|
| **可编辑对象** | creator 的 PersonalResult（同步更新 SummaryResult） |
| **编辑权限** | 仅任务创建者 |
| **编辑时机** | 任务状态为 `COMPLETED`(3) |
| **版本管理** | 无。直接覆盖保存 |
| **Citation** | 保存时清理 content 中无引用的 citation，不做 re-index |
| **与 Regenerate 的关系** | Regenerate 覆盖编辑内容（重新生成） |

---

## 三、数据库变更

仅 ALTER 现有表，不新建表：

```sql
-- migrations/sql/20260508-01-alter-tables-for-edit.sql

-- +migrate Up
ALTER TABLE summary_personal_result
    ADD COLUMN edited_at DATETIME DEFAULT NULL COMMENT '最后编辑时间';

ALTER TABLE summary_result
    ADD COLUMN edited_at DATETIME DEFAULT NULL COMMENT '最后编辑时间';

-- +migrate Down
ALTER TABLE summary_personal_result DROP COLUMN edited_at;
ALTER TABLE summary_result DROP COLUMN edited_at;
```

`edited_at IS NOT NULL` 即表示"已被编辑过"。Regenerate 时新生成的 SummaryResult 自带 `edited_at = NULL`，PersonalResult reset 时一并清空。

---

## 四、后端 API

### 4.1 编辑保存接口

**`PUT /api/v1/summaries/:id/edit`**

**权限**: 仅任务创建者，任务状态 = COMPLETED

**Request:**
```json
{
    "content": "编辑后的总结内容...",
    "base_result_id": 456
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| content | string | 是 | 编辑后内容，最大 500KB |
| base_result_id | int64 | 是 | 当前 SummaryResult.ID（防 Regenerate ABA） |

**Response (200):**
```json
{
    "code": 0,
    "message": "ok",
    "data": {
        "edited_at": "2026-05-08T14:30:00Z"
    }
}
```

**Error Responses:**

| HTTP | code | 场景 |
|------|------|------|
| 403 | 40003 | 非创建者 |
| 409 | 40009 | base_result_id 不匹配（已被 Regenerate） |
| 400 | 40005 | 任务非 COMPLETED 状态 |
| 400 | 40010 | content 为空或超限 |
| 404 | 40008 | 任务不存在 |

### 4.2 详情接口变更

现有 `GET /api/v1/summaries/:id` flat 结构追加字段：

```json
{
    "... (existing fields) ...",
    "result_id": 456,
    "result_edited_at": "2026-05-08T14:30:00Z",
    "result_is_edited": true,
    "permissions": {
        "can_edit": true
    }
}
```

`permissions.can_edit` = creator && status == COMPLETED && participants <= 1。

---

## 五、后端核心逻辑

### 5.1 编辑保存流程

```
PUT /api/v1/summaries/:id/edit
    ↓
1. 权限校验：当前用户 == task.creator_id
2. 状态校验：task.status == COMPLETED
3. 单人模式校验：该 task 的 participant 数量 <= 1（拒绝多人任务编辑）
4. 内容校验：content 非空 && len(content) <= 500KB（UTF-8 字节数）
5. 加载当前 SummaryResult，校验 ID == req.base_result_id
   → 不匹配返回 409
6. 加载 PersonalResult（creator 的，不存在则 500 + 日志告警）
7. 内容变更检测：content 文本与当前一致则直接返回 200 + 当前 edited_at（幂等）
8. Citation 清理
9. 开启事务（锁顺序与 Regenerate 保持一致：先 SummaryResult 再 PersonalResult）：
   a. 第一步就 UPDATE SummaryResult（获取行锁）:
      UPDATE summary_result SET content=?, citations_json=?, edited_at=NOW()
      WHERE id = base_result_id
      → rows_affected == 0 则 rollback + 409
   b. 状态复核：SELECT task.status WHERE id=task_id（此时 Regenerate 如果并发执行，
      会因 SummaryResult 行锁被阻塞，或已把 status 改为非 COMPLETED）
      → status != COMPLETED 则 rollback + 400
   c. 更新 PersonalResult: content, citations_json, edited_at=NOW()
10. 返回 edited_at
```

### 5.2 Citation 清理策略

仅清理无引用的 citation，不做 re-index：

```go
func cleanUnreferencedCitations(content string, citations []model.Citation) []model.Citation {
    // Go regexp 不支持 lookahead，两步策略：
    // Step 1: 用与现有 worker 一致的正则 \[(\d{1,5})\] 匹配所有候选
    //         （1-5 位数字，兼容现有 citation index 范围）
    // Step 2: 对每个匹配检查：
    //   - 如果匹配位置在 fenced code block 或 inline code 内，跳过
    //   - 如果匹配后紧跟 '(' 字符（Markdown link），跳过
    //   - 如果匹配在文末（无后续字符），仍记入 referenced set
    //   - 其余记入 referenced set
    referenced := extractReferencedIndices(content)

    var kept []model.Citation
    for _, c := range citations {
        if referenced[c.Index] {
            kept = append(kept, c)
        }
    }
    return kept
}
```

### 5.3 Regenerate 联动

现有 Regenerate handler 的 PersonalResult reset 追加：

```go
updates["edited_at"] = nil
```

SummaryResult 被 DELETE 重建，新记录 `edited_at` 自带 NULL，无需额外处理。

---

## 六、后端代码变更清单

| 文件 | 变更 |
|------|------|
| `internal/model/model.go` | `SummaryResult` 新增 `EditedAt *time.Time` |
| `internal/model/personal_result.go` | `PersonalResult` 新增 `EditedAt *time.Time` |
| `internal/api/handler/edit.go` | **新增**：`EditHandler.EditSummary` |
| `internal/api/handler/task.go` | `GetSummary` 追加 `result_id`, `result_edited_at`, `result_is_edited`, `permissions` |
| `internal/api/handler/task.go` | `Regenerate` 事务追加 `edited_at = nil` |
| `internal/api/router/router.go` | 注册 `PUT /summaries/:id/edit` |
| `internal/service/edit.go` | **新增**：Citation 清理逻辑 |
| `migrations/sql/20260508-01-alter-tables-for-edit.sql` | ALTER 两张表 |

---

## 七、前端代码变更清单

| 文件 | 变更 |
|------|------|
| `summaryApi.ts` | 新增 `editSummary(taskId, content, baseResultId)` |
| `types/summary.ts` | 追加 `result_id`, `result_edited_at`, `result_is_edited`, `permissions` 类型 |
| `SummaryDetailPage.tsx` | 根据 `permissions.can_edit` 显示编辑按钮；切换阅读/编辑模式 |
| `SummaryEditor.tsx` | **新增**：textarea 编辑器组件（保存/取消按钮） |

### 前端交互

| 场景 | 行为 |
|------|------|
| 编辑态 | textarea 编辑 Markdown 原文 |
| 保存成功 | PUT → GET detail 刷新 → 退出编辑态 |
| 409 | toast "内容已更新，请刷新" + 自动刷新详情 |
| 未修改 | 禁用保存按钮 |
| 5xx | 保留编辑内容不退出，toast 提示重试 |

---

## 八、风险点

| 风险 | 应对 |
|------|------|
| Regenerate ABA | base_result_id 校验 + 事务内 WHERE id= 兜底 |
| 超大文本 | 500KB UTF-8 字节数限制 |
| 并发编辑（多端） | last-write-wins，单人场景可接受 |
| Edit 与 Regenerate 并发死锁 | 事务内第一步就 UPDATE SummaryResult（获取行锁），Regenerate 也先 DELETE SummaryResult，锁顺序一致无死锁 |
| 多人任务误编辑 | Step 3 显式校验 participant 数量 <= 1，拒绝多人任务 |
