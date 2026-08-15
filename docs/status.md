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

## 已推送 GitHub(PR #1)

- 仓库: https://github.com/lipeiying032/naive-reality, 分支 feature/naivereal-source, PR #1 已创建.
- 本仓库模型: 仅含 Go 代码+补丁+CI; 内核源码由 CI 按 CHROMIUM_VERSION 从 klzgrad/naiveproxy 克隆后应用 patches/001-004 构建(上游仓库已 vendor 全部依赖, 无需 gclient).
- PR #1 已合并到 main; PR #2(ci: windows runner 用 bash shell 修复)已创建: https://github.com/lipeiying032/naive-reality/pull/2.
- CI 结果: go.yml 双平台测试(ubuntu+windows, 含 TUN 编译)与官方三件套 e2e 全部 success; Build Kernel: Linux 任务补丁应用全部通过、编译进行中; windows 任务首次因默认 shell 为 PowerShell 在补丁应用步骤失败, 已修复(workflow 加 defaults.run.shell: bash)并提交 PR #2.

## 已推送 GitHub(PR #3)

- Build Kernel 三平台(linux x64/arm64 + windows)编译+basic.sh 全部通过; 唯一失败为打包步骤 cp 上游缺失的 config.json.
- 修复(PR #3): 仓库根新增 config.json(含 reality 块样例)与 USAGE.txt; 打包步骤改从 $GITHUB_WORKSPACE 拷贝; windows 打包改用 naive.exe; 全部 7 个 actions/cache 步骤加 save-always(失败也保存 ccache/工具链, 重跑复用增量编译, 不再冷构建).
- ccache/sccache 上限 200M -> 1G.

## 待办

1. 等待 Build Kernel 重跑(PR #3): 成功后 reality-e2e 自动执行 REALITY 全链路验证.
2. TUN 管理员环境手工验收(建卡/路由/DNS).
3. 压测/脱敏审计后正式发布.

## 需要用户提供

- GitHub 仓库地址(用于 CI; 本仓库为 naiveproxy fork + Go 子模块 + patches/).
- 服务器部署信息(目标站/SNI/shortId 策略)以便给出上线配置样例.