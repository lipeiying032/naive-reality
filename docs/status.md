# 项目状态

更新时间: 本会话最后一次自动续跑.

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
- M4 TUN(tui/internal/tun/): wintun+gVisor 完整实现编译通过并接入 TUI(gvisor 钉 2026-01 伪版本, 最新伪版本的代理 zip 损坏已规避); DNS 编解码+DoH 测试通过; 运行时验收需管理员环境.
- M4 v2rayN: 实测 7.24.4 发行包与 core-bin 仓库, 内核路径确认 bin\naiveproxy\naive.exe, 替换指南定稿.
- M5 发布: 前端 Linux/Win 二进制 + TUI Win 二进制已产出并冒烟通过; 部署/构建/Windows/TUN 文档成稿;
  - CI: .github/workflows/go.yml(Go 测试) + build-kernel.yml(内核 Linux x64/arm64 + Windows x64, 复刻官方流水线).

## 待办(依赖外部条件)

1. 推送 GitHub 后运行 build-kernel.yml 编译验证四个补丁(本机无 MSVC/depot_tools).
2. 编译通过后: REALITY 全链路 e2e(补丁内核 <-> 前端 <-> 官方服务端), Xray 服务端握手互操作验证.
3. TUN 编译修正 + 管理员环境手工验收(建卡/路由/DNS).
4. TUN 运行时手工验收(管理员权限: 建卡/路由/DNS/断开恢复).
5. 压测/脱敏审计后正式发布.

## 需要用户提供

- GitHub 仓库地址(用于 CI; 本仓库为 naiveproxy fork + Go 子模块 + patches/).
- 服务器部署信息(目标站/SNI/shortId 策略)以便给出上线配置样例.