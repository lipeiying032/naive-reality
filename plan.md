# plan: 将 naiveproxy HTTP/3 的 BBR 吞吐对齐 Hysteria2 BBR 模式

状态：研究/待审阅。未修改任何源代码。
目标分支建议：`agent/h3-hysteria2-bbr`（后续实施阶段再创建，不要在本计划分支上改源码）。

---

## 0. 一句话结论

不能直接把 Hysteria2 的 Go `quic-go` 代码“塞进” naiveproxy 的 Chromium C++ QUIC 栈；可行路线是：

1. 服务端：新写一个基于 **apernet/quic-go（Hysteria2 使用的 fork）** 的标准 HTTP/3 CONNECT frontend，
   移植 Hysteria2 的 BBR/pacer/窗口/GSO 参数。
2. 客户端：给 naiveproxy 的 Chromium quiche 增加与 Hysteria2 对齐的 QUIC 参数/BBR profile patch。
3. 用同机同链路 A/B 压测验收，目标是 BBR 模式下 goodput 对齐，而不是替换成 Hysteria2 私有协议。

本次明确不集成 REALITY：
- 不改 BoringSSL/REALITY patch。
- 不在 H3 ALPN、H3 frontend、H3 测试中接 `--reality-*` 或 REALITY 证书逻辑。
- H3 先只做标准 TLS + 标准 HTTP/3 CONNECT。

---

## 1. 已完成的研究动作

只做了只读研究，没有改 repo 源码：

- 拉取原版 naiveproxy：`/tmp/naive-src`
  - pinned upstream commit：`3ba967e2d36cc133a896e81a36257ad4c6ea20f4`
  - Chromium：`150.0.7871.63`
- 拉取 Hysteria2：`/tmp/hysteria2`
  - 当前 HEAD：`14e9fff`
  - `core/go.mod` 依赖：
    `github.com/apernet/quic-go v0.61.1-0.20260806010916-184d081eef3e`
- 拉取 Hysteria2 使用的 quic-go fork：`/tmp/apernet-quic-go`
  - 目标提交：`184d081eef3e9edd5cb7c0ddf2460c91f2e6adb1`
  - 关键 fork commits：
    - `b494d6d feat: hysteria fork modifications`
    - `0ad2f22 feat: chrome parrot`
    - `bc12a60 feat: expose Conn.InitialPacketSize for congestion controller seeding`
    - `cdc8dd7 feat: add Transport.DisableGSO`
    - `184d081 fix: chrome parrot packet number length, Initial ACK and coalescing`

研究文件都在 `/tmp`，不进入仓库；实施阶段只复制允许复用的算法文件和必要依赖声明。

---

## 2. 关键发现：naiveproxy/Chromium 与 Hysteria2 的 QUIC 差异

### 2.1 协议差异（决定不能整体替换）

- naiveproxy 的 `quic://` 客户端发送的是 **标准 HTTP/3 CONNECT**（`QuicProxyClientSocket` 中
  `request_.method = "CONNECT"`）。
- Hysteria2 虽然也跑在 QUIC/HTTP/3 之上，但认证和代理请求是私有协议：
  - `POST /auth` + `Hysteria-Auth` / `Hysteria-CC-RX`
  - TCP 请求使用自定义 stream frame `0x401`
  - UDP 使用自定义 datagram 封装
- 因此如果直接把 Hysteria2 服务端当作 naiveproxy HTTP/3 服务端，协议不通。
  正确做法是：**只复用它的 QUIC 性能实现，不复用它的私有代理协议**。

### 2.2 拥塞控制

