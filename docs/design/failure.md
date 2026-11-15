# wayland: 失败策略与可观测设计

- **状态**: Frozen
- **冻结日期**: 2026-11-15
- **对应**: `docs/spec.md` v1.0 第 5 节, ADR-002, ADR-005, `docs/contracts.md` 第 6 节

## 失败分类

> 每个错误标识必须落在可重试 / 不可重试 / 需人工介入三类之一. 库不自动重试.

| 错误标识 | 分类 | 触发 | 调用方动作 |
| :--- | :--- | :--- | :--- |
| `ErrSource` (E-01) | 不可重试 | 来源缺失 / 非法 / 路径过长 | 修正 `WAYLAND_SOCKET` / `WAYLAND_DISPLAY` / `XDG_RUNTIME_DIR` |
| `ErrConnect` (E-02) | 可重试 | socket 不存在 / 拒绝 / 权限 | 调用方决定重连与退避; 库不自动重试 |
| `ErrClosed` (E-03) | 不可重试 | closed 上的操作 | 不再使用该 `Display` |
| `ErrIO` (E-04) | 可重试 | 读 / 写失败, EOF, reset | 重新 `Connect` / `DialContext`; 库不自动重试 |
| `ProtocolError` (E-05) | 不可重试 | `wl_display.error` | 读取 object / interface / code / message; 连接已关闭 |
| `ErrMalformedMessage` (E-06) | 不可重试 | 长度 / 对齐 / body 不匹配 | 关闭并重连; 记录现场 |
| `ErrProtocolViolation` (E-07) | 不可重试 | 未知对象 / opcode / delete_id | 关闭并重连; 这是服务端或本地状态错误 |
| `ErrVersion` (E-08) | 不可重试 | bind 版本超出广告或 interface | 降低请求版本后重试 |
| `ErrSince` (E-09) | 不可重试 | 请求 since 大于对象版本 | 修正调用方的版本选择 |
| `ErrInvalidArgument` (E-10 / E-15) | 不可重试 | 非法 null / fd / interface / enum | 修正调用参数 |
| `ErrObjectDestroyed` (E-11) | 不可重试 | destroyPending / destroyed 上发送 | 修正对象生命周期 |
| `context.Canceled` / `DeadlineExceeded` (E-12) | 可重试 | Roundtrip 取消 / 超时 | 调用方决定重试 |
| `GeneratorError` (E-13) | 不可重试 | 输入 / 语义错误 | 修正 XML 或生成器; 不写输出 |
| `GeneratorError` (E-14) | 可重试 | 写入错误 | 修正文件系统后重新运行 |
| `ErrIDExhausted` (E-16) | 不可重试 | 客户端 ID 耗尽 | 释放对象后重试 |
| `ErrNoHandler` | 不可重试 | 事件对象没有 sink | 设置 sink 后继续; 该事件已被消费 |
| `ErrInvalidOperation` | 不可重试 | 回调内 Close / 重复 SetSink / Release 已发送对象 | 修正调用方式 |

## 超时与重试

- **Connect**: 本地 Unix socket, 不设内部超时; 失败立即可见. `DialContext` 的超时由调用方 context 控制.
- **Dispatch**: 不设超时; 阻塞读由 `Close` 唤醒; 协议 / IO 错误返回.
- **Roundtrip**: 超时由 context 控制; 取消返回 `ctx.Err()`; 连接保持可用.
- **Send / 请求方法**: 不设超时; socket 满时阻塞; `Close` 唤醒; 写入失败返回 `ErrIO` / `ErrClosed`.
- **Close**: 不设超时; 等待正在运行的分发退出; 关闭 fd.
- **重试预算**: 库不自动重试. 连接类 E-02 / E-04 由调用方决定退避与次数; Roundtrip E-12 由调用方决定.
- **状态迁移**: E-04 / E-05 / E-06 / E-07 之后连接转 closed; 后续 Send / Dispatch / Roundtrip 返回 `ErrClosed`.
- **幂等前提**: `Close` 幂等; destructor 请求不幂等; bind 重复创建新对象; 请求方法通常不幂等, 由调用方按协议处理.

## 降级

- 库没有外部服务依赖, 没有降级路径.
- 版本不匹配 (E-08) 由调用方降低 bind 版本处理, 不由库自动降级.
- 协议错误 / 格式错误不做静默降级; 关闭连接并返回错误.

## 观测

- 库内不记录日志, 不暴露指标, 不创建追踪 span; 这是 ADR-005 的决定.
- 错误返回必须含操作, interface (适用时), message (适用时); `ProtocolError` 含 objectID / interface / code / message.
- 示例与 CLI 在边界记录错误并决定退出码; 不使用包级全局 logger.
- 不打印密钥, token, 完整请求体; fd 只打印数值.
- 失败路径必须由测试断言; 观测点是错误返回值, 不是日志.

## 与 L5 / L6 的衔接

- L5 观测就位清单: 库没有运行时可观测点位; 示例的 stderr 与退出码是唯一观测.
- L6 巡检: 检查错误分类是否仍与 E-01 到 E-16 对齐; 新增错误必须先写 ADR 与契约.
