# wayland

纯 Go 实现的 Wayland 客户端协议库, 零外部依赖 (仅标准库).

- wayland version: 1.25.0
- wayland-protocols version: 1.49

## 特性

- 完整的 wire 协议实现: Unix socket 通信, SCM_RIGHTS fd 传递
- 核心协议 (wayland.xml) 全部接口绑定, 位于根包
- 65+ 扩展协议绑定 (xdg-shell, viewporter, presentation-time 等), 按成熟度分层于 `protocol/`
- 代码生成器 `wayland-scanner`: 从协议 XML 生成类型安全的 Go 绑定
- 并发安全的事件分发, 正确的对象与 fd 生命周期管理

## 安装

```sh
go get github.com/xogas/wayland
```

## 快速开始

连接 compositor 并列出全部全局对象:

```go
package main

import (
    "context"
    "fmt"

    "github.com/xogas/wayland"
)

func main() {
    ctx := context.Background()

    dpy, err := wayland.Connect(ctx)
    if err != nil {
        panic(err)
    }
    defer dpy.Close()

    reg, _ := dpy.GetRegistry()
    reg.OnGlobal(func(ev wayland.RegistryGlobalEvent) {
        fmt.Printf("%s (version %d)\n", ev.Interface, ev.Version)
    })

    dpy.Roundtrip(ctx)
}
```

更多示例见 [example/](./example/readme.md): 窗口创建, shm 渲染, 输入处理, 拖放, 子表面等 17 个可运行示例.

## 代码生成

`*_gen.go` 文件由 wayland-scanner 生成, 不要手动修改:

```sh
make gen        # 重新生成核心协议与全部扩展协议
```

## 要求

- Linux
- Go 1.26+
- 运行中的 Wayland compositor (KWin, Mutter, Sway, Weston 等)

## License

The MIT License (MIT) - see [license](./license) for more details.
