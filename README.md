# alyoder — 阿里云百炼大模型账单导出与分析工具

通过阿里云 BssOpenApi 获取实例账单数据，按资产ID和日期维度汇总，并生成格式化推送消息。

## 功能特性

- **alibillget** — 调用 DescribeInstanceBill API 获取 DAILY 粒度账单，生成与阿里云控制台下载格式一致的 39 列 CSV（UTF-8 BOM）
- **alibilloder** — 读取原始 CSV，按资产ID+日期维度汇总应付金额，关联用户信息，输出汇总 CSV 和格式化推送消息（每日明细 `sumall_msg.txt`、月度汇总 `sumID_msg.txt`、单日提取 `sumsend_msg.txt`）

## 目录结构

```text
├── alibillget/        # 账单获取模块
│   ├── main.go        # 调用阿里云 API 获取原始账单
│   ├── go.mod
│   └── go.sum
├── alibilloder/       # 账单处理模块
│   ├── main.go        # 按资产+日期汇总，生成推送消息
│   ├── go.mod
│   └── user_id.txt    # 资产ID→用户名映射（示例）
├── .gitignore
├── README.md
└── LICENSE
```

## 环境要求

- Go 1.18+
- 阿里云 AccessKey（需有 BssOpenApi 权限）

## 快速开始

### 1. 设置环境变量

```bash
export ALIBABA_CLOUD_ACCESS_KEY_ID="your-access-key-id"
export ALIBABA_CLOUD_ACCESS_KEY_SECRET="your-access-key-secret"
```

### 2. 获取账单

```bash
cd alibillget
go build -o alibillget
./alibillget -cycle 2026-04 -dir ./output/
```

### 3. 汇总分析

```bash
cd alibilloder
go build -o alibilloder
./alibilloder -input ../alibillget/output/ -userfile ./user_id.txt -output ./summary.csv
```

### 4. 生成推送消息

```bash
# 所有日期消息 + 月度汇总
./alibilloder -msg -input ../alibillget/output/ -userfile ./user_id.txt -output ./summary.csv -msgdir ./sendmsg/

# 指定日期消息
./alibilloder -msg -date 2026-04-09 -input ../alibillget/output/ -userfile ./user_id.txt -output ./summary.csv -msgdir ./sendmsg/
```

## 命令行参数

### alibillget

| 参数     | 默认值   | 说明                       |
|----------|----------|---------------------------------|
| `-cycle` | 当前月份 | 账单月份，格式 YYYY-MM           |
| `-dir`   | `./`     | CSV 输出目录                   |

### alibilloder

| 参数        | 默认值           | 说明                                 |
|-------------|------------------|----------------------------------------------|
| `-input`    | `./`             | 原始 CSV 所在目录                        |
| `-output`   | `./summary.csv`  | 汇总 CSV 输出路径                        |
| `-userfile` | `./user_id.txt`  | 用户ID映射文件                           |
| `-msg`      | false            | 启用推送消息生成模式                     |
| `-date`     | 空               | 指定日期（YYYY-MM-DD），用于单日消息   |
| `-asset`    | 空               | 指定资产ID过滤                           |
| `-msgdir`   | 与 -output 同级  | 消息文件输出目录                     |

## 输出文件说明

| 文件                 | 说明                          |
|----------------------|-------------------------------|
| `bill_YYYY-MM_*.csv` | 原始账单（39列，UTF-8 BOM）   |
| `summary.csv`        | 按资产ID+日期汇总的应付金额   |
| `sumall_msg.txt`     | 所有日期的每日账单消息        |
| `sumID_msg.txt`      | 本月各资产ID综合汇总          |
| `sumsend_msg.txt`    | 指定日期的单日账单消息        |

## License

[MIT](LICENSE)
