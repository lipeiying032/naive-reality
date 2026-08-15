# 构建指南

## 网络注意(本开发环境)

- Go: $env:GOPROXY = "https://goproxy.cn,direct"; $env:GOSUMDB = "off"
- git: git config --global http.sslBackend openssl (规避 schannel 中断)

## frontend(Go, 本机可构建)

```powershell
cd frontend
go build -o naivereal-frontend.exe .
go test ./...
```

Linux 交叉编译: $env:GOOS="linux"; $env:GOARCH="amd64"; go build -o naivereal-frontend .

## 客户端内核(C++, CI 构建)

与官方 naiveproxy 完全相同的构建链(depot_tools + gn/ninja, Linux 与 Windows),
复刻官方 .github/workflows/build.yml. 本机无 MSVC/depot_tools, 大构建一律走 CI;
本地只做补丁开发(见 patches/). Chromium 全量树 + 构建约需 30-50GB 磁盘.

## 发布产物

- Linux: naivereal-frontend(Go 静态) + naive(官方 Linux 构建) + systemd unit
- Windows: naive.exe(补丁内核) + naivereal-tui.exe + wintun.dll + 文档