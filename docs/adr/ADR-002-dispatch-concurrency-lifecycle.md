# ADR-002: 分发, 并发与生命周期

- **状态**: Accepted
- **日期**: 2026-11-15
- **决策人**: 库作者
- **触发**: 改变进程 / 线程模型; 改变对外契约
- **不可逆性**: 高

## 背景

`docs/spec.md` 要求: 回调串行; 不止一个 goroutine 可以同时发请求; Dispatch 阻塞; Roundtrip 带 context; Close 可唤醒阻塞的分发; 对象有 alive / destroyPending / destroyed 状态. 需要决定谁来驱动事件循环, 谁执行回调, 以及 Close 与并发的交互.

## 备选方案

| 方案 | 优点 | 缺点 |
| :--- | :--- | :--- |
| A. 库内后台分发 goroutine | 用户不用驱动 Dispatch | 回调线程归属复杂; 关闭与错误传播难; 与 spec 的阻塞 Dispatch 冲突 |
| B. 调用方驱动 Dispatch, 回调在调用者 goroutine | 回调串行天然成立; 用户控制生命周期; 无后台 goroutine | 用户必须自己跑分发; 回调阻塞会阻塞分发 |
| C. 每个对象一个分发 goroutine | 隔离 | 回调并发, 违反 spec; goroutine 爆炸 |

## 决策

选择 B.

- `Display.Dispatch()` 与 `Display.Roundtrip(ctx)` 由调用方驱动; 同一时刻只允许一个在运行, 其他调用等待.
- 回调在持有分发权的调用者 goroutine 上同步执行; 库不创建回调 goroutine.
- 请求发送由一把写锁串行化; 每条消息完整写入; 同一 goroutine 内顺序保持; 不同 goroutine 之间只保证消息原子性.
- 回调里可以发请求; 分发期间不持有写锁, 所以不会自锁.
- `Close` 幂等; 第一次返回关闭错误 (若有), 后续返回 nil; 并发 Close 串行化; Close 等待正在运行的分发退出. 在回调里调用 Close 属于非法重入, 返回 `ErrInvalidOperation`, 不等待.
- `Roundtrip(ctx)` 取消后返回 `ctx.Err()`; 连接保持可用; 被取消的 callback 不再交付; 不消费其他对象的事件.
- 连接入口: `Connect()` 按标准顺序读环境; `DialContext(ctx, path)` 接受显式路径, 用于测试与嵌入; `DialFD(fd)` 接管已连接 fd. 不提供内部 connect 超时; `DialContext` 由调用方控制.
- 库内不启动常驻 goroutine; 未来若需要, 必须先写新 ADR.

## 后果

- 正面: 回调串行是结构保证, 不靠锁; 无后台 goroutine 泄漏; Close 与分发语义可判定; 测试用 socketpair / fake server 即可.
- 负面: 回调阻塞会阻塞整个分发; 用户必须显式驱动 Dispatch / Roundtrip; Close 不能从回调里直接调用; 发送在 socket 满时阻塞, 只能靠 Close 唤醒.
- 需要同步修改: `docs/contracts.md` 的 Display / Close / Roundtrip / 错误契约; `docs/design/failure.md` 的超时与重试; 示例的 `main` 结构.

## 不可逆性

- 以后改回: 改所有调用方的驱动方式, Close 语义与回调执行线程; 发布后需要 major 版本.
- 分级: 高.
- 回滚方案: v0.1.0 前整体重写; 发布后若引入后台分发, 必须保留调用方驱动模式作为兼容路径, 并写新 ADR.
