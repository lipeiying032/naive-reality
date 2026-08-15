# 协议说明(已核实事实)

## naive 协议(klzgrad/naiveproxy, BSD-3)

- 载体: HTTP/2(ALPN h2)CONNECT 隧道; 官方服务端只做 h1 明文 HTTP 代理, TLS/h2 终结一律由前置完成(官方 README 架构: Frontend -> Naive server).
- REALITY 前置兼容回退: xtls/reality fork 当前不下发 ALPN, 前置按客户端 preface 分流, 也接受 HTTP/1.1 CONNECT 并转发给官方服务端.
- 客户端 CONNECT 头(小写): proxy-authorization: Basic base64(user:pass); padding: 16-32 个 HPACK 不可索引字符; padding-type-request: "1, 0"; 可选 fastopen: 1.
- 服务端 200 响应头: padding: 30-62 个随机不可索引字符 + padding-type-reply: 选定类型; 认证失败拒绝.
- 隧道内 padding 帧(h2 DATA 载荷内部, 双向独立, 每方向前 8 帧, 之后裸传):
  uint16BE payload_len | uint8 padding_len(<=255) | payload | 零填充
  - 客户端->服务端: buf_len<100 时 padding=rand[255-buf_len,255], 否则 rand[0,255]
  - 服务端->客户端: padding=rand[0,255]
  - 服务端写侧: 剩余负载 (200,1024) 时按 rand[100,200] 切块
- 参考源码: src/net/tools/naive/ 下 naive_protocol.h, naive_padding_framer.cc, naive_padding_socket.cc, naive_proxy_delegate.cc, http_proxy_server_socket.cc, padding_utils.cc

## REALITY(XTLS 现行设计)

- 服务端静态 X25519 密钥对; 客户端配置: 公钥(32B base64url), shortId(hex<=16, 右零填充 8B), SNI.
- ClientHello SessionID(32B) = AES-256-GCM(Key, nonce=Random[20:32], 明文=版本(3B)|reserved(1B)|unix秒BE32|shortId(8B), AAD=整个 ClientHello 且 session-id 位置置零)
- Key = HKDF-SHA256(ikm=X25519(客户端临时私钥, 服务端静态公钥), salt=Random[:20], info="REALITY")
- 服务端判定: 解密校验 shortId 后:
  - 通过: TLS1.3 + 每连接新生成 ed25519 临时自签证书(签名字段=HMAC-SHA512(Key, 公钥), CertificateVerify 用真实私钥), 禁 tickets; 设计期望 ALPN=h2, 但当前 fork 未在 EncryptedExtensions 下发 ALPN
  - 未通过: ClientHello 原样中继到 target, 全程字节转发
- 客户端证书校验: 叶子 ed25519 且 HMAC-SHA512(Key, 公钥)==证书签名 -> 真服务端; 否则正常 CA 链 -> 真目标站 -> 蜘蛛模式; 否则断开
- 注意: session-id 认证仅用于筛选探测流量, 真正的用户认证是 naive Basic 认证
- 参考: XTLS/REALITY(服务端 fork), XTLS/Xray-core transport/internet/reality/reality.go(UClient)