| 项目 | naiveproxy / Chromium quiche | Hysteria2 / apernet quic-go |
|---|---|---|
| 默认 CC | `quic_default_to_bbr = true`，即 BBRv1；BBRv2 默认 false | `congestion.type` 默认 `bbr` |
| BBRv3 | `bbr3_sender.cc` 存在、Create 支持，但默认无开关接线 | 无（Hysteria 用自己的 BBR 移植） |
| BBR profile | 无 profile 概念，只有若干 `quic_bbr_*` flags | `conservative` / `standard` / `aggressive` |
| 关键标准参数 | highGain 2.885，cwndGain flag 默认 2.0，startup RTT 3 | standard 基本一致，但 profile 化 |
| aggressive 参数 | 无 | highGain 3.0，highCwndGain 2.25，cwndGain 2.5，startupRTT 4 |
| conservative 参数 | 无 | highGain 2.25，highCwndGain 1.75，cwndGain 1.75，startupRTT 2，含 overshoot 检测 |
| pacing | 10 个初始 burst + lumpy pacing（默认 2 包/0.25cwnd，>1.2Mbps 时） | 简单 token bucket，burst 10 包，max delay 4*MinPacingDelay |

参考源码：
- quiche BBRv1：`src/net/third_party/quiche/src/quiche/quic/core/congestion_control/bbr_sender.cc`
- quiche flags：`quiche_feature_flags_list.h`、`quiche_protocol_flags_list.h`
- Hysteria BBR：`core/internal/congestion/bbr/bbr_sender.go`、`bandwidth.go`
- Hysteria pacer：`core/internal/congestion/common/pacer.go`

### 2.3 流控窗口

| 项目 | naiveproxy/Chromium | Hysteria2 |
|---|---|---|
| 初始 stream 窗口 | 6 MiB（`quic_context.cc`） | 8 MiB |
| 最大 stream 窗口 | quiche limit 16 MiB | 8 MiB（初始=最大） |
| 初始 session 窗口 | 15 MiB | 20 MiB |
| 最大 session 窗口 | quiche limit 24 MiB | 20 MiB（初始=最大） |
| 自增策略 | 窗口更新快于 2RTT 时翻倍，直到 limit | 初始直接给满 |

参考：
- `src/net/quic/quic_context.cc`：`kQuicSessionMaxRecvWindowSize = 15MB`、`kQuicStreamMaxRecvWindowSize = 6MB`
- `quic_constants.h`：stream limit 16MB，session limit 24MB
- `hysteria2/core/client/config.go`：`8MB / 20MB`
- `hysteria2/core/server/config.go`：`8MB / 20MB`

### 2.4 UDP socket buffer 与发送路径

| 项目 | naiveproxy/Chromium | Hysteria2 |
|---|---|---|
| 内核 UDP rcvbuf | `kQuicSocketReceiveBufferSize = 1 MiB` | 期望 8 MiB，必要时 `SO_RCVBUFFORCE` |
| 内核 UDP sndbuf | 未显式设到 8 MiB | 期望 8 MiB，必要时 `SO_SNDBUFFORCE` |
| GSO/UDP_SEGMENT | `QuicChromiumPacketWriter::IsBatchMode()` 返回 false，当前无 batch/GSO 发送 | fork 默认启用 GSO，`MaxLargePacketBufferSize = 20KB` |
| recv optimization | `enable_socket_recv_optimization` 默认 false | 独立 reader + 8MiB buffer |
| 初始包大小 | `kDefaultMaxPacketSize = 1250`，随后 PMTUD | `InitialPacketSize = 1280`，随后 PMTUD |

参考：
- `src/net/quic/quic_context.h`（1MiB socket buffer）
- `src/net/quic/quic_chromium_packet_writer.cc`（`IsBatchMode=false`、`GetNextWriteLocation` 为空、`Flush` 空实现）
- `apernet-quic-go/internal/protocol/params.go`（1280 / 8MiB）
- `apernet-quic-go/sys_conn_buffers.go`、`sys_conn_buffers_write.go`

### 2.5 其他 fork 性能相关改动

apernet/quic-go fork 还包含：
- `frame_sorter` 树化，降低乱序/重排开销。
- `streamframe_interval.go`，优化 stream frame 组装。
- 自定义 CC adapter，允许运行时替换 CC（`Conn.SetCongestionControl`）。
- datagram 处理优化、PMTUD 在非 raw conn 上工作、GSO error 修复。
- Chrome parrot（对性能影响小，主要是指纹，不在本计划中强依赖）。

