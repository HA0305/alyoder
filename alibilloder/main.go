package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// groupKey 用于按 资产ID + 账单日期 分组
type groupKey struct {
	BillDate string // 账单日期 (YYYY-MM-DD)
	AssetID  string // 资产ID（分号前第一部分）
}

// groupValue 存储分组聚合结果
type groupValue struct {
	TotalAmount  *big.Rat          // 应付金额合计
	ResourceIDs  map[string]bool   // 资源实例ID去重集合
}

// resultRow 用于排序输出
type resultRow struct {
	BillDate    string
	AssetID     string // 资产ID
	UserName    string // 用户名称（来自 user_id.txt）
	ResourceIDs string // 去重后的模型名称，用分号分隔
	TotalAmount string
}

const (
	colBillDate     = 5  // 账单日期
	colProductName  = 8  // 产品名称
	colInstanceID   = 11 // 资产/资源实例ID
	colInstanceName = 12 // 资产/资源实例名称
	colPayAmount    = 34 // 应付金额
	totalColumns    = 39 // CSV 总列数
)

func main() {
	// 命令行参数
	inputDir := flag.String("input", "./", "输入 CSV 文件所在目录")
	outputFile := flag.String("output", "./summary.csv", "输出汇总 CSV 文件路径")
	userFile := flag.String("userfile", "./user_id.txt", "用户ID映射文件路径")
	msgMode := flag.Bool("msg", false, "生成推送消息格式")
	msgDate := flag.String("date", "", "推送消息的账单日期，格式 YYYY-MM-DD")
	msgAsset := flag.String("asset", "", "指定资产ID过滤（可选，空表示所有）")
	msgDir := flag.String("msgdir", "", "消息文件输出目录（默认为 -output 同级目录）")
	flag.Parse()

	fmt.Printf("输入目录: %s\n", *inputDir)
	fmt.Printf("输出文件: %s\n", *outputFile)
	fmt.Printf("用户文件: %s\n", *userFile)

	// 读取用户ID映射
	userMap, err := loadUserMap(*userFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取用户文件失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("已加载 %d 条用户映射\n", len(userMap))

	// 查找所有 CSV 文件
	csvFiles, err := filepath.Glob(filepath.Join(*inputDir, "*.csv"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "查找 CSV 文件失败: %v\n", err)
		os.Exit(1)
	}
	if len(csvFiles) == 0 {
		fmt.Fprintf(os.Stderr, "目录 %s 下未找到 CSV 文件\n", *inputDir)
		os.Exit(1)
	}
	fmt.Printf("找到 %d 个 CSV 文件\n", len(csvFiles))

	// 分组聚合
	groups := make(map[groupKey]*groupValue)
	totalRecords := 0

	for _, csvFile := range csvFiles {
		n, err := processFile(csvFile, groups)
		if err != nil {
			fmt.Fprintf(os.Stderr, "处理文件 %s 失败: %v\n", csvFile, err)
			os.Exit(1)
		}
		totalRecords += n
		fmt.Printf("  已处理: %s (%d 条记录)\n", filepath.Base(csvFile), n)
	}

	fmt.Printf("共读取 %d 条数据记录，汇总为 %d 个分组\n", totalRecords, len(groups))

	// 推送消息模式
	if *msgMode {
		var msgOutDir string
		if *msgDir != "" {
			msgOutDir = *msgDir
		} else {
			msgOutDir = filepath.Dir(*outputFile)
		}
		// 确保消息输出目录存在
		if err := os.MkdirAll(msgOutDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "创建消息输出目录失败: %v\n", err)
			os.Exit(1)
		}
		if *msgDate != "" {
			// 指定日期：生成单日消息保存到 sumsend_msg.txt
			msg := generateMessage(groups, userMap, *msgDate, *msgAsset)
			fmt.Println("\n" + msg)
			msgFile := filepath.Join(msgOutDir, "sumsend_msg.txt")
			if err := os.WriteFile(msgFile, []byte(msg), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "写入消息文件失败: %v\n", err)
			} else {
				fmt.Printf("\n消息已保存到: %s\n", msgFile)
			}
		} else {
			// 未指定日期：生成所有日期的消息保存到 sumall_msg.txt
			allDates := collectDates(groups)
			var allMsg strings.Builder
			for i, date := range allDates {
				if i > 0 {
					allMsg.WriteString("\n\n")
				}
				allMsg.WriteString(generateMessage(groups, userMap, date, *msgAsset))
			}
			fmt.Println("\n" + allMsg.String())
			msgFile := filepath.Join(msgOutDir, "sumall_msg.txt")
			if err := os.WriteFile(msgFile, []byte(allMsg.String()), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "写入消息文件失败: %v\n", err)
			} else {
				fmt.Printf("\n全部消息已保存到: %s\n", msgFile)
			}
		}

		// 生成本月各资产ID综合汇总消息 -> sumID_msg.txt
		monthlyMsg := generateMonthlyMessage(groups, userMap, *msgAsset)
		sumIDFile := filepath.Join(msgOutDir, "sumID_msg.txt")
		if err := os.WriteFile(sumIDFile, []byte(monthlyMsg), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "写入月度汇总消息失败: %v\n", err)
		} else {
			fmt.Printf("月度汇总已保存到: %s\n", sumIDFile)
		}

		return
	}

	// 转换为排序切片
	rows := buildSortedRows(groups, userMap)

	// 写入输出 CSV
	if err := writeOutput(*outputFile, rows); err != nil {
		fmt.Fprintf(os.Stderr, "写入输出文件失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("已生成汇总文件: %s (%d 条记录)\n", *outputFile, len(rows))

	// 打印前几条结果预览
	printPreview(rows, 10)
}

// loadUserMap 读取 user_id.txt，返回 assetID -> userName 映射
func loadUserMap(filePath string) (map[string]string, error) {
	userMap := make(map[string]string)
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("打开文件失败: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// 支持 Tab 或空格分隔
		var id, name string
		if idx := strings.IndexByte(line, '\t'); idx >= 0 {
			id = strings.TrimSpace(line[:idx])
			name = strings.TrimSpace(line[idx+1:])
		} else if idx := strings.IndexByte(line, ' '); idx >= 0 {
			id = strings.TrimSpace(line[:idx])
			name = strings.TrimSpace(line[idx+1:])
		} else {
			continue
		}
		if id != "" && name != "" {
			userMap[id] = name
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}
	return userMap, nil
}

// processFile 读取单个 CSV 文件，将数据聚合到 groups 中，返回处理的记录数
func processFile(filePath string, groups map[groupKey]*groupValue) (int, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("打开文件失败: %w", err)
	}
	defer f.Close()

	// 跳过 UTF-8 BOM (EF BB BF)
	reader := skipBOM(f)

	csvReader := csv.NewReader(reader)
	csvReader.LazyQuotes = true
	csvReader.FieldsPerRecord = -1 // 允许字段数不一致

	// 读取并跳过 header
	header, err := csvReader.Read()
	if err != nil {
		return 0, fmt.Errorf("读取 header 失败: %w", err)
	}
	if len(header) < totalColumns {
		return 0, fmt.Errorf("header 列数不足: 期望 %d 列，实际 %d 列", totalColumns, len(header))
	}

	count := 0
	lineNum := 1 // header 是第 1 行
	for {
		lineNum++
		record, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, fmt.Errorf("第 %d 行读取失败: %w", lineNum, err)
		}
		if len(record) < totalColumns {
			fmt.Fprintf(os.Stderr, "警告: %s 第 %d 行列数不足(%d < %d)，已跳过\n",
				filepath.Base(filePath), lineNum, len(record), totalColumns)
			continue
		}

		billDate := strings.TrimSpace(record[colBillDate])
		instanceID := strings.TrimSpace(record[colInstanceID])
		amountStr := strings.TrimSpace(record[colPayAmount])

		// 拆分资产ID和资源实例ID
		assetID, resourceID := splitInstanceID(instanceID)

		// 解析应付金额（使用有理数避免浮点误差）
		amount := new(big.Rat)
		if amountStr != "" {
			if _, ok := amount.SetString(amountStr); !ok {
				return count, fmt.Errorf("第 %d 行应付金额解析失败: %q", lineNum, amountStr)
			}
		}

		key := groupKey{BillDate: billDate, AssetID: assetID}
		if v, exists := groups[key]; exists {
			v.TotalAmount.Add(v.TotalAmount, amount)
			if resourceID != "" {
				v.ResourceIDs[resourceID] = true
			}
		} else {
			total := new(big.Rat).Set(amount)
			resIDs := make(map[string]bool)
			if resourceID != "" {
				resIDs[resourceID] = true
			}
			groups[key] = &groupValue{
				TotalAmount: total,
				ResourceIDs: resIDs,
			}
		}
		count++
	}
	return count, nil
}

