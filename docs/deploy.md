# 服务端部署指南(Linux)

## 组件

- naivereal-frontend: REALITY TLS 前端(Go, 单文件, 静态编译)
- naive: 官方 naiveproxy 服务端二进制(无需改动)

## 安装

```sh
# 1. 上传二进制到服务器
#    naive              (官方 linux-x64 构建, 也可用本仓库 CI 产物)
#    naivereal-frontend (本仓库 frontend/ 交叉编译产物)
# 2. 生成 REALITY 密钥对(本地或服务器均可)
./naivereal-frontend genkey
# 3. 编辑 /etc/naivereal/frontend.toml(见 frontend/frontend.toml.example)
# 4. 安装并启动
sudo ./deploy/install.sh ./你的二进制目录
sudo systemctl enable --now naivereal naivereal-frontend
```

Docker: `docker build -t naivereal . && docker run -d --network host -v $PWD/frontend.toml:/etc/naivereal/frontend.toml naivereal`

## 目标站选择标准(REALITY 中继目标)

1. 国外网站, 支持 TLS 1.3 与 h2(用 curl 验证: `curl -sI --tlsv1.3 --http2 https://目标站 -o /dev/null -w "%{http_version}"`)
2. 主域名不做跳转(避免选择会 301 到 www 的主域)
3. IP 与你的服务器尽量近(延迟低, 更像真实流量)
4. 目标站能从服务器稳定访问(中继依赖此路径)

## 客户端配置对应关系

- server_name = 目标站 SNI(必须属于 server_names 列表)
- public_key = genkey 输出的 Public key
- short_id = short_ids 列表之一
- 用户名/密码 = naive 服务端 --listen 中的 user:pass

## 上线前自查清单

1. 直连探测: `curl -v --resolve <server_name>:443:<服务器IP> https://<server_name>/` 应看到目标站真实证书(流量被中继)
2. REALITY 客户端连接成功(握手日志无报错, h2 隧道可用)
3. 服务器可出站访问 target:443
4. 防火墙放行 443; naive 服务端仅监听 127.0.0.1
5. 日志无明文密码/shortId(本套件默认脱敏)

## 运维

- 状态: `curl "http://127.0.0.1:9090/stats?token=..."`(若配置了 status 端点)
- 重启: systemctl restart naivereal naivereal-frontend
- 密钥轮换: 重新 genkey -> 更新 frontend.toml 与所有客户端 public_key -> 重启
- 证书: REALITY 模式无证书管理; 若用 tls 模式(兼容官方 naive 客户端), 证书用你的真实域名证书