这些改动无法原样落入 Chromium，但 GSO、CC adapter 思想、pacer 参数是重点移植对象。

---

## 3. 推荐技术路线

### 3.1 总体架构（H3 无 REALITY）

```
naiveproxy 客户端 (Chromium quiche)
  --proxy=quic://vps:443
        |
        | IETF QUIC / HTTP/3 CONNECT（标准协议）
        v
h3frontend (Go, apernet/quic-go fork + Hysteria2 BBR)
  - 标准 TLS 证书
  - 标准 HTTP/3 CONNECT handler
  - quic.Config: 8MB/20MB 窗口、GSO、PMTUD
  - per-conn BBR profile: standard/aggressive/conservative
        |
        | HTTP/1.1 CONNECT（保留 naive padding 头）
        v
官方 naive server (127.0.0.1:18080, TCP)
```

说明：
- 下载方向的发送端是 `h3frontend`，所以服务端必须换成 apernet/quic-go BBR 才可能追平 HY2。
- 上传方向的发送端是 naiveproxy 客户端，必须同步调 Chromium quiche 的 BBR 参数。
- 客户端仍使用原版 naiveproxy HTTP/3 语义，不做 Hysteria2 私有认证协议。

### 3.2 不推荐路线

- 直接替换 Chromium quic/ 为 quic-go：语言/接口/构建体系完全不同，风险和工程量不可控。
- 让 naiveproxy 直接连 Hysteria2 服务端：私有协议不兼容，除非同时改 naiveproxy 客户端协议，这会偏离“原版 naiveproxy http3”。
- 只改客户端 CC：下载吞吐主要由服务端 CC 决定，达不到目标。

---

## 4. 分阶段实施计划

### Phase 0：基线测量（先不改代码）

环境：
- 同一 VPS、同一线路、同一端口/证书。
- HY2 服务端配置必须明确禁用 Brutal，只走 BBR：
  ```yaml
  congestion:
    type: bbr
    bbrProfile: standard
  # 不配置 bandwidth.up/down，或服务器返回 CC-RX auto，确保不是 Brutal
  ```
- naiveproxy 基线：用户当前的 `quic://` HTTP3 服务端 + 当前 naive 客户端。

测量：
- 下载 2GB/10GB 文件：median goodput、RTT、丢包、重传、CPU。
- 上传同样大小。
- 单流和多流各测 5 次。
- 同时抓 NetLog/qlog（可临时加 debug flag）和 tcpdump。

验收基线输出：
- `naive-h3-bbr-baseline.json`
- `hysteria2-bbr-baseline.json`

### Phase 1：新 Go h3frontend（重点，服务端）

新增目录（建议，不在本计划阶段创建）：
```text
h3frontend/
  go.mod
  go.sum
  main.go
  config.go
  relay.go
  internal/congestion/
    utils.go
    common/pacer.go
    bbr/*.go
  LICENSE.apernet-quic-go
  LICENSE.hysteria2
```

实现要点：
1. 依赖：
   - `github.com/apernet/quic-go`，锁定 `v0.61.1-0.20260806010916-184d081eef3e`
   - 不依赖 `github.com/apernet/hysteria/core/v2`（其 congestion 是 internal 包，不能 import）。
   - 从 Hysteria2 `core/internal/congestion` 复制 BBR/pacer 源码，保留 MIT 版权头。
2. 配置：
   ```toml
   listen = "0.0.0.0:443"

   [tls]
   cert = "/etc/naivereal/fullchain.pem"
   key = "/etc/naivereal/privkey.pem"

   [quic]
   initStreamReceiveWindow = 8388608
   maxStreamReceiveWindow = 8388608
   initConnReceiveWindow = 20971520
   maxConnReceiveWindow = 20971520
   maxIdleTimeout = "30s"
   disablePathMTUDiscovery = false
   disableGSO = false

   [congestion]
   type = "bbr"
   bbrProfile = "standard"

   [upstream]
   addr = "127.0.0.1:18080"
   ```
   **没有任何 `reality` 配置项。**