// skipBOM 跳过 UTF-8 BOM 头
func skipBOM(r io.ReadSeeker) io.Reader {
	bom := make([]byte, 3)
	n, err := r.Read(bom)
	if err != nil || n < 3 || bom[0] != 0xEF || bom[1] != 0xBB || bom[2] != 0xBF {
		// 没有 BOM，回到开头
		r.Seek(0, io.SeekStart)
	}
	// 有 BOM 则已跳过，继续从当前位置读取
	return r
}

// buildSortedRows 将分组数据转为排序后的结果行
func buildSortedRows(groups map[groupKey]*groupValue, userMap map[string]string) []resultRow {
	rows := make([]resultRow, 0, len(groups))
	for k, v := range groups {
		// 将去重后的资源实例ID排序后用分号拼接
		resIDs := make([]string, 0, len(v.ResourceIDs))
		for id := range v.ResourceIDs {
			resIDs = append(resIDs, id)
		}
		sort.Strings(resIDs)

		rows = append(rows, resultRow{
			BillDate:    k.BillDate,
			AssetID:     k.AssetID,
			UserName:    userMap[k.AssetID],
			ResourceIDs: strings.Join(resIDs, ";"),
			TotalAmount: formatRat(v.TotalAmount, 10),
		})
	}
	// 按账单日期升序，同日期按资产ID排序
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].BillDate != rows[j].BillDate {
			return rows[i].BillDate < rows[j].BillDate
		}
		return rows[i].AssetID < rows[j].AssetID
	})
	return rows
}

