package ast

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AnalyzeWorkspaceArchitecture 深度分析工作区 Go 工程架构、包依赖有向图与契约合规性
func AnalyzeWorkspaceArchitecture(rootDir string) (*ArchitectureReport, error) {
	trimmed := strings.TrimSpace(rootDir)
	if trimmed == "" {
		return nil, fmt.Errorf("workspace root directory cannot be empty")
	}
	stat, err := os.Stat(trimmed)
	if err != nil {
		return nil, fmt.Errorf("workspace root [%s] does not exist: %w", trimmed, err)
	}
	if !stat.IsDir() {
		return nil, fmt.Errorf("workspace root [%s] is not a directory", trimmed)
	}

	normRoot := normalizeWindowsPath(filepath.Clean(trimmed))
	modulePath := detectModulePath(normRoot)

	// 1. 扫描所有包含 .go 文件的目录
	dirFilesMap := make(map[string][]string)
	err = filepath.Walk(normRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info == nil {
			return nil
		}
		if info.IsDir() {
			base := strings.ToLower(info.Name())
			if strings.HasPrefix(base, ".") || base == "node_modules" || base == "vendor" ||
				base == "dist" || base == "bin" || base == "build" || base == "target" ||
				base == "release" || base == "archive" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			dir := filepath.Dir(path)
			dirFilesMap[dir] = append(dirFilesMap[dir], path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk workspace error: %w", err)
	}

	packages := make([]PackageNode, 0, len(dirFilesMap))
	packageMap := make(map[string]*PackageNode)
	fset := token.NewFileSet()

	// 记录所有结构体与方法，用于契约匹配
	type structInfo struct {
		pkgPath string
		name    string
		file    string
		methods map[string]bool
	}
	allStructs := make(map[string]*structInfo) // "pkgPath::StructName" -> structInfo
	type ifaceInfo struct {
		pkgPath string
		name    string
		file    string
		methods []string
	}
	allInterfaces := make([]ifaceInfo, 0)

	// 2. 逐包解析 AST
	for dir, files := range dirFilesMap {
		rel, err := filepath.Rel(normRoot, dir)
		if err != nil {
			rel = filepath.Base(dir)
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			rel = "root"
		}

		var pkgName string
		symbols := make([]SymbolItem, 0)
		internalImports := make(map[string]bool)

		for _, filePath := range files {
			relFile, _ := filepath.Rel(normRoot, filePath)
			relFile = filepath.ToSlash(relFile)

			node, parseErr := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
			if parseErr != nil || node == nil {
				continue
			}
			if pkgName == "" && node.Name != nil {
				pkgName = node.Name.Name
			}

			// 收集 import 依赖
			for _, imp := range node.Imports {
				if imp.Path == nil {
					continue
				}
				importPath := strings.Trim(imp.Path.Value, "\"")
				if strings.HasPrefix(importPath, modulePath) {
					sub := strings.TrimPrefix(importPath, modulePath)
					sub = strings.TrimPrefix(sub, "/")
					if sub == "" {
						sub = "root"
					}
					internalImports[sub] = true
				}
			}

			// 收集导出的声明
			for _, decl := range node.Decls {
				switch d := decl.(type) {
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						typeSpec, ok := spec.(*ast.TypeSpec)
						if !ok || !ast.IsExported(typeSpec.Name.Name) {
							continue
						}
						typeName := typeSpec.Name.Name
						kind := "type"
						methods := make([]string, 0)

						switch t := typeSpec.Type.(type) {
						case *ast.StructType:
							kind = "struct"
							key := rel + "::" + typeName
							if _, exists := allStructs[key]; !exists {
								allStructs[key] = &structInfo{
									pkgPath: rel,
									name:    typeName,
									file:    relFile,
									methods: make(map[string]bool),
								}
							}
						case *ast.InterfaceType:
							kind = "interface"
							if t.Methods != nil {
								for _, m := range t.Methods.List {
									for _, id := range m.Names {
										if ast.IsExported(id.Name) {
											methods = append(methods, id.Name)
										}
									}
								}
							}
							allInterfaces = append(allInterfaces, ifaceInfo{
								pkgPath: rel,
								name:    typeName,
								file:    relFile,
								methods: methods,
							})
						}

						doc := ""
						if d.Doc != nil {
							doc = strings.TrimSpace(d.Doc.Text())
						}

						symbols = append(symbols, SymbolItem{
							Name:    typeName,
							Kind:    kind,
							File:    relFile,
							Line:    fset.Position(typeSpec.Pos()).Line,
							Doc:     doc,
							Methods: methods,
						})
					}

				case *ast.FuncDecl:
					funcName := d.Name.Name
					// 结构体方法
					if d.Recv != nil && len(d.Recv.List) > 0 {
						recvType := getReceiverTypeName(d.Recv.List[0].Type)
						if recvType != "" {
							key := rel + "::" + recvType
							if s, exists := allStructs[key]; exists {
								s.methods[funcName] = true
							} else {
								allStructs[key] = &structInfo{
									pkgPath: rel,
									name:    recvType,
									file:    relFile,
									methods: map[string]bool{funcName: true},
								}
							}
						}
					} else if ast.IsExported(funcName) {
						// 导出的普通函数
						symbols = append(symbols, SymbolItem{
							Name: funcName,
							Kind: "func",
							File: relFile,
							Line: fset.Position(d.Pos()).Line,
						})
					}
				}
			}
		}

		if pkgName == "" {
			pkgName = filepath.Base(dir)
		}

		importList := make([]string, 0, len(internalImports))
		for imp := range internalImports {
			if imp != rel {
				importList = append(importList, imp)
			}
		}
		sort.Strings(importList)

		layer, layerName := classifyArchitecturalLayer(rel)

		pkgNode := PackageNode{
			ID:         rel,
			Name:       pkgName,
			Path:       rel,
			Layer:      layer,
			LayerName:  layerName,
			Files:      len(files),
			Symbols:    symbols,
			Imports:    importList,
			ImportedBy: make([]string, 0),
		}

		packages = append(packages, pkgNode)
		packageMap[rel] = &packages[len(packages)-1]
	}

	// 3. 构建依赖有向边与反向依赖统计
	edges := make([]ArchitectureEdge, 0)
	violationCount := 0

	for i := range packages {
		fromPkg := &packages[i]
		for _, toID := range fromPkg.Imports {
			if toPkg, ok := packageMap[toID]; ok {
				toPkg.ImportedBy = append(toPkg.ImportedBy, fromPkg.ID)

				// 架构守卫规则检测（对齐铁律 7）
				isViolation, reason := checkArchitectureViolation(fromPkg.ID, toID)
				if isViolation {
					violationCount++
				}

				edges = append(edges, ArchitectureEdge{
					From:            fromPkg.ID,
					To:              toID,
					IsViolation:     isViolation,
					ViolationReason: reason,
				})
			}
		}
	}

	// 4. 接口契约多态实现绑定 (Contract Matrix)
	contracts := make([]ContractItem, 0)
	for _, iface := range allInterfaces {
		item := ContractItem{
			InterfaceName:   iface.name,
			Package:         iface.pkgPath,
			File:            iface.file,
			Methods:         iface.methods,
			Implementations: make([]ContractImpl, 0),
		}

		if len(iface.methods) > 0 {
			for _, strct := range allStructs {
				if strct.pkgPath == iface.pkgPath && strct.name == iface.name {
					continue
				}
				// 判断该结构体是否实现接口的所有导出方法
				allMatch := true
				matchCount := 0
				for _, m := range iface.methods {
					if strct.methods[m] {
						matchCount++
					} else {
						allMatch = false
					}
				}
				if allMatch && len(iface.methods) > 0 {
					item.Implementations = append(item.Implementations, ContractImpl{
						StructName: strct.name,
						Package:    strct.pkgPath,
						File:       strct.file,
						Status:     "compliant",
					})
				} else if matchCount > 0 && float64(matchCount)/float64(len(iface.methods)) >= 0.5 {
					item.Implementations = append(item.Implementations, ContractImpl{
						StructName: strct.name,
						Package:    strct.pkgPath,
						File:       strct.file,
						Status:     "partial",
					})
				}
			}
		}

		contracts = append(contracts, item)
	}

	// 排序保证输出确定性
	sort.Slice(packages, func(i, j int) bool {
		return packages[i].ID < packages[j].ID
	})
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From == edges[j].From {
			return edges[i].To < edges[j].To
		}
		return edges[i].From < edges[j].From
	})

	totalSymbols := 0
	totalFiles := 0
	for _, p := range packages {
		totalSymbols += len(p.Symbols)
		totalFiles += p.Files
	}

	return &ArchitectureReport{
		Workspace:      normRoot,
		ModulePath:     modulePath,
		TotalPackages:  len(packages),
		TotalFiles:     totalFiles,
		TotalSymbols:   totalSymbols,
		ViolationCount: violationCount,
		Packages:       packages,
		Edges:          edges,
		Contracts:      contracts,
	}, nil
}