3. QUIC listener：
   - 使用 `quic.Transport{Conn: udpConn, DisableGSO: false}`。
   - `Accept` 循环拿到 `*quic.Conn` 后：
     ```go
     conn.SetCongestionControl(bbr.NewBbrSender(...))
     http3Server.ServeQUICConn(conn)
     ```
   - `NewBbrSender` 的 packet size seed 使用 `conn.InitialPacketSize()`，避免 PMTUD 后 seed 不一致。
4. HTTP/3 CONNECT handler：
   - 复用现有 `frontend/relay.go` 的 `buildH1Connect` / `copyResponseHeaders` 思想。
   - 通过 `http3.HTTPStreamer.HTTPStream()` hijack QUIC stream。
   - 先返回 `200` 和 naive padding 响应头，再双向 `io.Copy` 到官方 naive server。
   - 请求头保留 `proxy-authorization`、`padding`、`padding-type-request`。
   - 非 CONNECT 请求返回 404，保证可伪装成普通 H3 站点。
5. 安全边界：
   - 不引入 `xtls/reality`。
   - 不读取 `reality` 配置。
   - 不修改 `patches/001-boringssl-reality.patch`。

### Phase 2：naiveproxy 客户端 quiche 参数对齐（C++ patch）

新增 patch 文件，例如：
```text
patches/010-quic-hysteria2-bbr-tuning.patch
patches/README.md（补充说明）
```

第一版只做低风险参数：
1. UDP socket buffer：`1 MiB -> 8 MiB`
   - `src/net/quic/quic_context.h`
   - 部署时同步设置 `net.core.rmem_max/wmem_max >= 8388608`。
2. 流控窗口：
   - `src/net/quic/quic_context.cc`
   - stream：`6 MiB -> 8 MiB`
   - session：`15 MiB -> 20 MiB`
   - 与 Hysteria2 初始窗口一致。
3. 对 `quic://` 连接启用 socket recv optimization：
   - `QuicParams.enable_socket_recv_optimization = true`。
4. 新增 naive 配置项（默认不改变非 QUIC 行为）：
   ```json
   {
     "proxy": "quic://user:pass@vps:443",
     "quic": {
       "socket-receive-buffer": 8388608,
       "initial-stream-receive-window": 8388608,
       "initial-session-receive-window": 20971520,
       "congestion": "bbr",
       "bbr-profile": "standard"
     }
   }
   ```
   对应命令行开关：`--quic-bbr-profile=...` 等。

### Phase 3：BBR profile 与 pacing 移植

目标是让两端 BBR 行为可配置：

1. 在 quiche `bbr_sender.cc` 增加 profile 化参数：
   - `standard`：保持现有 Chromium BBRv1 默认。
   - `aggressive`：移植 Hysteria2 aggressive：
     - highGain `3.0`
     - highCwndGain `2.25`
     - cwndGainConstant `2.5`
     - numStartupRtts `4`
     - bytesLostMultiplier `2`
     - enable ack aggregation startup
   - `conservative`：移植 Hysteria2 conservative：
     - highGain `2.25`
     - highCwndGain `1.75`
     - cwndGainConstant `1.75`
     - numStartupRtts `2`
     - drainToTarget、detectOvershooting、reduceExtraAckedOnBandwidthIncrease
2. pacing 对齐：
   - 对比 quiche `PacingSender` 和 Hysteria `common/pacer.go`。
   - 对 `bbr` profile 关闭 lumpy pacing 或把 burst 对齐为 HY2 的 `10 packets / 4*MinPacingDelay`。
   - 用独立 flag/tag 控制，默认只在 `quic-bbr-profile` 显式设置时启用。
3. 增加 BBRv3 实验开关（可选）：
   - quiche 已有 `bbr3_sender.cc`，仅缺少默认选择接线。
   - 若 BBRv1 profile 移植后仍追不上 HY2，增加 `quic-congestion=bbr3` 实验。

### Phase 4：GSO/batch 发送（视压测差距决定）

