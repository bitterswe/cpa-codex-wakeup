# Codex 唤醒任务

适用于支持原生插件和资源菜单的 CLIProxyAPI。在 CPA Manager Plus 左侧打开「Codex 唤醒任务」，连接 CPA 管理接口后即可新建任务。管理密钥仅在当前页面内存中保存。

## 使用唤醒任务

1. 手动选择一个或多个 CPA Codex OAuth 账号，或在「账号池与标签」配置成员并让任务使用该池。池与标签由本插件管理。账号卡显示上游返回的套餐和主、次额度，支持搜索、筛选、全选和分页。
2. 从系统返回的 Codex 模型目录中勾选一个或多个模型，也可批量添加到预设。每次触发会为每个账号分别请求每个选中的模型。临时不可用或额度耗尽的账号仍可加入任务。目录未确认的已有预设会显示提示。提示词留空时发送 `hi`。
3. 选择每日、每周、间隔、配额重置后或启动后。定点任务使用所填 IANA 时区，默认 `Asia/Shanghai`。额度任务按每个账号的窗口分别触发。启动指插件在 CPA 中启动。确定的触发时间最多预览五次。
4. 直接执行会依次向选中账号发送模型请求。执行前确认会在页面生成待确认记录，到期自动跳过。可以手动立即执行、停用、编辑或删除任务。展开执行记录可查看当时的提示词以及每个账号、模型的文本回答。单条回答保存约 32 KiB，超过部分标记截断。v0.2.0 历史可以显示原提示词，旧版未保存的回答无法补回。

调度在 CPA 服务端运行，关闭浏览器后仍然有效。默认状态文件为 `~/.cli-proxy-api/plugins/cpa-codex-wakeup/tasks.json`。容器部署应把目录挂载到持久卷，也可以通过 `CPA_CODEX_WAKEUP_DIR` 指定一个可写的持久化目录。额度和唤醒请求由插件访问 `chatgpt.com`，使用凭据指定的代理或系统 HTTP 代理环境变量。真实唤醒会使用所选账号的模型额度。

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

推送与 `plugin.go` 中版本匹配的标签后，GitHub Actions 会在两种 CPU 架构上分别运行调度与模拟上游测试、编译并加载插件，通过接口测试后发布两个 ZIP 和 `checksums.txt`。每个 ZIP 根目录包含一个 `cpa-codex-wakeup.so`。