// AnalyzeBlastRadius 分析特定符号被改动时的波及影响面 (Blast Radius Radar)
func AnalyzeBlastRadius(rootDir, targetSymbol string) (*BlastRadiusReport, error) {
	report, err := AnalyzeWorkspaceArchitecture(rootDir)
	if err != nil {
		return nil, err
	}

	// 拆分 "package::Symbol" 或 "Symbol"
	var targetPkg string
	var symName = targetSymbol
	if strings.Contains(targetSymbol, "::") {
		parts := strings.SplitN(targetSymbol, "::", 2)
		targetPkg = parts[0]
		symName = parts[1]
	} else if strings.Contains(targetSymbol, ".") {
		parts := strings.Split(targetSymbol, ".")
		symName = parts[len(parts)-1]
		targetPkg = strings.Join(parts[:len(parts)-1], "/")
	}

	// 定位符号所在包
	var matchedPkg *PackageNode
	for i := range report.Packages {
		p := &report.Packages[i]
		if targetPkg != "" && (p.ID == targetPkg || strings.HasSuffix(p.ID, targetPkg)) {
			matchedPkg = p
			break
		}
		for _, s := range p.Symbols {
			if s.Name == symName {
				matchedPkg = p
				break
			}
		}
		if matchedPkg != nil {
			break
		}
	}

	if matchedPkg == nil && len(report.Packages) > 0 {
		matchedPkg = &report.Packages[0]
	}

	directCallers := make([]string, 0)
	indirectMap := make(map[string]bool)

	candidateDirs := make([]string, 0)
	normRoot := normalizeWindowsPath(filepath.Clean(rootDir))

	if matchedPkg != nil {
		directCallers = append(directCallers, matchedPkg.ImportedBy...)

		// 收集待扫描的物理目录
		if matchedPkg.Path == "root" || matchedPkg.Path == "." {
			candidateDirs = append(candidateDirs, normRoot)
		} else {
			candidateDirs = append(candidateDirs, filepath.Join(normRoot, filepath.FromSlash(matchedPkg.Path)))
		}

		pkgMap := make(map[string]*PackageNode)
		for i := range report.Packages {
			pkgMap[report.Packages[i].ID] = &report.Packages[i]
		}

		for _, direct := range directCallers {
			if dp, ok := pkgMap[direct]; ok {
				if dp.Path == "root" || dp.Path == "." {
					candidateDirs = append(candidateDirs, normRoot)
				} else {
					candidateDirs = append(candidateDirs, filepath.Join(normRoot, filepath.FromSlash(dp.Path)))
				}
				for _, ind := range dp.ImportedBy {
					if ind != matchedPkg.ID && !containsStr(directCallers, ind) {
						indirectMap[ind] = true
					}
				}
			}
		}
	}

	// 若无显式导入者，默认全工作区探测引用点
	if len(candidateDirs) == 0 {
		candidateDirs = append(candidateDirs, normRoot)
	}

	// 深入 AST 扫描具体的符号引用代码行 (CallSite)
	callSites := findSymbolCallSites(normRoot, symName, candidateDirs)

	// 如果找到了精准调用点，把调用点格式化合入 DirectCallers 便于老视图展示，并丰富提示
	formattedCallers := make([]string, 0)
	if len(callSites) > 0 {
		for _, cs := range callSites {
			item := fmt.Sprintf("%s:%d", cs.File, cs.Line)
			if cs.Function != "" {
				item += fmt.Sprintf(" [%s()]", cs.Function)
			}
			if cs.Snippet != "" {
				item += fmt.Sprintf(" › %s", cs.Snippet)
			}
			formattedCallers = append(formattedCallers, item)
		}
	} else {
		// 回退显示包级调用方
		formattedCallers = directCallers
	}

	indirectCallers := make([]string, 0, len(indirectMap))
	for ind := range indirectMap {
		indirectCallers = append(indirectCallers, ind)
	}
	sort.Strings(directCallers)
	sort.Strings(indirectCallers)

	// 计算风险等级 (基于真实调用点数 + 波及包数)
	totalPoints := len(callSites) + len(directCallers) + len(indirectCallers)
	riskLevel := "LOW"
	if totalPoints >= 10 || len(indirectCallers) >= 4 {
		riskLevel = "CRITICAL"
	} else if totalPoints >= 5 || len(indirectCallers) >= 2 {
		riskLevel = "HIGH"
	} else if totalPoints >= 2 {
		riskLevel = "MEDIUM"
	}

	affectedTests := make([]string, 0)
	if matchedPkg != nil {
		affectedTests = append(affectedTests, matchedPkg.ID+"_test.go")
	}
	for _, c := range directCallers {
		affectedTests = append(affectedTests, c+"_test.go")
	}

	suggestion := fmt.Sprintf("改动符号 [%s] 已精准定位到 %d 处源码引用点，波及 %d 个下游模块。建议修改后优先运行关联单测。", symName, len(callSites), len(directCallers))

	targetPkgName := ""
	if matchedPkg != nil {
		targetPkgName = matchedPkg.ID
	}

	return &BlastRadiusReport{
		TargetSymbol:    symName,
		TargetPackage:   targetPkgName,
		RiskLevel:       riskLevel,
		DirectCallers:   formattedCallers,
		CallSites:       callSites,
		IndirectCallers: indirectCallers,
		AffectedTests:   affectedTests,
		Suggestion:      suggestion,
	}, nil
}