仅在 Phase 1-3 后仍有明显差距时实施：

- 参照 apernet/quic-go 的 `sys_conn_oob.go` + `send_conn.go`：
  - Linux `UDP_SEGMENT`
  - 20KB GSO batch buffer
  - ECN/控制消息保留
- 对应 Chromium 改造点：
  - `src/net/socket/udp_socket_posix.cc`
  - `src/net/quic/quic_chromium_packet_writer.cc`
  - `QuicPacketWriter` batch 接口（当前 `IsBatchMode=false`）
- 风险高，必须单独分支和回归测试。

---

## 5. 测试计划

### 5.1 本地/单元测试

- Go：
  - `cd h3frontend && go test ./...`
  - BBR profile 解析/参数测试
  - pacer 单元测试（从 Hysteria2 对应测试裁剪）
  - H3 CONNECT relay 测试（本地 mock upstream）
- C++ patch：
  - `git apply --check` 对 pinned upstream commit
  - `git diff --check`
  - 只对 `quic://` 配置路径做参数注入；`https://`/`http://` 行为不得改变。

### 5.2 本地集成测试

- 本地运行：
  - `h3frontend`（标准证书）
  - 官方 naive server
  - naive 客户端 `--proxy=quic://127.0.0.1:443`
  - `curl --socks5-hostname 127.0.0.1:1080 https://api.github.com/zen`
- 断言：
  - 返回 `200`
  - 无 REALITY 参数/日志
  - TLS ALPN 只有 `h3`

### 5.3 远程 Action 构建（后续实施阶段）

- 客户端：使用 `build-kernel.yml` 的定向 target 只构建 Linux x64（不跑 Windows/ARM64/reality-e2e）。
- h3frontend：扩展现有 `go.yml` 或新增 workflow，只跑 `h3frontend` 的 `go test` 和 Linux 二进制构建。
- 本计划阶段不触发任何构建。

### 5.4 VPS 实测

1. 上传并部署：
   - 官方 naive server：`127.0.0.1:18080`
   - `h3frontend`：`0.0.0.0:443`
2. 客户端使用 patched naive Linux x64。
3. 同一 VPS 上运行 HY2 BBR 作为对照组：
   ```yaml
   congestion:
     type: bbr
     bbrProfile: standard
   ```
4. 下载/上传 2GB 测试文件，各 5 轮，取 median。
5. 指标：
   - goodput MiB/s
   - RTT/minRTT
   - 丢包率、重传率
   - CWND/pacing rate（加 debug 输出）
6. 验收目标：
   - 首轮目标：naive H3 BBR goodput >= HY2 BBR 的 90%
   - 最终目标：>= 95%，且 RTT/丢包不显著劣化
   - 若达不到，禁止用 Brutal 冒充 BBR；回到 Phase 3/4 继续定位。
7. 测试完成后删除 VPS 上的 h3frontend、naive 进程、证书和测试文件。

---

## 6. 明确不做的事

- 不在 H3 ALPN 中实现 REALITY。
- 不改 `patches/001-boringssl-reality.patch`。
- 不引入 Hysteria2 私有认证协议替代 naiveproxy HTTP/3 CONNECT。
- 不替换 Chromium QUIC 为 Go quic-go。
- 不使用 Brutal 结果冒充 BBR 结果。
- 不修改 release/REALITY e2e 工作流，除非后续单独评审。

---

## 7. 风险与回滚

| 风险 | 影响 | 应对 |
|---|---|---|
| 服务端才是下载发送端，客户端调参不够 | 下载仍慢 | 以 Go h3frontend 为主要改动 |
| quiche BBR profile 参数改动影响所有 QUIC | 非 quic:// 行为变化 | 所有新参数仅对 `quic://` 或显式配置生效 |
| 8MiB socket buffer 受 `rmem_max` 限制 | 达不到 HY2 水平 | 部署时同步 sysctl；必要时研究 `SO_RCVBUFFORCE` |
| GSO/batch 改造复杂 | 构建或兼容性风险 | 单独 Phase 4，默认关闭，压测驱动 |
| Hysteria2 congestion 是 internal 包 | 不能直接 import | 复制文件并保留 MIT license/版权头 |
| 测试受 VPS 线路波动影响 | 结论不准 | 同机同链路多轮 median + 对照组轮换 |
| H3 frontend 与 REALITY frontend 混淆 | 功能耦合 | 独立 `h3frontend/` 模块，配置中禁止 reality 字段 |

