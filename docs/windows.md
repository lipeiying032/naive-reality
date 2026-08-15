# Windows 使用说明

## 组件

- naive.exe(或自定义名): 客户端内核(官方 naiveproxy + REALITY 补丁), 命令行与官方一致, 支持官方 config.json + 可选 reality 块
- naivereal-tui.exe: TUI 客户端(档案/统计/系统代理/TUN/分享链接)
- wintun.dll: TUN 模式所需(可选)

## 内核单独使用(v2rayN 风格)

```json
{
  "listen": "socks://127.0.0.1:1080",
  "proxy": "https://user:pass@host:443",
  "reality": { "server_name": "www.microsoft.com", "public_key": "...", "short_id": "..." }
}
```

`naive.exe config.json` 启动; 命令行标志与官方一致, 另有 --reality-server-name/--reality-public-key/--reality-short-id.

## TUI 客户端

- 配置: %APPDATA%/naivereal/profiles.json(内核运行配置 %APPDATA%/naivereal/core-config.json)
- 按键: c 连接 / d 断开 / tab 切页 / a 粘贴分享链接导入 / x 复制分享链接 / q 退出
- 分享链接: naive+https://(标准, 可导入 v2rayN) 与 naivereal://(含 REALITY 参数)
- 系统代理: 档案中 system_proxy.enabled=true 时连接自动开启并退出恢复
- TUN: 见 docs/tun.md

## v2rayN 内核替换

见 docs/v2rayN.md.