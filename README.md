# Codex 唤醒任务

适用于支持原生插件和资源菜单的 CLIProxyAPI。v0.1.0 提供 CPA Manager Plus 左侧的「Codex 唤醒任务」入口，页面显示已加载的插件版本。账号选择和定时执行属于后续版本。

## 安装

在首个 GitHub Release 发布后，将下面的地址追加到 CPA 的插件商店源。

```text
https://raw.githubusercontent.com/bitterswe/cpa-codex-wakeup/main/registry.json
```

刷新商店，安装并启用 `cpa-codex-wakeup`。从左侧进入「Codex 唤醒任务」，核对版本号。发布包面向 Linux amd64 和 arm64，构建环境为 Debian Bookworm。

## 开发与发布

Linux 本地验证需要 Go、C 编译器和 Python 3。从项目目录执行：

```sh
mkdir -p build
CGO_ENABLED=1 go build -buildmode=c-shared -o build/cpa-codex-wakeup.so .
python3 tests/smoke.py build/cpa-codex-wakeup.so
```

将源码推送到 `bitterswe/cpa-codex-wakeup` 的 `main` 分支。推送与 `plugin.go` 中版本匹配的标签（首次为 `v0.1.0`）后，GitHub Actions 会在两种 CPU 架构上分别编译并加载插件，通过接口测试后发布两个 ZIP 和 `checksums.txt`。每个 ZIP 根目录包含一个 `cpa-codex-wakeup.so`。
