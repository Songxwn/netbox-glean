# netbox-glean

`netbox-glean` 是一个用 Go 编写的硬件自动发现工具：通过 **SNMP**、**IPMI** 和 **Redfish** 采集网络设备与服务器的硬件配置，自动整理并写入 [NetBox](https://netboxlabs.com/)。

它自带一个轻量的 Web UI，可以：

- 管理采集目标（地址、协议、凭据、NetBox 归属覆盖）
- 一键采集预览，或采集并同步到 NetBox
- 批量采集所有启用的目标
- **批量扫描网段**：ICMP 探活后按协议探测，自动创建采集目标，可立即采集同步
- **独立的 IP 采集**：读取 SNMP 设备的 ARP/NDP 表，或扫描 IPv4 网段，单独写入 NetBox IPAM
- 查看每次采集的日志、硬件摘要、扫描结果和 NetBox 同步结果
- 在线配置 NetBox 连接、默认 Site / Device Role / Platform 和同步策略

[![CI](https://github.com/Songxwn/netbox-glean/actions/workflows/ci.yml/badge.svg)](https://github.com/Songxwn/netbox-glean/actions/workflows/ci.yml)
[![Release](https://github.com/Songxwn/netbox-glean/actions/workflows/release.yml/badge.svg)](https://github.com/Songxwn/netbox-glean/actions/workflows/release.yml)

## 功能

- **三种采集协议**
  - SNMP v1 / v2c / v3（MD5、SHA1、SHA224/256/384/512；DES、AES、AES192/256）
  - IPMI 2.0 over LAN / LANplus（纯 Go，无需安装 `ipmitool`）
  - Redfish（HTTPS + Basic Auth，读取 System / Chassis / Manager / Storage / Firmware Inventory）
- **自动写入 NetBox**
  - 厂商、Site、Device Role、Platform、Device Type、Device 自动查找或创建
  - 网卡接口同步（名称、类型、速率、MTU、MAC、启用状态）
  - **LLDP 自动连线**：读取 `LLDP-MIB` 邻居，自动在 NetBox 中创建 Interface 之间的 Cable
  - **批量扫描自动添加目标**：输入 IPv4 网段 + 协议 + 凭据，自动发现设备并创建采集目标
  - 硬件组件写入 Inventory Items（CPU、内存、硬盘、电源、风扇、BMC、固件等）
  - 可选设置管理地址为主 IP
  - 可选写入已存在的 `glean_*` 自定义字段
- **独立的 IP 采集**
  - 独立的 IP 采集目标、页面、API 与定时任务，与设备采集分离
  - 读取 SNMP 设备 ARP/NDP 表，或对 IPv4 网段做 ICMP 扫描
  - 按 MAC 匹配 NetBox 接口后写入 IPAM
  - 支持批量执行所有启用的 IP 采集目标
- **NetBox v1 / v2 Token 认证**
  - v1：`Authorization: Token <TOKEN>`
  - v2：`Authorization: Bearer nbt_<key>.<secret>`（NetBox 4.5+）
  - 支持 `auto` 自动识别，也可强制指定
- **单二进制、跨平台**
  - 前端资源通过 `go:embed` 打包进可执行文件
  - GitHub Actions 自动构建 Linux / Windows / macOS / FreeBSD 多架构发布包
- **任务模型**
  - 后台并发执行，限制并发数与单任务超时
  - 每个目标可开启定时任务，按分钟间隔自动采集并同步
  - 内存中保留任务历史与日志（重启后清空）

## 工作流程

```text
┌─────────────┐   SNMP / IPMI / Redfish   ┌──────────────┐
│  设备 / BMC  │ ◀──────────────────────── │ netbox-glean │
└─────────────┘                           └──────┬───────┘
                                                 │ REST API
                                                 ▼
                                          ┌──────────────┐
                                          │    NetBox    │
                                          └──────────────┘
```

1. 在 Web UI 中配置 NetBox URL、Token、默认 Site 等。
2. 添加采集目标并填写对应协议凭据；也可以使用「批量扫描」自动发现并添加目标。
3. 点击「采集」只预览结果；点击「同步」则采集后写入 NetBox。
4. IP 地址采集在独立的「IP 采集」页面配置，不影响设备采集任务。
5. 在「任务记录」中查看日志、扫描结果与同步明细。

## 快速开始

### 使用发布二进制

从 [Releases](https://github.com/Songxwn/netbox-glean/releases) 下载对应平台压缩包，解压后运行：

```bash
./netbox-glean --listen 0.0.0.0:8080 --data-dir ./data
```

Windows：

```powershell
.\netbox-glean.exe --listen 0.0.0.0:8080 --data-dir .\data
```

然后访问 <http://localhost:8080>。

### 使用 Docker

GitHub Actions 会把镜像推送到 GitHub Container Registry：推送 `main` 时更新 `latest`，推送 `v*` 标签时同时生成版本号标签。

```bash
docker run -d --name netbox-glean \
  -p 8080:8080 \
  -v glean-data:/data \
  -e NETBOX_GLEAN_WEB_USERNAME=admin \
  -e NETBOX_GLEAN_WEB_PASSWORD=change-me \
  ghcr.io/songxwn/netbox-glean:latest
```

GHCR 包首次推送后默认是**私有**的，可在 GitHub 的包页面（Profile → Packages）把 `netbox-glean` 的可见性改为 `Public`，之后即可匿名拉取；保持私有时需要先登录：

```bash
echo "$GITHUB_TOKEN" | docker login ghcr.io -u <github-user> --password-stdin
```

镜像同时支持 `linux/amd64` 与 `linux/arm64`。

或使用 Docker Compose（默认拉取 GHCR 镜像，本地修改代码时用 `--build`）：

```bash
docker compose pull
docker compose up -d

# 使用本地源码构建
docker compose up -d --build
```

Compose 默认把容器的 `8080` 映射到宿主机 `8788`，启动后访问 <http://localhost:8788>。

> 采集局域网设备时，容器需要能访问目标网段。Linux 下可按需使用 `network_mode: host`。
> 官方镜像已给二进制授予 `CAP_NET_RAW`（仅用于可选的 ICMP 扫描）。若容器以 `no-new-privileges` 运行，ICMP 扫描会被跳过，ARP/NDP 读取不受影响。
> 数据卷中保存明文凭据，请限制宿主机访问权限。

### 从源码运行

需要 Go 1.27+：

```bash
go mod tidy
go run ./cmd/netbox-glean --listen 127.0.0.1:8080 --data-dir ./data
```

## NetBox 准备

### 1. API Token

推荐为 `netbox-glean` 创建专用 Token，至少具备以下对象的新增/修改/删除权限：

- `dcim.manufacturer`、`dcim.site`、`dcim.devicetype`、`dcim.devicerole`、`dcim.platform`
- `dcim.device`、`dcim.interface`、`dcim.inventoryitem`、`dcim.cable`
- `ipam.ipaddress`
- `extras.customfield`（只读，用于检测字段是否存在）

在 Web UI 的「设置 → NetBox 连接」中填入：

| Token 类型 | 形态 | 认证头 |
| --- | --- | --- |
| v1（NetBox ≤ 4.4 及旧版） | 普通 Token 字符串 | `Authorization: Token <TOKEN>` |
| v2（NetBox 4.5+） | `nbt_<key>.<secret>` | `Authorization: Bearer nbt_<key>.<secret>` |

`Token 版本` 保持「自动识别」时，以 `nbt_` 前缀判断为 v2，否则按 v1 处理。也可以手动指定。

### 2. Site / Device Role

在「设置 → 写入默认值」中填写：

- `Site slug`：必填，例如 `dc1`
- `Site 名称`：首次自动创建 Site 时使用，可留空
- `Device role slug`：默认 `server`
- `Platform slug`：可选
- `Device type U 高`：默认 `1`

每个采集目标可以单独覆盖 Site、Device Role、Platform 和 NetBox 设备名。

### 3. 自定义字段（可选）

勾选「写入已存在的 `glean_*` 自定义字段」后，工具只会写入 NetBox 中**已经存在**且适用于 Device 的字段，不会自动创建字段。

建议在 NetBox 中为 Device 创建以下字段：

| 字段名 | 类型 | 说明 |
| --- | --- | --- |
| `glean_last_seen` | 文本 | 最近采集时间（RFC3339） |
| `glean_source` | 文本 | `snmp` / `ipmi` / `redfish` |
| `glean_power_state` | 文本 | 电源状态 |
| `glean_cpu_model` | 文本 | CPU 型号 |
| `glean_cpu_cores` | 整数 | CPU 总核数 |
| `glean_memory_gb` | 小数 | 内存容量（GB） |
| `glean_bios_version` | 文本 | BIOS 版本 |
| `glean_bmc_version` | 文本 | BMC 固件版本 |
| `glean_uuid` | 文本 | 系统 UUID |
| `glean_manufacturer` | 文本 | 采集到的厂商 |
| `glean_model` | 文本 | 采集到的型号 |
| `glean_serial` | 文本 | 采集到的序列号 |
| `glean_managed_by` | 文本 | 固定为 `netbox-glean` |

## 采集目标

| 协议 | 默认端口 | 必填凭据 | 说明 |
| --- | --- | --- | --- |
| SNMP | 161/UDP | v1/v2c：Community；v3：用户名 + 认证/加密配置 | 读取 `SNMPv2-MIB`、`ENTITY-MIB`、`IF-MIB` |
| IPMI | 623/UDP | 用户名、密码 | 默认 `lanplus`（IPMI 2.0），也可选 `lan` |
| Redfish | 443/TCP | 用户名、密码 | 地址可填 IP、主机名或完整 `https://...` URL |

补充说明：

- Redfish 默认使用 HTTPS；自签名证书可在目标中开启「跳过 TLS 证书校验」。
- SNMP v3 若只填用户名不填认证密码，会使用 `noAuthNoPriv`；填认证密码而不填加密密码则使用 `authNoPriv`。
- IPMI 采集的是 FRU 信息（机箱、主板、产品信息），不会读取传感器或 SEL。
- 采集结果只是预览，不会写入 NetBox；同步任务才会写入。

### 批量扫描设备

「采集目标」页面顶部和概览页都有「批量扫描」按钮。填写 IPv4 网段、协议、凭据，以及可选的默认 Site / Device Role / Platform / 名称前缀：

1. 先对整个网段做 ICMP 探活，跳过没有响应的地址。
2. 对存活地址用所选协议做一次快速探测：SNMP 读取 `sysName`，IPMI 建立会话，Redfish 检查 HTTPS 端口。
3. 探测成功的设备会执行一次完整采集，然后自动创建为采集目标。
4. 目标已存在（按地址匹配）时标记为「已存在」，不会重复创建。
5. 勾选「发现后立即采集并同步」时，会为每个新目标提交采集并同步任务。

限制与说明：

- 单次批量扫描最多 256 个地址，ICMP 探活本身单次最多 1024 个地址；建议按 `/24` 或更小网段分批扫描。
- 同一网段必须使用同一套管理凭据；批量扫描不会猜测凭据。
- 扫描任务与其他采集任务共享并发限制，设备较多时请提高 `--timeout` 或缩小网段。
- 扫描结果保存在任务详情中，包含存活、新增、已存在、跳过和失败计数，以及每台设备的处理结果。

### 定时任务

在目标的编辑窗口勾选「启用定时采集并同步」并填写间隔（分钟，5–10080）即可。行为说明：

- 定时任务固定执行「采集并同步」，不需要在 Web UI 手动触发。
- 首次启用后会立即执行一次，之后按 `last_run_at + 间隔` 判断是否到期。
- 同一目标已有排队或运行中的任务时，本轮跳过，避免重复写入。
- 目标被停用后不会再触发定时任务。
- 设备目标与 IP 采集目标各自独立调度；IP 采集目标的调度信息保存在 `ip_targets.json`。
- 调度状态保存在 `targets.json` / `ip_targets.json` 的 `last_run_at` 字段；重启不会丢失。
- 任务列表与详情会标记触发方式（`manual` / `schedule` / `scan`）。

## 数据映射

| 采集结果 | NetBox 对象 |
| --- | --- |
| 厂商 | Manufacturer |
| 型号 | Device Type（`model` + `u_height`） |
| 序列号、资产标签 | Device `serial` / `asset_tag` |
| 名称 | Device `name`（优先使用目标中的设备名覆盖） |
| 网卡 | Interface（按名称匹配，存在则更新） |
| LLDP 邻居 | Cable（仅连接 NetBox 中已存在的设备与接口，已连接的接口会跳过） |
| 硬件组件 | Inventory Item（按名称匹配，存在则更新） |
| ARP/NDP 邻居、ICMP 存活地址（IP 采集） | IP Address（MAC 匹配到 NetBox 接口后自动分配） |
| 管理地址 | 可选写入 IP Address 并设为主 IP |
| CPU / 内存 / BIOS / BMC 等 | 可选写入 `glean_*` 自定义字段 |

删除行为：

- 「删除本次未采集到的接口」默认关闭；开启后只删除该设备下、未出现在本次采集结果中的接口。
- 管理接口（默认 `mgmt0`）不会被自动删除。
- 硬件清单不会删除历史组件，避免误删人工维护的数据。

### LLDP 自动连线

- 仅 SNMP 目标会读取 LLDP（`LLDP-MIB` 的 `lldpRemTable`），IPMI 与 Redfish 目标不会产生邻居数据。
- 同步顺序为：设备 → 接口 → IP 地址 → Cable，因此新建的接口也可以立即连线。
- 远端设备必须已存在于 NetBox，按设备名匹配（优先同一 Site）；远端端口依次按 `lldpRemPortId`、`lldpRemPortDesc`、MAC 地址匹配。
- 已经存在 Cable 的接口会跳过，不覆盖人工维护的连线。
- 可在「设置 → 同步选项」中关闭「按 LLDP 邻居自动创建连线」。
- NetBox 4.x 使用 `a_terminations` / `b_terminations` 创建 Cable，3.x 使用旧版 termination 字段，工具会自动回退重试。

### IP 采集

IP 地址采集与设备采集完全分开：在「IP 采集」页面新增独立目标，不会修改或依赖设备目标的采集配置。每个 IP 采集目标由两部分组成，至少填写一项：

1. **SNMP 来源（可选）**：填写一台三层设备的地址与 SNMP 凭据，读取它的地址转换表：
   - `ipNetToMediaTable`：传统 IPv4 ARP 表
   - `ipNetToPhysicalTable`：IPv4 / IPv6 邻居表（NDP）
2. **扫描网段（可选）**：逗号分隔的 IPv4 CIDR，例如 `10.0.0.0/24, 10.0.1.0/24`。工具会向网段内每个可用地址发送 ICMP Echo：
   - 单次最多扫描 1024 个地址，自动跳过网络地址和广播地址。
   - 仅支持 IPv4；IPv6 邻居仍可通过 `ipNetToPhysicalTable` 读取。
   - 扫描结果会与 ARP/NDP 结果合并，同一地址优先保留 MAC 和接口信息。

写入 NetBox 的行为：

- 先按 MAC 查询 NetBox 接口（`/api/dcim/interfaces/?mac_address=...`）。
- 匹配到接口时创建 `ipam.ipaddress` 并分配给该接口；已存在的 IP 如果尚未分配，会补上分配，但不会覆盖已有分配。
- 未匹配到接口的 IP 默认跳过，避免把交换机 ARP 表中的手机、打印机等全部写入 IPAM。可在「设置 → 同步选项」勾选「写入未匹配 NetBox 接口的 IP」，以未分配形式写入。
- 只有 ICMP 扫描结果时拿不到 MAC；需要写入 IPAM 时请开启上面的「写入未匹配 NetBox 接口的 IP」，或同时读取 ARP/NDP 表以获取 MAC。
- 也可以在「设置 → 同步选项」关闭「同步发现的 IP 地址到 NetBox IPAM」，只查看采集结果。
- ICMP 扫描由本机发起，需要能路由到目标网段。
- IP 采集目标同样支持定时任务（「采集并同步」），并可通过「采集全部 / 同步全部」批量执行。

权限说明：

- Linux / macOS 会优先使用非特权 ping socket，失败时回退到原始 ICMP socket。
- 从源码运行且系统禁用了非特权 ping socket 时，可执行 `sudo setcap cap_net_raw=+ep ./netbox-glean` 授予该能力。
- 官方 Docker 镜像已通过 `setcap cap_net_raw=+ep` 给二进制授予 `CAP_NET_RAW`；以 `no-new-privileges` 运行时需要自行调整。
- Windows 需要以管理员身份运行；否则请关闭 ICMP 扫描，仅使用 ARP/NDP。

## 配置

优先级：命令行参数 > 环境变量 > 默认值。

| 参数 | 环境变量 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `--listen` | `NETBOX_GLEAN_LISTEN` | `0.0.0.0:8080` | HTTP 监听地址 |
| `--data-dir` | `NETBOX_GLEAN_DATA_DIR` | `data` | 设置与目标 JSON 存放目录 |
| `--web-user` | `NETBOX_GLEAN_WEB_USERNAME` | 空 | Web UI HTTP Basic Auth 用户名 |
| `--web-password` | `NETBOX_GLEAN_WEB_PASSWORD` | 空 | 设置后启用 HTTP Basic Auth |
| `--netbox-url` | `NETBOX_GLEAN_NETBOX_URL` | 空 | 首次启动时写入 NetBox URL |
| `--netbox-token` | `NETBOX_GLEAN_NETBOX_TOKEN` | 空 | 首次启动时写入 Token |
| `--site` | `NETBOX_GLEAN_SITE` | 空 | 首次启动时写入默认 Site |
| `--device-role` | `NETBOX_GLEAN_DEVICE_ROLE` | 空 | 首次启动时写入默认 Device Role |
| `--concurrency` | `NETBOX_GLEAN_CONCURRENCY` | `4` | 最大并发任务数 |
| `--timeout` | `NETBOX_GLEAN_TIMEOUT` | `10m` | 单任务超时 |
| `--debug` | `NETBOX_GLEAN_DEBUG` | `false` | 输出调试日志 |
| `--version` | - | - | 打印版本并退出 |

首次启动后，除进程级参数（监听地址、数据目录、并发、超时、Web 登录）外，其余配置以 Web UI 保存的值为准。

## HTTP API

Web UI 使用的接口也可以直接调用：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/health` | 健康检查（无需登录） |
| `GET` | `/api/version` | 版本信息 |
| `GET` | `/api/stats` | 目标与任务统计 |
| `GET` | `/api/settings` | 读取设置（Token 脱敏） |
| `PUT` | `/api/settings` | 保存设置；Token 留空表示不修改 |
| `POST` | `/api/settings/test` | 测试 NetBox 连接（别名 `/api/netbox/test`） |
| `GET` | `/api/targets` | 列出目标（凭据脱敏） |
| `POST` | `/api/targets` | 新增目标 |
| `GET` | `/api/targets/{id}` | 目标详情 |
| `PUT` | `/api/targets/{id}` | 更新目标；密码留空表示不修改 |
| `DELETE` | `/api/targets/{id}` | 删除目标 |
| `POST` | `/api/targets/{id}/collect` | 提交任务，`{"sync": true}` 表示采集并同步 |
| `POST` | `/api/collect` | 批量提交设备采集，`{"target_ids": [], "sync": true}`；不传 ID 表示所有启用目标 |
| `GET` | `/api/ip-targets` | 列出 IP 采集目标（凭据脱敏） |
| `POST` | `/api/ip-targets` | 新增 IP 采集目标 |
| `GET` | `/api/ip-targets/{id}` | IP 采集目标详情 |
| `PUT` | `/api/ip-targets/{id}` | 更新 IP 采集目标；密码留空表示不修改 |
| `DELETE` | `/api/ip-targets/{id}` | 删除 IP 采集目标 |
| `POST` | `/api/ip-targets/{id}/collect` | 提交 IP 采集任务，`{"sync": true}` 表示采集并同步 IPAM |
| `POST` | `/api/ip-collect` | 批量提交 IP 采集，`{"target_ids": [], "sync": true}`；不传 ID 表示所有启用目标 |
| `POST` | `/api/scan` | 批量扫描设备并自动添加目标 |
| `GET` | `/api/jobs` | 任务列表，支持 `?limit=` |
| `GET` | `/api/jobs/{id}` | 任务详情（含日志、采集结果或扫描结果） |
| `DELETE` | `/api/jobs` | 清理已完成任务 |

目标对象中与定时任务相关的字段：`schedule_enabled`、`schedule_interval_minutes`（分钟）、`last_run_at`（只读，由服务端维护）。任务对象新增 `trigger` 字段（`manual` / `schedule` / `scan`）。

IP 采集目标对象的主要字段：`name`、`enabled`、`subnets`（逗号分隔的 IPv4 CIDR）、`address` / `port` / `snmp`（可选 SNMP 来源）、`schedule_enabled`、`schedule_interval_minutes`、`last_run_at`。设置对象包含 `sync_ip_addresses` 与 `sync_unmatched_ips`。采集结果中的 `ip_addresses` 数组包含 `address`、`mac`、`interface`、`source` 等字段。

批量扫描请求示例：

```json
{
  "subnets": "10.0.0.0/24",
  "protocol": "snmp",
  "snmp": {"version": "v2c", "community": "public"},
  "site": "dc1",
  "device_role": "switch",
  "name_prefix": "office",
  "enabled": true,
  "sync": true
}
```

任务详情的 `scan` 字段包含 `scanned`、`alive`、`added`、`existing`、`skipped`、`failed`、`devices` 与 `warnings`。

> **从 v0.4 升级**：旧版 `targets.json` 中的 `discover_ips` / `scan_subnets` 会在首次启动时自动迁移为独立的 IP 采集目标（ID 为 `ip-<目标ID>`），并重写 `targets.json` 移除这两个字段。迁移是幂等的。

示例：

```bash
curl -u admin:change-me -X POST http://localhost:8080/api/targets/0123456789abcdef/collect \
  -H 'Content-Type: application/json' \
  -d '{"sync": true}'
```

## 构建与发布

### 本地构建

```bash
make tidy           # 首次构建前生成 go.sum
make build          # 输出 bin/netbox-glean
make test           # go test ./...
make dist           # 生成多平台二进制
```

仓库不提交 `go.sum`；`make build`、`make test` 等目标会先自动执行 `go mod tidy`，CI 也会在构建前生成。

### GitHub Actions

- `.github/workflows/ci.yml`：推送 / PR 时执行 `go mod tidy`、构建、`go vet`、测试、跨平台编译冒烟测试和 Docker 镜像构建。
- `.github/workflows/release.yml`：
  - 推送 `v*` 标签时，交叉编译所有目标平台并发布 GitHub Release
  - 手动触发时只构建并保留工作流产物
- `.github/workflows/docker.yml`：构建 `linux/amd64` / `linux/arm64` 多架构镜像并推送到 GHCR（`latest`、分支、语义化版本和 `sha-` 标签）。

发布：

```bash
git tag v0.1.0
git push origin v0.1.0
```

发布产物包含：

- `linux/amd64`、`linux/arm64`
- `darwin/amd64`、`darwin/arm64`
- `windows/amd64`、`windows/arm64`
- `freebsd/amd64`、`freebsd/arm64`
- `checksums.txt`

所有平台仅提供 `amd64` 与 `arm64` 两种架构。

所有构建均为 `CGO_ENABLED=0` 静态二进制（Windows 使用系统 API，无需额外运行库）。

## 安全说明

- **凭据以明文 JSON 保存**在数据目录（`settings.json`、`targets.json`、`ip_targets.json`），文件权限为 `0600`。请限制数据目录访问，或将其放在加密卷中。
- Web UI 默认不带登录。暴露到非可信网络前，务必设置 `NETBOX_GLEAN_WEB_USERNAME` / `NETBOX_GLEAN_WEB_PASSWORD`，并通过 HTTPS 反向代理访问。
- 建议为 NetBox 使用最小权限专用 Token，并定期轮换。
- SNMP v1/v2c Community 和 IPMI/Redfish 密码会以明文经过网络传输（Redfish 除外，使用 HTTPS）。请仅在受信管理网络中使用。
- 「跳过 TLS 证书校验」会降低安全性，只建议在实验室或自签名证书环境临时使用。
- ICMP 扫描需要 `CAP_NET_RAW`（容器镜像已通过文件能力授予）或管理员权限；请仅在受信管理网络中使用，避免扫描不属于自己的网段。
- 定时任务按 30 秒粒度检查是否到期；进程停机期间错过的执行不会补跑，恢复后按 `last_run_at` 判断是否到期。
- 任务历史保存在内存中，进程重启即清空；不会持久化采集到的凭据或结果。

## 已知限制

- 不支持 NetBox v2 Token 的自动刷新；Token 过期后需要重新填写。
- SNMP 采集以标准 MIB（`ENTITY-MIB`、`IF-MIB`）为主，厂商私有 MIB 未覆盖。
- IPMI 仅采集 FRU，不采集传感器、SEL、SOL。
- 硬件清单按名称匹配更新，不做删除；重命名组件会在 NetBox 中新增一条记录。
- LLDP 只能发现直连邻居；远端设备未录入 NetBox、或接口命名与 LLDP 上报不一致时无法自动连线。
- 每个接口只保留一条 Cable，已有连线不会被自动改写。
- IP 采集是独立目标；ICMP 扫描只支持 IPv4，单次最多 1024 个地址，且需要 `CAP_NET_RAW` 或管理员权限。
- 批量设备扫描先做 ICMP 探活，单次最多 256 个地址；未开启 ICMP 权限时无法使用批量扫描。
- 只有 MAC 能在 NetBox 中找到对应接口时才会自动分配 IP；未匹配地址默认不写入 IPAM。
- 单次同步最多处理 1000 个 IP，超大 ARP 表会被截断并产生警告。
- NetBox 写入为逐对象 REST 调用，大规模设备建议分批执行或提高超时。

## 目录结构

```text
cmd/netbox-glean/        程序入口
internal/collect/        SNMP / IPMI / Redfish 采集器
internal/netbox/         NetBox REST 客户端与同步逻辑
internal/model/          共享数据类型与默认值
internal/store/          JSON 持久化
internal/jobs/           后台任务管理
internal/server/         HTTP API、Web UI 与定时调度
internal/server/web/     前端静态资源（go:embed）
internal/config/         命令行与环境变量
.github/workflows/       CI / Release / 容器镜像
```

## 故障排查

| 现象 | 排查方向 |
| --- | --- |
| SNMP 超时 | 确认 UDP 161 可达、Community 正确、设备 ACL 放行；v3 检查用户名/协议/密码 |
| IPMI 连接失败 | 确认 UDP 623 可达、BMC 已启用 IPMI over LAN、用户权限足够；可尝试 `lan` 接口 |
| Redfish 401/403 | 确认账号密码与权限；BMC 可能要求先创建会话或启用 Redfish |
| Redfish TLS 报错 | 自签名证书时开启「跳过 TLS 证书校验」 |
| NetBox 返回 400 | 查看任务日志中的 API 错误；常见原因是 Site/Role slug 不存在、Device Type 冲突或自定义字段类型不匹配 |
| NetBox 401/403 | 检查 Token 版本（v1/v2）、Token 权限和是否过期 |
| 设备被创建到错误 Site | 检查目标级 Site 覆盖与默认 Site 设置 |
| LLDP 邻居为空 | 确认设备已启用 LLDP（`lldpAdminStatus` 为 `enabled`）且对端也在发送 LLDP |
| 有邻居但没有创建 Cable | 远端设备需先存在于 NetBox；检查远端端口名是否与 NetBox 接口名一致，任务日志会列出跳过的原因 |
| 批量扫描没有新增目标 | 确认网段路由、ICMP 权限、协议端口放行与凭据正确；任务详情会显示每台设备是跳过还是失败 |
| IP 采集结果为空 | 确认 IP 采集目标至少配置了扫描网段或 SNMP 来源；ICMP 结果缺少 MAC 时需开启「写入未匹配接口的 IP」 |

日志会随任务保存在内存中，也可通过 `--debug` 查看进程级日志。

## 许可证

[MIT](LICENSE)

---

项目地址：<https://github.com/Songxwn/netbox-glean>