// findSymbolCallSites 深入 AST 语法树识别指定符号引用的精准行号与代码摘要
func findSymbolCallSites(rootDir, symName string, candidateDirs []string) []CallSite {
	fset := token.NewFileSet()
	callSites := make([]CallSite, 0)
	visitedSite := make(map[string]bool)

	for _, dir := range candidateDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			filePath := filepath.Join(dir, e.Name())
			fileContentBytes, err := os.ReadFile(filePath)
			if err != nil {
				continue
			}
			fileLines := strings.Split(string(fileContentBytes), "\n")

			node, err := parser.ParseFile(fset, filePath, fileContentBytes, 0)
			if err != nil || node == nil {
				continue
			}

			relFile, _ := filepath.Rel(rootDir, filePath)
			relFile = filepath.ToSlash(relFile)

			var currentFunc string
			ast.Inspect(node, func(n ast.Node) bool {
				if n == nil {
					return true
				}
				switch x := n.(type) {
				case *ast.FuncDecl:
					currentFunc = x.Name.Name
				case *ast.SelectorExpr:
					if x.Sel != nil && x.Sel.Name == symName {
						pos := fset.Position(x.Pos())
						siteKey := fmt.Sprintf("%s:%d", relFile, pos.Line)
						if !visitedSite[siteKey] {
							visitedSite[siteKey] = true
							snippet := ""
							if pos.Line > 0 && pos.Line <= len(fileLines) {
								snippet = strings.TrimSpace(fileLines[pos.Line-1])
							}
							callSites = append(callSites, CallSite{
								File:     relFile,
								Line:     pos.Line,
								Function: currentFunc,
								Snippet:  snippet,
							})
						}
					}
				case *ast.Ident:
					if x.Name == symName {
						pos := fset.Position(x.Pos())
						siteKey := fmt.Sprintf("%s:%d", relFile, pos.Line)
						if !visitedSite[siteKey] {
							visitedSite[siteKey] = true
							snippet := ""
							if pos.Line > 0 && pos.Line <= len(fileLines) {
								snippet = strings.TrimSpace(fileLines[pos.Line-1])
							}
							callSites = append(callSites, CallSite{
								File:     relFile,
								Line:     pos.Line,
								Function: currentFunc,
								Snippet:  snippet,
							})
						}
					}
				}
				return true
			})
		}
	}

	sort.Slice(callSites, func(i, j int) bool {
		if callSites[i].File == callSites[j].File {
			return callSites[i].Line < callSites[j].Line
		}
		return callSites[i].File < callSites[j].File
	})

	return callSites
}

