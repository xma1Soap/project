# Channel Quota Controller

用于 AI 公益站渠道/模型的额度熔断控制器。当前版本首先服务于安全演练：默认只读、默认 `dry-run`，附带本地 JSON 适配器，不会连接或修改线上主站。

## 安全边界

- 配置文件默认 `"dry_run": true`。
- 即使配置设为 `false`，启动时缺少 `--confirm-live-actions` 仍会强制 dry-run。
- 只管理配置中逐条列出的 `channel_id + model`。
- 渠道必须带有指定的精确标签（默认 `auto-quota`），否则拒绝操作。
- 默认按“渠道 + 模型”摘除，不关闭整个渠道。
- 可将多条路由放入同一 `pool_id`，摘除前检查池内剩余容量。
- `min_active_routes_after_disable` 默认为 1，防止自动关闭最后一条受管路由。
- 普通 429、502、超时不会触发；必须同时匹配状态码和额度错误文本，并连续达到阈值。
- 不会启用人工或外部系统禁用的路由。
- 只恢复由本控制器亲自暂停且仍持有所有权的路由。
- 恢复前需要连续探测成功；管理员手动干预后控制器释放所有权，不与管理员争抢状态。
- 每次操作前重新读取标签和路由状态，操作后再次读取验证。
- 单实例锁避免两个进程同时修改同一状态。
- 审计日志不记录 API 密钥、提示词或完整用户请求。

## 当前阶段

项目已收窄为幻想乡公益站专用控制器。完整定制 `v8.13` 源码已可构建，后端增加了
`channel + group + model` 精确路由状态接口，Python 端增加了站点专用入口和生产第三道
确认开关。当前仍只允许在隔离测试实例验证，主站必须先旁路 dry-run。

New API 适配边界与收到源码后的接入步骤见
[`docs/NEWAPI_INTEGRATION.md`](docs/NEWAPI_INTEGRATION.md)。
已收到的定制 `v8.13` 源码结论见
[`docs/SOURCE_REVIEW_V8.13.md`](docs/SOURCE_REVIEW_V8.13.md)。
测试部署和主站闸门见
[`docs/GENSOUKYOU_DEPLOYMENT.md`](docs/GENSOUKYOU_DEPLOYMENT.md)。

## 本地运行

```bash
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -e .

channel-quota-controller \
  --config examples/config.dry-run.json \
  --channels examples/channels.json \
  --events examples/events.jsonl \
  --state var/state.json \
  --audit var/audit.jsonl \
  --lock var/controller.lock \
  --now 2099-01-01T00:01:00+08:00 \
  --once
```

输出中的 `dry_run` 应为 `true`，额度条件命中时只会显示 `would_disable`。`--now`
只能和 `--once` 同用，用来让示例数据每次演练都有确定结果，长期服务不应设置它。

## 双重 live 开关

仅用于本地模拟验证：

1. 将复制出的测试配置改为 `"dry_run": false`；
2. 启动时额外传入 `--confirm-live-actions`。

```bash
channel-quota-controller ... --confirm-live-actions --once
```

这时也只会修改 `--channels` 指定的本地 JSON，不会连接主站。

## 事件格式

每行一条 JSON：

```json
{"timestamp":"2026-08-24T10:00:00+08:00","channel_id":1001,"model":"provider-v4p-free","status_code":429,"message":"daily quota exceeded"}
```

时间必须带时区。错误消息只用于识别额度特征；审计日志不会复制完整消息。

## 状态所有权

控制器只在实际执行摘除成功后写入：

```text
normal -> quota_disabled (owned_by_controller=true)
quota_disabled -> probe -> enable -> normal
```

如果路由原本已经关闭而状态库没有所有权记录，控制器将其视为人工禁用并跳过。

## 测试

```bash
PYTHONPATH=src python -m unittest discover -s tests -v
```

覆盖场景包括 dry-run 不变更、普通 429 不误判、缺少标签拒绝、只摘除单个模型、人工禁用保护、两次探测后恢复及时区重置计算。

## Linux 服务

`systemd/channel-quota-controller.service` 是加固模板，默认不带 live 确认参数。正式部署前仍需创建专用的非 root 用户、确定线上只读日志来源和主站 API 适配器。
