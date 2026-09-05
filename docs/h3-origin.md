# H3：自有域名与标准 TLS

H3 默认使用 `mode = "origin"`：客户端沿用上游 Chromium QUICHE/BoringSSL，服务端以自有证书完成标准 TLS。网站访问和代理 CONNECT 使用同一个 QUIC/H3 端点，认证在加密后的每个 CONNECT 请求内进行。

旧的 `mode = "reality"`、QUIC Initial 预检、第三方 target 中继、借用证书及自定义 HMAC proof 已移除。旧模式不会自动回退成普通 TLS；启动会返回迁移提示。此变更放弃“无需自有域名、借用第三方网站身份”的前提，不承诺流量与任意第三方网站完全一致。

## 组件与配置

```text
原生 Chromium 客户端 ── 标准 QUIC/TLS ── 同一个 Go H3 端点
                                           ├─ GET/HEAD：自有网站
                                           └─ 每请求 Basic 认证的 CONNECT
                                                └─ 本地 naive HTTP 服务端
```

服务端不需要改为 Chromium。上游 naive 本身只提供本地 HTTP CONNECT，客户端复用 Chrome 网络栈；可对外提供正常网站的成熟服务端也可以使用 Go。本项目继续使用 apernet/quic-go 和 Go TLS，所有访问共用这套服务端实现。

使用 [origin.toml.example](../h3frontend/origin.toml.example) 和 [客户端示例](../h3frontend/native-h3-client.json.example)。必须填写：

- 与客户端代理 URL 域名匹配的证书和私钥；生产客户端应使用系统信任的证书。
- `origin.web_root`：公开网站目录，只服务 GET/HEAD；符号链接不能越出该目录。
- `origin.username/password`：与本地 naive 上游使用相同凭据；每个 CONNECT 独立校验。
- `upstream.addr`：本地 naive HTTP CONNECT 服务端。

`origin.tcp_listen` 可选。配置后，同一程序以同一证书提供 TCP HTTPS 网站，并通过 Alt-Svc 告知浏览器 UDP H3 端口。TCP 入口只提供网站，代理 CONNECT 由 H3 承载；不配置时，可由另一个自有 HTTPS 服务提供网站发现入口。Alt-Svc 使用本地 UDP 监听端口；存在公网端口映射时，应由了解公网端口的 HTTPS 服务发布正确的 Alt-Svc。

检查配置和证书私钥配对，不启动监听器：

```sh
./naivereal-h3frontend check h3frontend.toml
```

`mode = "tls"` 仍保留为显式的 CONNECT 前端兼容选项，认证交给本地 naive 上游，普通请求返回 404；它不提供 `origin` 的网站/认证路由。不写 mode 时现在选择 `origin`，旧的省略 mode 的 TLS 配置应补上 `mode = "tls"` 或迁移到 origin。

## 从 QUIC REALITY 迁移

先准备自有域名、证书、私钥和网站目录。这些信息不能从旧 target 或 REALITY 静态密钥自动推导。迁移工具需要 Python 3.11+：

```sh
python3 scripts/migrate-h3-config.py \
  --server /path/to/old-h3frontend.toml \
  --client /path/to/old-config.json \
  --hostname proxy.example \
  --cert /path/to/fullchain.pem \
  --key /path/to/privkey.pem \
  --web-root /path/to/public-site \
  --out-dir /path/to/new-configs
```

输出目录必须不存在。工具生成 `h3frontend.toml`、`config.json` 和 `MIGRATION.txt`；在 POSIX 上配置文件权限为 0600，目录为 0700。原文件保持原样。工具保留本地监听地址、上游地址和客户端凭据，保留客户端原公网端口（可用 `--port` 指定），替换代理域名，移除旧 REALITY、QUIC 调优、旧域名映射和禁用后量子选项。服务端旧 QUIC 调优不复制。

需要 TCP 网站入口时，显式添加 `--tcp-listen 0.0.0.0:443`。是否能绑定该地址取决于部署环境；工具不探测、不启动、不改变已有服务。证书续期、DNS、信任链和上游凭据仍需运营者核对。配置检查仅校验本地语法、必要路径和私钥配对，不等于证书已受客户端信任。

客户端必须使用默认的 `native-h3` 内核或官方 naiveproxy，代理地址为 `quic://user:pass@自有域名:端口`。不要携带 REALITY 或旧自定义 QUIC 参数；默认内核会明确拒绝这些配置。TUI/v2rayN 中的旧档案不会自动转换，需导入新配置。

## 构建与兼容边界

默认构建 profile 为 `native-h3`：只应用 `005-native-h3-config.patch`。该补丁只拒绝不兼容配置，不修改 QUICHE、BoringSSL、Transport Parameters、Initial 打包、ACK、拥塞控制或 H3 编码。

```sh
python3 scripts/apply-kernel-patches.py /path/to/clean-pinned-upstream
# 仅在仍需要 TCP REALITY 时，使用另一干净 checkout：
python3 scripts/apply-kernel-patches.py /path/to/another-checkout --profile tcp-reality
```

工具验证 CI 固定的上游 commit、Chromium 版本、工作树干净状态和补丁应用结果。`native-h3` 的修改文件集合必须仅有 `src/net/tools/naive/naive_config.cc`。后续编译仍使用上游 `src/get-clang.sh` 和 `src/build.sh`（目标 `naive`），没有新增 C++ H3 服务端。

TCP REALITY 是独立的兼容 profile，应用 001–004 和 006；它拒绝 QUIC+REALITY。其安全特性不能套用本次 H3 的结论。010/011/012 不再参与任何构建，历史实现保留在 git 历史中。

## 验证与剩余边界

本地 Go 集成测试覆盖受信任证书、错误域名/不受信任证书、H3 网站和 CONNECT、每请求授权隔离、同一源 UDP socket 上的多连接隔离，以及 TCP HTTPS/Alt-Svc。迁移工具有不覆盖、凭据一致性和输入拒绝测试。

CI 增加构建后的客户端配置拒绝测试、仅访问回环地址的 Chromium → H3 → naive 全链路测试。全链路脚本只能在明确允许临时 CA 安装的隔离 GitHub Actions runner 上运行；不能把脚本存在当作测试已通过。实际本次执行结果见研究目录中的 ROOTFIX-RESULTS.md。

标准 TLS 修复的是认证边界与握手正确性。服务器仍可被识别为其实际 Go QUIC 栈；连接持续时间、吞吐、HTTP CONNECT 与普通网页用途、naiveproxy 自身的代理参数也可能形成统计差异。服务器 0-RTT 仍关闭，证书从文件在启动时加载、尚无自动续期/热加载。这些限制没有被描述为“绝对不可识别”。