func containsStr(arr []string, s string) bool {
	for _, v := range arr {
		if v == s {
			return true
		}
	}
	return false
}

// classifyArchitecturalLayer 依据工程规范自动判定模块架构层级
func classifyArchitecturalLayer(relPath string) (layer string, layerName string) {
	rel := strings.ToLower(relPath)
	if strings.HasPrefix(rel, "cmd/") || rel == "root" || rel == "." {
		return "entry", "应用入口层 (Entry & Launcher)"
	}
	if strings.HasPrefix(rel, "internal/transport") || strings.HasPrefix(rel, "internal/agent") || strings.HasPrefix(rel, "internal/telemetry") {
		return "host", "宿主中枢与事件分发 (Host & Transport)"
	}
	if strings.HasPrefix(rel, "internal/core") || rel == "internal/session" {
		return "core", "业务微内核与回路 (Core Engine & Loop)"
	}
	if rel == "internal/host" || strings.HasPrefix(rel, "plugins/rail") || rel == "internal/diff" || rel == "internal/lsp" || rel == "internal/gitops" {
		return "bus", "抽象总线与安全防线 (Registry & Safety Rail)"
	}
	if strings.HasPrefix(rel, "pkg/") {
		return "spec", "插件契约与协议 (Plugin Spec & Protocol)"
	}
	if strings.HasPrefix(rel, "plugins/tool") || strings.HasPrefix(rel, "plugins/provider") {
		return "tool", "热插拔工具实现 (Hotplug Operators)"
	}
	return "other", "通用支撑组件 (Supporting Utilities)"
}