// splitInstanceID 将资产/资源实例ID拆分，返回资产ID和模型名称
// 格式: 资产ID;工作空ID;模型名称;token类型;...;
func splitInstanceID(id string) (assetID, modelName string) {
	parts := strings.Split(id, ";")
	if len(parts) >= 1 {
		assetID = parts[0]
	}
	if len(parts) >= 3 {
		modelName = parts[2]
	}
	return
}

// formatRat 将有理数格式化为指定小数位的字符串
func formatRat(r *big.Rat, decimals int) string {
	// 使用 big.Float 进行精确格式化
	f := new(big.Float).SetRat(r)
	return f.Text('f', decimals)
}

// writeOutput 将汇总结果写入 CSV 文件（UTF-8 BOM + CRLF）
func writeOutput(outputPath string, rows []resultRow) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("创建输出文件失败: %w", err)
	}
	defer f.Close()

	// 写入 UTF-8 BOM
	if _, err := f.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return fmt.Errorf("写入 BOM 失败: %w", err)
	}

	w := csv.NewWriter(f)
	w.UseCRLF = true

	// 写入 header
	header := []string{"账单日期", "资产ID", "用户名称", "模型名称(去重)", "应付金额合计"}
	if err := w.Write(header); err != nil {
		return fmt.Errorf("写入 header 失败: %w", err)
	}

	// 写入数据行
	for _, row := range rows {
		record := []string{
			row.BillDate,
			row.AssetID,
			row.UserName,
			row.ResourceIDs,
			row.TotalAmount,
		}
		if err := w.Write(record); err != nil {
			return fmt.Errorf("写入数据行失败: %w", err)
		}
	}

	w.Flush()
	return w.Error()
}

