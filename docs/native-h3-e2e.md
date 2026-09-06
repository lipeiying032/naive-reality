# Native H3 e2e：QUIC 证书错误与初始化顺序

PR #17 的 [Build Kernel run 33972986336](https://github.com/lipeiying032/naive-reality/actions/runs/33972986336) 使用 `85dce9f86dd4d2cf3e02f0981504b62d6121871c`。Linux x64 artifact ZIP 的 SHA-256 为 `1f704d3f528193bcef4630a1b2ad8e451b9721d22b75c80bf4608b9f21364035`，内核为 naive 150.0.7871.63。

## 实测与根因

用该 artifact、当前 Go h3frontend 和原 e2e 参数可在回环地址复现 `ERR_QUIC_PROTOCOL_ERROR`。启用 Chromium `--log-net-log` 和服务端 `QUIC_GO_LOG_LEVEL=debug` 后：

- Chromium `CERT_VERIFY_PROC`/`CERT_VERIFIER_JOB`：`cert_status=0`，证书路径为 `TRUSTED_ANCHOR`，但 `is_issued_by_known_root=false`。测试 CA 已受本地信任。
- 客户端发送 QUIC CONNECTION_CLOSE：`CRYPTO_ERROR 0x12e`，CRYPTO frame `0x6`，TLS alert 46 (`certificate unknown`)，内部 QUIC 错误 199 (`QUIC_TLS_CERTIFICATE_UNKNOWN`)。
- Go 侧已处理 ClientHello 和 transport parameters，并安装 Handshake 密钥，随后收到上述客户端关闭帧。通用 `ERR_QUIC_PROTOCOL_ERROR` 不能排除 TLS 证书策略错误。

固定上游 commit 的源码解释了这条失败路径：

1. [naive_proxy_bin.cc](https://github.com/klzgrad/naiveproxy/blob/3ba967e2d36cc133a896e81a36257ad4c6ea20f4/src/net/tools/naive/naive_proxy_bin.cc#L273) 在 `builder.Build()` **之后**才设置 `origins_to_force_quic_on`。
2. [URLRequestContextBuilder::Build](https://github.com/klzgrad/naiveproxy/blob/3ba967e2d36cc133a896e81a36257ad4c6ea20f4/src/net/url_request/url_request_context_builder.cc#L550) 已构造网络会话；[QuicSessionPool 构造函数](https://github.com/klzgrad/naiveproxy/blob/3ba967e2d36cc133a896e81a36257ad4c6ea20f4/src/net/quic/quic_session_pool.cc#L703) 按值复制 `QuicParams`，之后修改 context 参数不会更新该副本。
3. [会话池创建 proof verifier](https://github.com/klzgrad/naiveproxy/blob/3ba967e2d36cc133a896e81a36257ad4c6ea20f4/src/net/quic/quic_session_pool.cc#L2507) 时，从这份副本提取允许本地信任根的代理域名，实际列表为空。
4. [ProofVerifierChromium::DoVerifyCertComplete](https://github.com/klzgrad/naiveproxy/blob/3ba967e2d36cc133a896e81a36257ad4c6ea20f4/src/net/quic/crypto/proof_verifier_chromium.cc#L428) 在正常证书校验成功后追加“已知根”检查，返回 `ERR_QUIC_CERT_ROOT_NOT_KNOWN`，最终表现为上述 TLS/QUIC 关闭。

因此本次无需修改 Go TLS、ALPN、QUIC 帧或传输参数。

## 修复与回归验证

`007-native-h3-context.patch` 通过已有的 `set_quic_context` 接口，在 `Build()` 之前设置原有 QUIC 代理选项。它恢复 Chromium 对这些代理域名的本地信任根策略，仍要求证书链有效、域名匹配；没有关闭证书校验。补丁限制在 naive 应用入口，QUICHE、BoringSSL 和 Go 服务端源码保持上游实现。

`tests/native-h3-e2e.sh` 验证 SOCKS → Chromium QUIC → H3 → naive CONNECT 的 payload 和 TCP HTTPS 网站，并通过 Chromium NetLog 检查错误域名 (`-200`) 和移除测试 CA 后 (`-202`) 的拒绝结果。失败日志保留具体 QUIC 关闭原因；退出时删除 CA 并执行 `update-ca-certificates --fresh`，清除 CA 及其索引链接。

构建修复后的内核后，在获准安装临时测试 CA 的隔离 Linux runner 中执行：

```sh
GITHUB_ACTIONS=true NAIVEREAL_ALLOW_CI_TRUST_INSTALL=1 \
  bash tests/native-h3-e2e.sh /path/to/kernel
```

测试脚本存在不等于验证通过；编译和互通结果以对应修复提交的 CI 日志为准。
