# naivereal

在官方 [naiveproxy](https://github.com/klzgrad/naiveproxy) 内核(C++/Chromium)基础上, 于 TCP 传输层增加 [REALITY](https://github.com/XTLS/REALITY)(Xray 现行机制) 的代理套件.

## 组件

| 组件 | 目录 | 说明 |
|---|---|---|
| 服务端 REALITY 前端 | frontend/ | Go; 复用 Xray 的 xtls/reality 服务端 fork, 终结 REALITY TLS/h2, 以 HTTP/1.1 CONNECT 转发给官方 naive 服务端; 也支持普通 TLS 模式(等价 Caddy) |
| H3 frontend | h3frontend/ | 独立 QUIC/HTTP3 CONNECT 前端; 不包含 REALITY; 由 release-components 工作流自动发布 |
| 服务端 naive 内核 | (上游) | 官方 naiveproxy 服务端二进制(不做任何改动, CI 按 CHROMIUM_VERSION 从上游拉取) |
| 客户端内核 | patches/ | 官方 naiveproxy 客户端 + REALITY 补丁(BoringSSL); CI 克隆上游源码后应用 patches/001-004 构建, 单一 exe, 保持官方 config.json 契约, 可替换 v2rayN 目录内的 naiveproxy 内核 |
| Windows TUI 客户端 | tui/ | Go/bubbletea; 档案管理, 统计, 系统代理, TUN 模式(wintun + gVisor), 分享链接导入导出 |

## 架构

```
[浏览器] -> 本地 SOCKS5/HTTP -> [客户端内核: 官方 naive + REALITY 补丁]
  -> TCP -> [服务端 REALITY 前端(Go, xtls/reality)] -> [官方 naive 服务端(127.0.0.1)] -> 目标站

未认证 TLS 探测流量: 前端把 ClientHello 原样中继到 target(如 www.microsoft.com:443),
探测者看到目标站的真实 TLS 握手; 认证客户端收到 HMAC 临时证书并进入 h2 CONNECT 隧道
(naive padding 帧端到端透明).
```

## 快速开始

服务端(Linux):

```sh
./naivereal-frontend genkey                       # 生成 REALITY X25519 密钥对
cp frontend/frontend.toml.example frontend.toml   # 填入 private_key/short_ids/server_names/target
./naive --listen=http://user:pass@127.0.0.1:8080  # 官方 naive 服务端
./naivereal-frontend frontend.toml                # REALITY 前端监听 :443
```

Windows: 见 docs/windows.md; v2rayN 内核替换见 docs/v2rayN.md.

## 构建

- frontend: cd frontend && go build ./... (本网络环境建议 GOPROXY=https://goproxy.cn,direct)
- tui: cd tui && go build ./... (TUN 依赖 gvisor/wireguard-go 较大, 首次构建需下载)
- 客户端内核(C++): 推送 GitHub 后由 .github/workflows/build-kernel.yml 自动构建
  (linux x64/arm64 + windows x64): CI 克隆 klzgrad/naiveproxy(按 CHROMIUM_VERSION 校验)
  并应用 patches/001-004; 本地构建同样 = 克隆上游 + 应用补丁 + 官方 get-clang.sh/build.sh.
- 补丁: patches/001-004(boringssl + net 接线 + 蜘蛛模式 + 构建注册), 均已 git apply --check 验证.
- 测试: cd frontend && go test ./...; cd tui && go test ./...

## 自动发布

- `h3frontend/` 和 `frontend/` 均由 `.github/workflows/release-components.yml` 自动构建并发布：
  - push 到 `main` 且相关目录变更时，更新 `continuous` release；
  - 推送 `v*` tag 时，创建正式 GitHub Release。
- 产物包括：
  - `naivereal-h3frontend-linux-amd64`
  - `naivereal-h3frontend-linux-arm64`
  - `naivereal-frontend-linux-amd64`
  - `naivereal-frontend-linux-arm64`
  - `naivereal-frontend-windows-amd64.exe`
- 官方 naive 内核仍由 `.github/workflows/build-kernel.yml` 单独构建，不混入上述 Go 组件产物。

## 状态

见 docs/status.md(组件完成度, 待办与所需外部条件).

## 许可证

- 本项目 Go 代码: MIT
- naiveproxy 内核与派生补丁: BSD-3-Clause(上游条款)
- 依赖: github.com/xtls/reality (MPL-2.0), golang.org/x/net (BSD-3), bubbletea (MIT), wintun (WireGuard 许可, 随包附带 LICENSE), gVisor (Apache-2.0)