// checkArchitectureViolation 检查是否违反项目铁律（如铁律 7 插件单向依赖禁令）
func checkArchitectureViolation(fromPkg, toPkg string) (bool, string) {
	from := strings.ToLower(fromPkg)
	to := strings.ToLower(toPkg)

	// 规则 1: plugins/ 禁止直接反向依赖宿主、内部业务层或入口
	if strings.HasPrefix(from, "plugins/") {
		if to == "root" || strings.HasPrefix(to, "cmd/") || strings.HasPrefix(to, "internal/core") || to == "internal/session" {
			return true, fmt.Sprintf("【铁律 7 违规】插件层 [%s] 非法直接反向依赖宿主/内核层 [%s]，破坏微内核单向解耦！", fromPkg, toPkg)
		}
	}

	// 规则 2: internal/core/ 禁止直接 import 具体 plugins/tool
	if strings.HasPrefix(from, "internal/core") {
		if strings.HasPrefix(to, "plugins/tool") || strings.HasPrefix(to, "plugins/provider") {
			return true, fmt.Sprintf("【铁律 7 违规】微内核层 [%s] 非法直接持有具体插件 [%s]，必须经由 pkg/plugin/v1 契约与 host.Registry 派发！", fromPkg, toPkg)
		}
	}

	return false, ""
}

