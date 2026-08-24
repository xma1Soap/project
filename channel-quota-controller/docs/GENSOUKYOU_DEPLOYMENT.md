# 幻想乡公益站专用部署闸门

本文只适用于幻想乡公益站当前定制环境：New API `v8.13`、SQLite、Docker
Compose、无 Redis。它不是通用 New API 部署说明。

## 当前可验证产物

- 后端工作树：`new-api-gensoukyou-worktree`；
- Linux/amd64 二进制：`artifacts/new-api-gensoukyou-v8.13-linux-amd64`；
- SHA-256：`5A82CFFC550F09D81AD2F813B5B38885692AB9B237190A171A6C71B40298AFF0`；
- Python 控制器入口：`gensoukyou-channel-controller`；
- Go 全仓库测试和 Python 22 项测试均已通过。

二进制包含两个站点专用的增量能力：

1. `TestChannel` 失败响应增加结构化 `status_code`；
2. root-only 的精确路由读写接口：
   - `GET /api/channel/routes/:id`
   - `PUT /api/channel/route`

精确路由使用 `channel_id + group + model`，更新请求必须带
`expected_enabled`。状态不一致返回冲突，不会降级为关闭整个渠道。

## 测试实例部署

测试实例当前把主站二进制 `/opt/new-api/new-api.bin` 挂载为 `/new-api`。
不要覆盖这个共享文件。应改用独立路径：

```text
/opt/new-api-test/new-api-test.bin
```

部署顺序：

1. 备份测试数据库，最好先执行 SQLite checkpoint；
2. 上传新二进制为 `/opt/new-api-test/new-api-test.bin.new`；
3. 在 VPS 上核对 SHA-256；
4. 将文件原子改名为 `new-api-test.bin` 并设为 `0755`；
5. 使用 `deploy/test/docker-compose.yml`，确认 volume 不再指向主站二进制；
6. 只重建 `new-api-test`，不要重启 `new-api`；
7. 先测试登录、渠道列表、`glm-5.3` 推理和新增只读 route API；
8. 再对测试渠道执行一次 `true -> false -> true` 的比较并交换演练；
9. 每一步读取数据库和 API 回验，确认整渠道状态没有改变。

回滚只需让测试 Compose 重新挂载旧测试二进制并重建 `new-api-test`。主站
容器及 `/opt/new-api/new-api.bin` 始终不参与测试。

## 控制器凭证

凭证只能写入 root 可读的环境文件或 systemd credential，不得放入 JSON、命令行、
Git 或审计日志。控制器读取：

```text
GENSOUKYOU_ADMIN_ACCESS_TOKEN
GENSOUKYOU_NEW_API_BASE_URL
GENSOUKYOU_NEW_API_USER_ID
```

测试实例建议使用 `http://127.0.0.1:3001`。明文 HTTP 只允许 loopback；远程调试应走
SSH 隧道。

## 主站上线闸门

以下条件全部满足前，不部署主站：

- 测试实例精确禁用/恢复成功；
- 主站数据库和旧二进制已有可恢复备份；
- 已确认实际用户组，而不是默认假设 `default`；
- 已确认上游每日重置时间；
- 已确认测试接口能保留 `exceed quota limit`、`余额不足` 等额度标记；
- 主站旁路 dry-run 至少观察一个完整额度周期；
- 每个池至少保留两条活动路由；
- 只有带精确 opt-in 标签的渠道会被管理。

主站 live 需要三道开关：配置 `dry_run=false`、命令行
`--confirm-live-actions`、命令行 `--confirm-production-host gensoukyou.xyz`。
缺少任意一项都会强制 dry-run。

## 当前配置的限制

`examples/gensoukyou.glm.production-dry-run.json` 只列出已知出现额度错误的 #86/#87，
并保持 dry-run。其中 `reset_time=00:00 Asia/Shanghai` 只是待站长确认的占位值，不能据此
启用 live。其它 GLM 路由应在确认渠道 group、上游账号独立性和配额规则后再加入同一池。