---

## 8. 实施前需要你确认的点

1. 现有 naiveproxy HTTP/3 服务端是什么软件？
   - 如果是 Caddy/其他 quic-go 服务端，建议直接替换为计划中的 `h3frontend`。
   - 如果是 Chromium 自带服务端（非本仓库产物），需要另给仓库/版本。
2. 目标线路的带宽和 RTT 大致是多少？
   - 用于计算 BDP 和验收窗口。
3. BBR profile 默认用哪个？
   - 建议先 `standard`，和 HY2 BBR 标准模式对齐；如 HY2 实际用 `aggressive`，则两边都设为 `aggressive`。
4. 验收阈值是否接受：
   - 首轮 >= 90%，最终 >= 95% HY2 BBR goodput？
5. VPS 测试机是否仍用 `198.46.146.78`，还是给新的机器？

---

## 9. 后续执行顺序（批准后）

1. 创建实施分支 `agent/h3-hysteria2-bbr`（基于 main）。
2. 按 Phase 1-3 实现，保持 `[skip ci]` 提交。
3. 本地：
   - `git diff --check`
   - `git apply --check` 所有 patch
   - `cd h3frontend && go test ./... && go build`
   - 本地 H3 CONNECT 集成测试
4. 远程推送分支。
5. 定向 GitHub Actions：
   - 客户端：Linux x64 定向构建
   - h3frontend：go workflow 定向构建
6. VPS 部署、压测、抓包分析、清理。
7. 带着 benchmark 数据开 PR。
8. 若达标且用户确认，再合并；未达标继续 Phase 4 或回滚。

---

## 附：关键源码索引

### naiveproxy / Chromium
- `src/net/quic/quic_context.cc`
- `src/net/quic/quic_context.h`
- `src/net/quic/quic_session_pool.cc`
- `src/net/quic/quic_chromium_packet_writer.cc`
- `src/net/third_party/quiche/src/quiche/quic/core/quic_constants.h`
- `src/net/third_party/quiche/src/quiche/quic/core/quic_config.cc`
- `src/net/third_party/quiche/src/quiche/quic/core/quic_connection.cc`
- `src/net/third_party/quiche/src/quiche/quic/core/quic_sent_packet_manager.cc`
- `src/net/third_party/quiche/src/quiche/quic/core/congestion_control/bbr_sender.cc`
- `src/net/third_party/quiche/src/quiche/quic/core/congestion_control/bbr3_sender.cc`
- `src/net/third_party/quiche/src/quiche/quic/core/congestion_control/pacing_sender.cc`
- `src/net/third_party/quiche/src/quiche/common/quiche_feature_flags_list.h`
- `src/net/third_party/quiche/src/quiche/common/quiche_protocol_flags_list.h`
- `src/net/quic/quic_proxy_client_socket.cc`

### Hysteria2 / apernet quic-go
- `core/client/config.go`
- `core/client/client.go`
- `core/server/config.go`
- `core/server/server.go`
- `core/internal/congestion/utils.go`
- `core/internal/congestion/bbr/bbr_sender.go`
- `core/internal/congestion/bbr/bandwidth.go`
- `core/internal/congestion/common/pacer.go`
- `core/internal/congestion/brutal/brutal.go`（仅参考，BBR 验收不使用 Brutal）
- `internal/protocol/params.go`
- `internal/protocol/protocol.go`
- `sys_conn_buffers.go`
- `sys_conn_buffers_write.go`
- `sys_conn_oob.go`
- `connection.go`（`SetCongestionControl` / `InitialPacketSize`）