// printPreview 打印前 N 条结果预览
func printPreview(rows []resultRow, n int) {
	if len(rows) == 0 {
		fmt.Println("无汇总结果")
		return
	}
	if n > len(rows) {
		n = len(rows)
	}
	fmt.Printf("\n--- 前 %d 条汇总结果预览 ---\n", n)
	fmt.Printf("%-12s | %-10s | %-25s | %-40s | %s\n", "账单日期", "资产ID", "用户名称", "模型名称(去重)", "应付金额合计")
	fmt.Println(strings.Repeat("-", 110))
	for i := 0; i < n; i++ {
		r := rows[i]
		resIDs := r.ResourceIDs
		if len(resIDs) > 38 {
			resIDs = resIDs[:35] + "..."
		}
		userName := r.UserName
		if userName == "" {
			userName = "-"
		}
		fmt.Printf("%-12s | %-10s | %-25s | %-40s | %s\n", r.BillDate, r.AssetID, userName, resIDs, r.TotalAmount)
	}
}

// collectDates 收集所有不重复的日期并升序排列
func collectDates(groups map[groupKey]*groupValue) []string {
	dateSet := make(map[string]bool)
	for k := range groups {
		dateSet[k.BillDate] = true
	}
	dates := make([]string, 0, len(dateSet))
	for d := range dateSet {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	return dates
}

// generateMessage 生成推送消息格式
func generateMessage(groups map[groupKey]*groupValue, userMap map[string]string, date string, filterAsset string) string {
	// 筛选指定日期的数据，按资产ID汇总
	type userBill struct {
		AssetID    string
		UserName   string
		ModelNames []string
		Total      *big.Rat
	}

	assetData := make(map[string]*userBill)

	for k, v := range groups {
		if k.BillDate != date {
			continue
		}
		if filterAsset != "" && k.AssetID != filterAsset {
			continue
		}

		ub, exists := assetData[k.AssetID]
		if !exists {
			userName := userMap[k.AssetID]
			if userName == "" {
				userName = "未知用户"
			}
			ub = &userBill{
				AssetID:  k.AssetID,
				UserName: userName,
				Total:    new(big.Rat),
			}
			assetData[k.AssetID] = ub
		}
		ub.Total.Add(ub.Total, v.TotalAmount)
		// 收集去重模型名
		for modelName := range v.ResourceIDs {
			ub.ModelNames = append(ub.ModelNames, modelName)
		}
	}

	if len(assetData) == 0 {
		return fmt.Sprintf("⚠️ 日期 %s 无账单数据", date)
	}

	// 模型名去重并排序
	for _, ub := range assetData {
		unique := make(map[string]bool)
		for _, m := range ub.ModelNames {
			unique[m] = true
		}
		ub.ModelNames = make([]string, 0, len(unique))
		for m := range unique {
			ub.ModelNames = append(ub.ModelNames, m)
		}
		sort.Strings(ub.ModelNames)
	}

	// 按金额降序排店
	userList := make([]*userBill, 0, len(assetData))
	for _, ub := range assetData {
		userList = append(userList, ub)
	}
	sort.Slice(userList, func(i, j int) bool {
		return userList[i].Total.Cmp(userList[j].Total) > 0
	})

	// 计算当日合计
	dailyTotal := new(big.Rat)
	for _, ub := range userList {
		dailyTotal.Add(dailyTotal, ub.Total)
	}

	// 解析日期用于显示
	month := ""
	day := ""
	parts := strings.Split(date, "-")
	if len(parts) == 3 {
		m, _ := strconv.Atoi(parts[1])
		d, _ := strconv.Atoi(parts[2])
		month = fmt.Sprintf("%d", m)
		day = fmt.Sprintf("%d", d)
	}

	// 构建消息
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 百炼大模型 · %s月%s号账单\n", month, day))
	sb.WriteString("━━━━━━━━━━━━━━━━━━\n")

	for i, ub := range userList {
		if i > 0 {
			sb.WriteString("\n")
		}
		amountStr := formatMoney(ub.Total)
		sb.WriteString(fmt.Sprintf("👤 %s：💰 ¥%s\n", ub.UserName, amountStr))
		sb.WriteString(fmt.Sprintf("📦 %s\n", strings.Join(ub.ModelNames, ", ")))
	}

	sb.WriteString("\n━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString(fmt.Sprintf("💰 当日合计：¥%s\n", formatMoney(dailyTotal)))

	return sb.String()
}

// formatMoney 格式化金额，保留两位小数
func formatMoney(r *big.Rat) string {
	f := new(big.Float).SetRat(r)
	return f.Text('f', 2)
}

// generateMonthlyMessage 生成本月各资产ID综合汇总消息
func generateMonthlyMessage(groups map[groupKey]*groupValue, userMap map[string]string, filterAsset string) string {
	type userBill struct {
		AssetID    string
		UserName   string
		ModelNames map[string]bool
		Total      *big.Rat
		Days       map[string]bool // 记录有账单的天数
	}

	assetData := make(map[string]*userBill)
	monthStr := ""

	for k, v := range groups {
		if filterAsset != "" && k.AssetID != filterAsset {
			continue
		}

		// 取月份信息
		if monthStr == "" {
			parts := strings.Split(k.BillDate, "-")
			if len(parts) >= 2 {
				m, _ := strconv.Atoi(parts[1])
				monthStr = fmt.Sprintf("%d", m)
			}
		}

		ub, exists := assetData[k.AssetID]
		if !exists {
			userName := userMap[k.AssetID]
			if userName == "" {
				userName = "未知用户"
			}
			ub = &userBill{
				AssetID:    k.AssetID,
				UserName:   userName,
				ModelNames: make(map[string]bool),
				Total:      new(big.Rat),
				Days:       make(map[string]bool),
			}
			assetData[k.AssetID] = ub
		}
		ub.Total.Add(ub.Total, v.TotalAmount)
		ub.Days[k.BillDate] = true
		for modelName := range v.ResourceIDs {
			ub.ModelNames[modelName] = true
		}
	}

	if len(assetData) == 0 {
		return "⚠️ 无账单数据"
	}

	// 按金额降序
	userList := make([]*userBill, 0, len(assetData))
	for _, ub := range assetData {
		userList = append(userList, ub)
	}
	sort.Slice(userList, func(i, j int) bool {
		return userList[i].Total.Cmp(userList[j].Total) > 0
	})

	// 计算月度合计
	monthlyTotal := new(big.Rat)
	for _, ub := range userList {
		monthlyTotal.Add(monthlyTotal, ub.Total)
	}

	// 构建消息
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 百炼大模型 · %s月账单汇总\n", monthStr))
	sb.WriteString("━━━━━━━━━━━━━━━━━━\n")

	for i, ub := range userList {
		if i > 0 {
			sb.WriteString("\n")
		}
		// 模型名排序
		models := make([]string, 0, len(ub.ModelNames))
		for m := range ub.ModelNames {
			models = append(models, m)
		}
		sort.Strings(models)

		sb.WriteString(fmt.Sprintf("👤 %s：💰 ¥%s（%d天）\n", ub.UserName, formatMoney(ub.Total), len(ub.Days)))
		sb.WriteString(fmt.Sprintf("📦 %s\n", strings.Join(models, ", ")))
	}

	sb.WriteString("\n━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString(fmt.Sprintf("💰 本月合计：¥%s\n", formatMoney(monthlyTotal)))

	return sb.String()
}
