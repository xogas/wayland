# ADR-005: 错误模型与失败分类

- **状态**: Accepted
- **日期**: 2026-11-15
- **决策人**: 库作者
- **触发**: 改变对外契约; 影响可观测性
- **不可逆性**: 中

## 背景

`docs/spec.md` 的 E-01 到 E-16 定义了错误语义, 但把 Go 类型, 包装方式与失败分类留给 L3. 调用方需要 `errors.Is` / `errors.As` 区分连接, IO, 协议, 版本, 对象与生成器错误; 库不能在内部记录日志或自动重试.

## 备选方案

| 方案 | 优点 | 缺点 |
| :--- | :--- | :--- |
| A. 分类哨兵 + `ProtocolError` 类型 | 调用方可用 `errors.Is` 判类别, `errors.As` 取协议字段; 包装保留上下文 | 哨兵数量较多 |
| B. 单一 `*Error` + `Kind` | 类型少 | 调用方每次检查 Kind; 类别判断不直观 |
| C. 只用哨兵 | 简单 | 协议错误的 object / interface / code / message 丢失 |

## 决策

选择 A.

- 导出哨兵: `ErrSource`, `ErrConnect`, `ErrIO`, `ErrClosed`, `ErrProtocolViolation`, `ErrMalformedMessage`, `ErrVersion`, `ErrSince`, `ErrInvalidArgument`, `ErrObjectDestroyed`, `ErrIDExhausted`, `ErrInvalidOperation`, `ErrNoHandler`.
- 导出 `ProtocolError` 类型, 字段 `ObjectID`, `Interface`, `Code`, `Message`; `errors.As` 可取.
- 所有上抛错误用 `fmt.Errorf("<操作>: %w", err)` 保留链; 错误消息含操作, interface (适用时), message (适用时); 不用字符串比较错误.
- `context.Canceled` / `context.DeadlineExceeded` 原样返回, 由调用方判定.
- 库内不记录日志, 不自动重试; 示例与 CLI 在边界记录并决定退出码.
- 生成器错误在 `cmd/waylandgen` 内部使用 `*GeneratorError` (文件, 字段, 消息), 命令打印并返回非 0; 不进入公共运行时 API.
- 失败分类, 超时与重试预算写入 `docs/design/failure.md`; 每个 E-ID 必须落到可重试 / 不可重试 / 需人工介入三类之一.

## 后果

- 正面: 调用方可以按类别处理; 协议错误保留原字段; 没有库内日志与自动重试带来的隐藏行为.
- 负面: 哨兵与 E-ID 的映射需要持续维护; 调用方要处理较多错误类别; 生成器错误类型不公开, 只保证命令输出.
- 需要同步修改: `docs/contracts.md` 的错误章节; `docs/design/failure.md`; 示例的边界日志与退出码.

## 不可逆性

- 以后改回: 改哨兵名与错误类型会影响所有调用方; 发布后需要 major 或别名兼容.
- 分级: 中.
- 回滚方案: v0.1.0 前可重命名; 发布后保留旧哨兵别名, 标记 Deprecated, 并在下一个 major 删除.