func detectModulePath(rootDir string) string {
	curr := rootDir
	for i := 0; i < 5; i++ {
		goModPath := filepath.Join(curr, "go.mod")
		if f, err := os.Open(goModPath); err == nil {
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if strings.HasPrefix(line, "module ") {
					parts := strings.Fields(line)
					if len(parts) >= 2 {
						_ = f.Close()
						return parts[1]
					}
				}
			}
			_ = f.Close()
		}
		parent := filepath.Dir(curr)
		if parent == curr || parent == "" {
			break
		}
		curr = parent
	}
	base := filepath.Base(rootDir)
	if base == "." || base == "/" || base == "\\" {
		return "module"
	}
	return base
}

// DiscoverGoModules 自动探测工作区中的根 Go 模块、子模块 (go.mod) 与 cmd/ 入口子应用
func DiscoverGoModules(workspace string) ([]GoModuleInfo, error) {
	trimmed := strings.TrimSpace(workspace)
	if trimmed == "" {
		return nil, fmt.Errorf("workspace path cannot be empty")
	}
	stat, err := os.Stat(trimmed)
	if err != nil {
		return nil, fmt.Errorf("workspace [%s] does not exist: %w", trimmed, err)
	}
	if !stat.IsDir() {
		return nil, fmt.Errorf("workspace [%s] is not a directory", trimmed)
	}

	norm := normalizeWindowsPath(filepath.Clean(trimmed))
	modules := make([]GoModuleInfo, 0)
	visited := make(map[string]bool)

	// 1. 探测根模块
	rootModName := detectModulePath(norm)
	rootDisplayName := fmt.Sprintf("%s (根模块)", filepath.Base(norm))
	if rootModName != "" && rootModName != "module" {
		rootDisplayName = fmt.Sprintf("%s (根项目)", rootModName)
	}
	modules = append(modules, GoModuleInfo{
		Name:       rootDisplayName,
		Path:       norm,
		RelPath:    ".",
		Type:       "root_module",
		IsRoot:     true,
		IsExternal: false,
	})
	visited[norm] = true

	// 2. 扫描工作区子目录中的独立 go.mod
	_ = filepath.Walk(norm, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			base := strings.ToLower(info.Name())
			if strings.HasPrefix(base, ".") || base == "node_modules" || base == "vendor" ||
				base == "dist" || base == "bin" || base == "build" || base == "target" ||
				base == "release" || base == "archive" {
				return filepath.SkipDir
			}
			// 限制层级防深层遍历
			rel, _ := filepath.Rel(norm, path)
			if strings.Count(filepath.ToSlash(rel), "/") > 3 {
				return filepath.SkipDir
			}
			return nil
		}

		if info.Name() == "go.mod" {
			dir := filepath.Dir(path)
			normDir := normalizeWindowsPath(dir)
			if !visited[normDir] {
				visited[normDir] = true
				rel, _ := filepath.Rel(norm, normDir)
				rel = filepath.ToSlash(rel)
				subModName := detectModulePath(normDir)
				modules = append(modules, GoModuleInfo{
					Name:       fmt.Sprintf("%s (独立模块: %s)", rel, subModName),
					Path:       normDir,
					RelPath:    rel,
					Type:       "sub_module",
					IsRoot:     false,
					IsExternal: false,
				})
			}
		}
		return nil
	})

	// 3. 扫描 cmd/ 目录下的独立可执行程序
	cmdDir := filepath.Join(norm, "cmd")
	if entries, err := os.ReadDir(cmdDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				subPath := filepath.Join(cmdDir, e.Name())
				normSub := normalizeWindowsPath(subPath)
				if !visited[normSub] {
					hasGo := false
					if subFiles, readErr := os.ReadDir(subPath); readErr == nil {
						for _, sf := range subFiles {
							if !sf.IsDir() && strings.HasSuffix(sf.Name(), ".go") {
								hasGo = true
								break
							}
						}
					}
					if hasGo {
						visited[normSub] = true
						rel, _ := filepath.Rel(norm, normSub)
						rel = filepath.ToSlash(rel)
						modules = append(modules, GoModuleInfo{
							Name:       fmt.Sprintf("%s (入口程序)", rel),
							Path:       normSub,
							RelPath:    rel,
							Type:       "cmd_app",
							IsRoot:     false,
							IsExternal: false,
						})
					}
				}
			}
		}
	}

	return modules, nil
}

func getReceiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

