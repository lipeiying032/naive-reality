# 当前 H3 状态

H3 已迁移到自有域名/证书的标准 TLS 路线。默认客户端 profile 为 `native-h3`，默认服务端模式为 `origin`；**默认服务端实现为 `h3native/`（C++/QUICHE）**，`h3frontend/`（Go/quic-go）作为对照基线与退路保留。旧 QUIC REALITY 已移除，配置会明确拒绝。实现和迁移见 [h3-origin.md](h3-origin.md)。

## 为什么默认换成原生服务端

Go 服务端在协议层完全正确，但它**可被识别**：QUIC 服务端的传输参数块会暴露实现。用 `tools/naive-fp` 实测，两者在 10 个属性上不同，其中一条一次握手即可判定——QUICHE 必发 `version_information`(0x11) 并每连接打乱参数顺序，quic-go 两者都不做。QUIC Hunter（PAM 2024）正是靠这一信息识别 18 种服务端库。

完整测量与取舍见 [原生 H3 研究](native-h3-spike.md)。

## 原生服务端未闭合项

**生产部署前必须读 [h3native/README.md](../h3native/README.md) 的 Status 一节。** 摘要：

1. `--upstream_addr` 上游代理模式当前不可用（CONNECT 交换后 QUICHE 的 event-loop socket 在非事件循环线程上打不开描述符，服务端 abort）。
2. 上游不可达会阻塞唯一的 event-loop 线程，拖垮整个服务端而非一条隧道。
3. 吞吐未测。同机实测 `h3frontend` 对 Hysteria2 BBR 模式约为三分之一（13.9 vs 41.1 MB/s），原生版的数字尚未取得——不要假定它等于其中任何一个。

## 服务器实测（2026-09-12，198.46.146.78）

同机、同 256 MB 文件、交替各 3 轮、取中位；`h3frontend` + native-h3 内核，HY2 为 `congestion.type: bbr` 且未配 bandwidth（确保非 Brutal）:

- HY2 BBR：**41.1 MB/s**
- h3frontend：**13.9 MB/s**（≈34%）
- loopback 直连参照：458 MB/s（说明差距是真实实现差异，不是链路瓶颈）

同一路径从外网客户端测会被客户端带宽压平（直连 3.92、HY2 2.96 MB/s），不可用于对比。

TCP REALITY 前端独立保留；需要该协议时须显式构建 `tcp-reality` 客户端。C++ 补丁应用检查不等于编译通过；本次实际验证记录见研究目录 ROOTFIX-RESULTS.md。

## 历史记录（截至 2026-08-18）

以下保留原有 TCP/TUI 开发记录，不代表本次改动已跑过这些测试或已发布。

## 已完成

- M1 服务端 REALITY 前端(frontend/): config/genkey/gencert/reality 接线/h2->h1 中继/状态端点,
  - 测试: 认证路径(临时证书 HMAC 验证 + h2 CONNECT 隧道回声)与未认证中继路径(拿到目标站证书)全部端到端通过;
  - 官方内核全链路 e2e: 官方 naive 客户端 -> 前端(tls 模式) -> 官方 naive 服务端, padding Variant1 协商成功, 4.88MB 传输字节一致.
- M2 客户端内核补丁(patches/): 001 BoringSSL REALITY 客户端, 002 net 接线, 003 蜘蛛模式, 004 构建注册;
  - 四个补丁全部 git apply --check 实测通过(boringssl 树 + naiveproxy src 树);
  - 参考实现: frontend/internal/realitytest(Go REALITY 测试客户端, 已与 xtls/reality 服务端互通验证).
- M3 Windows TUI 客户端(tui/): 档案管理/分享链接(naive+https 与 naivereal)/核心监管/入口(SOCKS5+HTTP)/统计/系统代理/日志;
  - 数据链路 e2e: TUI 入口 -> 官方内核 -> 官方服务端 -> 外网 通过.
- REALITY 生态互操作: realitytest 客户端与真实 Xray-core v26.3.27 VLESS+Reality 入站握手成功, 临时证书 HMAC 校验通过(线格式与参考生态完全兼容).
- M4 TUN(tui/internal/tun/): wintun+gVisor 已接入 TUI；数据包长度、TCP 生命周期、DNS/EDNS 和服务器排除路由已有回归测试；建卡与系统路由仍需在 Windows 管理员环境手工验收.
- M4 v2rayN: 实测 7.24.4 发行包与 core-bin 仓库, 内核路径确认 bin\naiveproxy\naive.exe, 替换指南定稿.
- M5 发布: 前端 Linux/Win 二进制 + TUI Win 二进制已产出并冒烟通过; 部署/构建/Windows/TUN 文档成稿;
  - CI: .github/workflows/go.yml(Go 测试、vet、Windows 官方内核入口 e2e) + build-kernel.yml(内核 Linux x64/arm64 + Windows x64, 复刻官方流水线).

## 已推送 GitHub(PR #1)

- 仓库: https://github.com/lipeiying032/naive-reality, 分支 feature/naivereal-source, PR #1 已创建.
- 本仓库模型: 仅含 Go 代码+补丁+CI; 内核源码由 CI 按 CHROMIUM_VERSION 从 klzgrad/naiveproxy 克隆后应用 patches/001-004 构建(上游仓库已 vendor 全部依赖, 无需 gclient).
- PR #1 已合并到 main; PR #2(ci: windows runner 用 bash shell 修复)已创建: https://github.com/lipeiying032/naive-reality/pull/2.
- CI 结果: go.yml 双平台测试(ubuntu+windows, 含 TUN 编译)与官方三件套 e2e 全部 success; Build Kernel: Linux 任务补丁应用全部通过、编译进行中; windows 任务首次因默认 shell 为 PowerShell 在补丁应用步骤失败, 已修复(workflow 加 defaults.run.shell: bash)并提交 PR #2.

## 已推送 GitHub(PR #3)

- Build Kernel 三平台(linux x64/arm64 + windows)编译+basic.sh 全部通过; 唯一失败为打包步骤 cp 上游缺失的 config.json.
- 修复(PR #3): 仓库根新增 config.json(含 reality 块样例)与 USAGE.txt; 打包步骤改从 $GITHUB_WORKSPACE 拷贝; windows 打包改用 naive.exe; 全部 7 个 actions/cache 步骤加 save-always(失败也保存 ccache/工具链, 重跑复用增量编译, 不再冷构建).
- ccache/sccache 上限 200M -> 1G.

## 当前验证重点

1. 本次修复 PR 的 Build Kernel 成功后，`reality-e2e` 会自动执行 REALITY 全链路验证；Linux arm64 构建已显式安装 qemu-user 来运行目标架构产物.
2. TUN 仍需 Windows 管理员环境手工验收(建卡、物理网关排除路由、DNS 和断开恢复).
3. 正式发布前继续进行长连接压测与运行日志脱敏审计.

## 部署前需要提供

- 服务器部署信息(目标站/SNI/shortId 策略)以便给出上线配置样例.
