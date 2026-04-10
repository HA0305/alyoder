package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	bssopenapi "github.com/alibabacloud-go/bssopenapi-20171214/v6/client"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	"github.com/alibabacloud-go/tea/tea"
)

// CSV 表头（39列），与阿里云手动下载的账单格式一致
var csvHeader = []string{
	"资源购买账号ID", "资源购买账号", "资源归属账号ID", "资源归属账号",
	"账单月份", "账单日期", "费用类型",
	"产品Code", "产品名称", "商品Code", "商品名称",
	"资产/资源实例ID", "资产/资源实例名称", "资源组", "实例标签",
	"资源实例配置", "资源实例规格", "公网IP", "私网IP",
	"地域", "可用区",
	"计费项Code", "计费规则说明", "计费项",
	"资源包抵扣量", "用量", "用量单位",
	"目录价", "目录价定价类型", "价格单位", "目录总价",
	"优惠券抵扣金额", "优惠金额", "节省计划抵扣目录总价",
	"应付金额", "代金券抵扣金额", "定价币种", "主键", "优惠后金额",
}

// safeStr 安全获取 *string 字段的值，nil 返回空字符串
func safeStr(p *string) string {
	if p != nil {
		return *p
	}
	return ""
}

// safeFloat32 安全获取 *float32 字段的值，nil 返回空字符串
func safeFloat32(p *float32) string {
	if p != nil {
		return fmt.Sprintf("%.10f", *p)
	}
	return ""
}

// createClient 初始化阿里云 BssOpenApi 客户端
func createClient() (*bssopenapi.Client, error) {
	accessKeyID := os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_ID")
	accessKeySecret := os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET")
	if accessKeyID == "" || accessKeySecret == "" {
		log.Fatal("请设置环境变量 ALIBABA_CLOUD_ACCESS_KEY_ID 和 ALIBABA_CLOUD_ACCESS_KEY_SECRET")
	}

	config := &openapi.Config{
		AccessKeyId:     tea.String(accessKeyID),
		AccessKeySecret: tea.String(accessKeySecret),
		Endpoint:        tea.String("business.aliyuncs.com"),
	}
	return bssopenapi.NewClient(config)
}

// fetchAllBillItems 逐日+分页获取所有账单条目
// 当 Granularity=DAILY 时，API 要求必须指定 BillingDate，因此需要按天循环
func fetchAllBillItems(client *bssopenapi.Client, billingCycle string) (
	items []*bssopenapi.DescribeInstanceBillResponseBodyDataItems,
	accountID, accountName, cycle string,
	err error,
) {
	// 解析账单月份，确定日期范围
	startDate, parseErr := time.Parse("2006-01", billingCycle)
	if parseErr != nil {
		err = fmt.Errorf("解析账单月份失败: %v", parseErr)
		return
	}

	// 结束日期：取月末和今天中较小的那个
	now := time.Now()
	today := now
	nextMonth := startDate.AddDate(0, 1, 0)
	lastDayOfMonth := nextMonth.AddDate(0, 0, -1)

	endDate := lastDayOfMonth
	if today.Before(lastDayOfMonth) {
		endDate = today
	}

	totalCount := 0
	firstData := true

	// 逐日循环
	for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format("2006-01-02")
		fmt.Printf("  查询日期: %s ...", dateStr)
		dayCount := 0
		nextToken := ""

		// 分页循环（单日内）
		for {
			request := &bssopenapi.DescribeInstanceBillRequest{
				BillingCycle:  tea.String(billingCycle),
				BillingDate:   tea.String(dateStr),
				Granularity:   tea.String("DAILY"),
				IsBillingItem: tea.Bool(true),
				MaxResults:    tea.Int32(300),
			}
			if nextToken != "" {
				request.NextToken = tea.String(nextToken)
			}

			resp, reqErr := client.DescribeInstanceBill(request)
			if reqErr != nil {
				err = fmt.Errorf("API 调用失败 (日期 %s): %v", dateStr, reqErr)
				return
			}

			if resp.Body == nil {
				err = fmt.Errorf("API 响应 Body 为空 (日期 %s)", dateStr)
				return
			}
			if resp.Body.Success != nil && !*resp.Body.Success {
				err = fmt.Errorf("API 返回失败 (日期 %s): Code=%s, Message=%s",
					dateStr, safeStr(resp.Body.Code), safeStr(resp.Body.Message))
				return
			}
			if resp.Body.Data == nil {
				// 某天没有数据，跳过
				break
			}

			data := resp.Body.Data

			// 获取顶层账户信息（仅首次）
			if firstData {
				accountID = safeStr(data.AccountID)
				accountName = safeStr(data.AccountName)
				cycle = safeStr(data.BillingCycle)
				firstData = false
			}

			pageItems := data.Items
			dayCount += len(pageItems)
			items = append(items, pageItems...)

			// 检查是否还有下一页
			if data.NextToken == nil || *data.NextToken == "" {
				break
			}
			nextToken = *data.NextToken
		}

		totalCount += dayCount
		fmt.Printf(" %d 条\n", dayCount)
	}

	fmt.Printf("全部日期查询完成，合计 %d 条记录\n", totalCount)
	return
}

