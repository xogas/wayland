# Wayland Go Binding Examples

本目录包含多个独立可运行的 Wayland 客户端示例, 演示 github.com/xogas/wayland 库的各种用法.

## 运行要求

- Linux 系统, 运行中的 Wayland compositor (KDE/KWin, GNOME/Mutter, Sway, Weston 等)
- XDG_RUNTIME_DIR 环境变量已设置
- Go 1.26+

## 基础示例

### globals -- 全局接口枚举

连接 Wayland compositor, 获取所有 global 接口并打印名称与版本号.

```sh
go run ./example/globals
```

### outputs -- 显示器信息与热插拔监听

绑定所有 wl_output, 打印每个显示器的名称、分辨率、刷新率、缩放系数; 然后进入 5 秒动态监听, 期间热插拔显示器会实时打印新增/移除事件.

```sh
go run ./example/outputs
```

### helloworld -- 最简窗口 + 内嵌字体

使用 xdg-shell 创建 toplevel 窗口, 内嵌 5x7 点阵字体, 在共享内存缓冲区中渲染 "Hello, Wayland!" 文字. 窗口尺寸根据文字内容自适应.

```sh
go run ./example/helloworld
```

无交互按键, 30 秒超时自动退出.
