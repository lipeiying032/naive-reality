# naivereal

基于官方 [naiveproxy](https://github.com/klzgrad/naiveproxy) 的代理套件。当前默认采用自有域名/证书的标准 TLS 和 H3，客户端保持上游 Chromium QUICHE/BoringSSL 网络栈。TCP REALITY 作为显式选择的兼容构建保留。

**QUIC REALITY 已移除。** 旧配置会明确拒绝，不能直接替换旧客户端/服务端；迁移与验证见 [H3 自有站点模式](docs/h3-origin.md)。

## 组件

| 组件 | 目录 | 说明 |
|---|---|---|
| 服务端 REALITY 前端 | frontend/ | Go; 复用 Xray 的 xtls/reality 服务端 fork, 终结 REALITY TLS/h2, 以 HTTP/1.1 CONNECT 转发给官方 naive 服务端; 也支持普通 TLS 模式(等价 Caddy) |
| H3 frontend | h3frontend/ | 默认 origin 模式：自有网站与逐请求认证的 CONNECT 共用标准 H3/TLS 端点；可选 TCP HTTPS 网站/Alt-Svc |
| 服务端 naive 内核 | (上游) | 官方 naiveproxy 服务端二进制(不做任何改动, CI 按 CHROMIUM_VERSION 从上游拉取) |
| 客户端内核 | patches/ | 默认 native-h3 仅应用配置拒绝补丁 005，不修改网络栈；可选 tcp-reality 使用 001–004、006 |
| Windows TUI 客户端 | tui/ | Go/bubbletea; 档案管理, 统计, 系统代理, TUN 模式(wintun + gVisor), 分享链接导入导出 |

## 默认 H3 架构

```
原生 Chromium 客户端 -> 标准 QUIC/TLS -> H3 frontend（自有证书）
                                         ├─ GET/HEAD：自有网站
                                         └─ 每请求认证 CONNECT -> 本地 naive 上游
```

服务端共用同一 QUIC/TLS 栈，无 Initial 认证预检、第三方 target relay 或自定义握手 proof。Go 服务端不等同于 Chromium 服务端，也不宣称与任意第三方网站不可区分。

## 可选 TCP REALITY 架构

```
[浏览器] -> 本地 SOCKS5/HTTP -> [客户端内核: 官方 naive + REALITY 补丁]
  -> TCP -> [服务端 REALITY 前端(Go, xtls/reality)] -> [官方 naive 服务端(127.0.0.1)] -> 目标站

未认证 TLS 探测流量: 前端把 ClientHello 原样中继到 target(如 www.microsoft.com:443),
探测者看到目标站的真实 TLS 握手; 认证客户端收到 HMAC 临时证书并进入 h2 CONNECT 隧道
(naive padding 帧端到端透明).
```

## H3 配置

使用 [服务端示例](h3frontend/origin.toml.example) 和根目录 `config.json`，填写自有域名、证书/私钥、公开网站目录及与本地 naive 上游相同的用户名/密码。默认内核不接受 REALITY 参数。

```sh
cd h3frontend && go build -o /tmp/naivereal-h3frontend .
/tmp/naivereal-h3frontend check /path/to/h3frontend.toml
```

配置检查不启动监听器。迁移工具、运行方式和 TCP 网站入口见 [H3 文档](docs/h3-origin.md)。

## 可选 TCP REALITY 配置

以下命令仅对应 TCP 前端；客户端需显式选择 tcp-reality 构建。服务端(Linux):

```sh
./naivereal-frontend genkey                       # 生成 REALITY X25519 密钥对
cp frontend/frontend.toml.example frontend.toml   # 填入 private_key/short_ids/server_names/target
./naive --listen=http://user:pass@127.0.0.1:8080  # 官方 naive 服务端
./naivereal-frontend frontend.toml                # REALITY 前端监听 :443
```

Windows: 见 docs/windows.md; v2rayN 内核替换见 docs/v2rayN.md.

## 构建

- frontend: cd frontend && go build ./... (本网络环境建议 GOPROXY=https://goproxy.cn,direct)
- tui: 已拆分到独立仓库 [naivereal-tui](https://github.com/lipeiying032/naivereal-tui); `cd tui && go build ./...` (TUN 依赖 gvisor/wireguard-go 较大, 首次构建需下载)
- 客户端内核(C++): 推送 GitHub 后由 .github/workflows/build-kernel.yml 自动构建
  (linux x64/arm64 + windows x64): CI 克隆 klzgrad/naiveproxy(按 CHROMIUM_VERSION 校验)
  默认应用 005；手动选择 tcp-reality 时应用 001–004、006。补丁入口为 scripts/apply-kernel-patches.py，后续使用官方 get-clang.sh/build.sh.
- H3: cd h3frontend && go test -race ./... && go build ./...；迁移测试: python3 tests/test_h3_migration.py -v。
- 测试: cd frontend && go test ./...; cd tui && go test ./...

## 自动发布

- 采用“统一正式版”策略：
  - 推送 `v*` 或 `x.y.z` tag 时，`.github/workflows/release-components.yml` 自动构建并上传：
    - `naivereal-h3frontend-linux-amd64`
    - `naivereal-h3frontend-linux-arm64`
    - `naivereal-frontend-linux-amd64`
    - `naivereal-frontend-linux-arm64`
    - `naivereal-frontend-windows-amd64.exe`
  - 同一 tag 触发的 Release 会再由 `.github/workflows/build-kernel.yml` 构建并上传：
    - `naivereal-kernel-linux-amd64`
    - `naivereal-kernel-linux-arm64`
    - `naivereal-kernel-windows-amd64.exe`
- 不维护 `continuous` / `nightly` 通道，所有正式版本都集中在同一个 tag Release 中。

## 状态

见 docs/status.md(组件完成度, 待办与所需外部条件).

## 许可证

- 本项目 Go 代码: MIT
- naiveproxy 内核与派生补丁: BSD-3-Clause(上游条款)
- 依赖: github.com/xtls/reality (MPL-2.0), golang.org/x/net (BSD-3), bubbletea (MIT), wintun (WireGuard 许可, 随包附带 LICENSE), gVisor (Apache-2.0)