// buildCSVRow 将单条账单条目转换为 CSV 行
func buildCSVRow(item *bssopenapi.DescribeInstanceBillResponseBodyDataItems, accountID, accountName, billingCycle string) []string {
	// 资源归属账号ID：优先使用 item.BillAccountID，否则用顶层 accountID
	billAccountID := safeStr(item.BillAccountID)
	if billAccountID == "" {
		billAccountID = accountID
	}
	// 资源归属账号：优先使用 item.BillAccountName，否则用顶层 accountName
	billAccountName := safeStr(item.BillAccountName)
	if billAccountName == "" {
		billAccountName = accountName
	}

	return []string{
		accountID,                               // 资源购买账号ID
		accountName,                             // 资源购买账号
		billAccountID,                           // 资源归属账号ID
		billAccountName,                         // 资源归属账号
		billingCycle,                            // 账单月份
		safeStr(item.BillingDate),               // 账单日期
		safeStr(item.Item),                      // 费用类型
		safeStr(item.ProductCode),               // 产品Code
		safeStr(item.ProductName),               // 产品名称
		safeStr(item.CommodityCode),             // 商品Code
		safeStr(item.ProductDetail),             // 商品名称
		safeStr(item.InstanceID),                // 资产/资源实例ID
		safeStr(item.NickName),                  // 资产/资源实例名称
		safeStr(item.ResourceGroup),             // 资源组
		safeStr(item.Tag),                       // 实例标签
		safeStr(item.InstanceConfig),            // 资源实例配置
		safeStr(item.InstanceSpec),              // 资源实例规格
		safeStr(item.InternetIP),                // 公网IP
		safeStr(item.IntranetIP),                // 私网IP
		safeStr(item.Region),                    // 地域
		safeStr(item.Zone),                      // 可用区
		safeStr(item.BillingItemCode),           // 计费项Code
		"",                                      // 计费规则说明（SDK 中无对应字段）
		safeStr(item.BillingItem),               // 计费项
		safeStr(item.DeductedByResourcePackage), // 资源包抵扣量
		safeStr(item.Usage),                     // 用量
		safeStr(item.UsageUnit),                 // 用量单位
		safeStr(item.ListPrice),                 // 目录价
		safeStr(item.ListPriceUnit),             // 目录价定价类型（复用 ListPriceUnit）
		safeStr(item.ListPriceUnit),             // 价格单位
		safeFloat32(item.PretaxGrossAmount),     // 目录总价
		safeFloat32(item.DeductedByCoupons),     // 优惠券抵扣金额
		safeFloat32(item.InvoiceDiscount),       // 优惠金额
		"",                                      // 节省计划抵扣目录总价（SDK 中无对应字段）
		safeFloat32(item.PretaxAmount),          // 应付金额
		safeFloat32(item.DeductedByCashCoupons), // 代金券抵扣金额
		safeStr(item.Currency),                  // 定价币种
		safeStr(item.PipCode),                   // 主键
		safeFloat32(item.PaymentAmount),         // 优惠后金额
	}
}

func main() {
	// 解析命令行参数
	billingCycle := flag.String("cycle", time.Now().Format("2006-01"), "账单月份，格式 YYYY-MM，默认当前月份")
	outputDir := flag.String("dir", "./", "输出 CSV 文件目录")
	flag.Parse()

	// 自动生成文件名：账单月份_导出时间.csv
	timestamp := time.Now().Format("20060102150405")
	fileName := fmt.Sprintf("bill_%s_%s.csv", *billingCycle, timestamp)
	outputPath := filepath.Join(*outputDir, fileName)

	// 确保输出目录存在
	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		log.Fatalf("创建输出目录失败: %v\n", err)
	}

	fmt.Printf("=== 阿里云实例账单导出工具 ===\n")
	fmt.Printf("账单月份: %s\n", *billingCycle)
	fmt.Printf("输出路径: %s\n\n", outputPath)

	// 1. 初始化客户端
	fmt.Println("[1/3] 初始化阿里云客户端...")
	client, err := createClient()
	if err != nil {
		log.Fatalf("初始化客户端失败: %v\n", err)
	}
	fmt.Println("  客户端初始化成功")

	// 2. 分页获取账单数据
	fmt.Println("[2/3] 开始获取账单数据...")
	items, accountID, accountName, cycle, err := fetchAllBillItems(client, *billingCycle)
	if err != nil {
		log.Fatalf("获取账单数据失败: %v\n", err)
	}
	if len(items) == 0 {
		fmt.Println("  未获取到任何账单数据，程序退出")
		return
	}

	// 3. 写入 CSV 文件
	fmt.Printf("[3/3] 写入 CSV 文件: %s\n", outputPath)
	if err := writeCSV(outputPath, items, accountID, accountName, cycle); err != nil {
		log.Fatalf("写入 CSV 失败: %v\n", err)
	}
	fmt.Printf("  CSV 文件写入完成，共 %d 条记录\n", len(items))
	fmt.Println("=== 完成 ===")
}

// writeCSV 将账单数据写入 CSV 文件（UTF-8 BOM + Windows 行尾）
func writeCSV(path string, items []*bssopenapi.DescribeInstanceBillResponseBodyDataItems, accountID, accountName, billingCycle string) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("创建文件失败: %v", err)
	}
	defer file.Close()

	// 写入 UTF-8 BOM
	if _, err := file.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return fmt.Errorf("写入 BOM 失败: %v", err)
	}

	// 使用 encoding/csv 写入，设置 Windows 风格行尾
	writer := csv.NewWriter(file)
	writer.UseCRLF = true

	// 写入表头
	if err := writer.Write(csvHeader); err != nil {
		return fmt.Errorf("写入表头失败: %v", err)
	}

	// 写入数据行
	for i, item := range items {
		row := buildCSVRow(item, accountID, accountName, billingCycle)
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("写入第 %d 行失败: %v", i+1, err)
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("CSV flush 失败: %v", err)
	}

	return nil
}
