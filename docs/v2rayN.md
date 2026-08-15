# v2rayN 内核替换指南(实测 v2rayN 7.24.4)

## 实测事实

- v2rayN 7.24.4 发行包自带核心: bin\xray\xray.exe, bin\sing_box\sing-box.exe, bin\mihomo\mihomo.exe.
- naiveproxy 核心不在默认包内: v2rayN 在添加 naive 节点时从 2dust/v2rayN-core-bin 仓库的
  v2rayN-windows-64-other-bins/bin/naiveproxy/naive.exe 按需下载, 存为
  `<v2rayN目录>\bin\naiveproxy\naive.exe`, 并以官方 naive config.json 格式驱动.

## 替换步骤

1. 先用 v2rayN 正常添加一次 naive 节点(触发其下载 naiveproxy 核心), 或手动创建目录;
2. 关闭 v2rayN;
3. 将本项目的补丁内核(保持文件名 naive.exe)覆盖 `bin\naiveproxy\naive.exe`;
4. 启动 v2rayN, 普通 naive 节点即走补丁内核(无 reality 块时行为与官方一致, 逐字节兼容).

## REALITY 节点(v2rayN 内使用)

v2rayN 的 naive 节点 UI 不含 REALITY 参数, 可用其"自定义配置"功能直接给出内核 config.json:

```json
{
  "listen": "socks://127.0.0.1:1080",
  "proxy": "https://user:pass@你的服务器:443",
  "reality": {
    "server_name": "www.microsoft.com",
    "public_key": "(genkey 输出的 Public key)",
    "short_id": "0123456789abcdef"
  }
}
```

或直接用本项目的 naivereal-tui.exe 管理(支持 naivereal:// 分享链接一键导入, REALITY 全参数).

## 分享链接互通

- v2rayN 的 naive 分享链接(标准格式 naive+https://user:pass@host:port)可直接导入我们的 TUI;
- 我们的 TUI 导出的非 REALITY 档案同样输出该标准格式, 可直接导入 v2rayN;
- 含 REALITY 参数的档案输出 naivereal:// 扩展格式(仅本套件解析).