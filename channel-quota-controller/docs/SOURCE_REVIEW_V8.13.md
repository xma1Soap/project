# 定制 New API v8.13 源码审查

审查对象：`new-api-backend-20260824.tar.gz`。压缩包的 `VERSION` 为 `v8.13`，不含 `.git`
元数据，因此可以审查当前快照，但无法准确还原它相对哪个上游 commit 的全部差异。
随后收到的完整包包含 `logger`、`i18n`、`oauth`、`web` 和 Git 对象。通过独立 Git
工作树恢复打包时省略的前端文件后，两个前端和 Linux/amd64 后端均构建成功，Go 全仓库
测试通过。

## 已确认能力

- `GET /api/channel/:id` 可读取脱敏渠道详情。
- `PUT /api/channel/` 可更改整个渠道状态；该路径会重建 Ability 并重新初始化渠道缓存。
- `GET /api/channel/test/:id?model=...` 可直接指定渠道和模型探测。
- `GET /api/log/` 支持按时间、渠道、模型和日志类型筛选，单页最大 100 条。

## 必须保持关闭的能力

### 单渠道+单模型启停

后端没有原子管理接口。通过 `PUT /api/channel/` 改写 `models` 会删除并重建该渠道的所有
Ability，且没有版本号/ETag 用于防止覆盖管理员的并发变更。因此不能将其当作安全的
单模型摘除手段，也不能降级为关闭整个渠道。

### 通过数据库错误日志识别 429 额度耗尽

`controller/relay.go:processChannelError` 在写数据库前明确排除 HTTP 429 和上游空响应。因此
虽然日志 API 的筛选能力完整，它的数据源并不覆盖典型的每日限额 429，不能单独用于触发自动摘除。

## 定制代码现状

- 新增了 `ChannelCooldownEnabled` / `ChannelCooldownSeconds`。
- `processChannelError` 对任何渠道类错误设置整渠道的进程内冷却，与自动禁用开关无关。
- 为同时覆盖内存缓存和直连数据库选路，冷却过滤分别写在 `model/channel_cache.go`
  和 `model/ability.go`，两条路径需要持续保持一致。
- 源码包保留了多份 `.bak-cooldown*` 文件。它们不会被 Go 编译，但说明当前是直接
  在生产树中反复试改的快照，不适合继续堆叠渠道额度逻辑。

## 接入决策

`examples/newapi-contract.v8.13.json` 已将能力标记为：

- 整渠道状态变更：已确认；
- 单模型状态变更：关闭；
- 指定路由探测：已确认；
- 完整额度错误事件查询：关闭。

下一个最小、可审查的后端改动应是专用的脱敏渠道健康事件输出，或为指定渠道测试响应
增加 `status_code`；不应直接操作 Ability 表。

站点专用工作树现已实现两项增量：`TestChannel` 在失败响应中增加结构化 `status_code`；
新增 root-only 的 `channel + group + model` 精确 Ability 比较并交换接口。内存缓存构建也已
改为尊重 `abilities.enabled`。相关单测、核心包测试和 Go 全仓库测试均通过，但 live 能力仍需
测试实例的禁用/恢复演练后才能启用。
