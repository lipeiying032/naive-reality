# TUN 模式(Windows TUI 客户端)

## 原理

- wintun 虚拟网卡 + gVisor 用户态 TCP/IP 栈
- TCP 连接逐条经本地 SOCKS5 转发进内核隧道(统计可见)
- 路由: 分裂默认路由 0.0.0.0/1 + 128.0.0.0/1 指向 TUN; 服务器 IP 经物理网关排除(防回环)
- DNS: TUN 网段 fake-DNS 应答, 经 DoH(默认 1.1.1.1/8.8.8.8, 可配)走 TCP 隧道

## 限制(v1)

- 仅转发 TCP + DNS; 其余 UDP 无响应(文档明确)
- 需要管理员权限(创建虚拟网卡与改路由)
- wintun.dll 随包分发(附其 LICENSE 文本); 也可自备放于程序目录

## 使用

1. 以管理员运行 naivereal-tui.exe
2. 档案配置中开启 tun: 设置 gateway/subnet/doh/exclude_ip
3. 连接后自动建卡与设路由; 断开自动恢复

## 故障排查

- 建卡失败: 确认管理员权限与 wintun.dll 存在
- 能连但 DNS 失败: 检查 DoH 端点可达性(经隧道)
- 服务器失联: 客户端会自动解析服务器 IPv4 并加入排除路由；若服务器有多个或动态地址，可再通过 exclude_ip 显式补充
