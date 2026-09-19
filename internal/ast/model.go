package ast

// SymbolItem 导出的 AST 核心实体（函数、结构体、接口）
type SymbolItem struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"` // "struct", "interface", "func", "type"
	File    string   `json:"file"` // 相对路径
	Line    int      `json:"line"`
	Doc     string   `json:"doc,omitempty"`
	Methods []string `json:"methods,omitempty"`
}

// PackageNode 架构层级中的包节点
type PackageNode struct {
	ID         string       `json:"id"`          // 归一化相对路径，如 "internal/core/loop"
	Name       string       `json:"name"`        // 包名，如 "loop"
	Path       string       `json:"path"`        // 相对路径
	Layer      string       `json:"layer"`       // "entry", "host", "core", "bus", "spec", "tool", "other"
	LayerName  string       `json:"layer_name"`  // "应用入口层", "宿主与调度", "业务微内核", etc.
	Files      int          `json:"files"`       // Go 源码文件数
	Symbols    []SymbolItem `json:"symbols"`     // 导出的核心符号
	Imports    []string     `json:"imports"`     // 依赖的工作区内部包
	ImportedBy []string     `json:"imported_by"` // 被哪些内部包依赖
}

// ArchitectureEdge 包间依赖拓扑有向边
type ArchitectureEdge struct {
	From            string `json:"from"`
	To              string `json:"to"`
	IsViolation     bool   `json:"is_violation"`
	ViolationReason string `json:"violation_reason,omitempty"`
}

// ContractImpl 契约实现关系
type ContractImpl struct {
	StructName string `json:"struct_name"`
	Package    string `json:"package"`
	File       string `json:"file"`
	Status     string `json:"status"` // "compliant", "partial"
}

// ContractItem 抽象接口契约
type ContractItem struct {
	InterfaceName   string         `json:"interface_name"` // 如 "ToolPlugin"
	Package         string         `json:"package"`        // 如 "pkg/plugin/v1"
	File            string         `json:"file"`
	Methods         []string       `json:"methods"`
	Implementations []ContractImpl `json:"implementations"`
}

// CallSite 精准符号调用与引用代码点
type CallSite struct {
	File     string `json:"file"`               // 相对文件路径
	Line     int    `json:"line"`               // 行号
	Function string `json:"function,omitempty"` // 所属函数或方法名
	Snippet  string `json:"snippet,omitempty"`  // 代码行内容
}

// BlastRadiusReport 符号改动影响面雷达报告
type BlastRadiusReport struct {
	TargetSymbol    string     `json:"target_symbol"`
	TargetPackage   string     `json:"target_package"`
	RiskLevel       string     `json:"risk_level"` // "LOW", "MEDIUM", "HIGH", "CRITICAL"
	DirectCallers   []string   `json:"direct_callers"`
	CallSites       []CallSite `json:"call_sites"` // 符号级精准引用点
	IndirectCallers []string   `json:"indirect_callers"`
	AffectedTests   []string   `json:"affected_tests"`
	Suggestion      string     `json:"suggestion"`
}

// ArchitectureReport 整体架构与依赖分析报告
type ArchitectureReport struct {
	Workspace      string             `json:"workspace"`
	ModulePath     string             `json:"module_path"`
	TotalPackages  int                `json:"total_packages"`
	TotalFiles     int                `json:"total_files"`
	TotalSymbols   int                `json:"total_symbols"`
	ViolationCount int                `json:"violation_count"`
	Packages       []PackageNode      `json:"packages"`
	Edges          []ArchitectureEdge `json:"edges"`
	Contracts      []ContractItem     `json:"contracts"`
}

// GoModuleInfo 工作区或项目中的 Go 模块/子应用信息
type GoModuleInfo struct {
	Name       string `json:"name"`        // 显示名称，例如 "tiancode (根模块)" 或 "cmd/installer (安装程序)"
	Path       string `json:"path"`        // 绝对路径
	RelPath    string `json:"rel_path"`    // 相对工作区路径，如 "." 或 "cmd/installer"
	Type       string `json:"type"`        // "root_module", "sub_module", "cmd_app", "external"
	IsRoot     bool   `json:"is_root"`     // 是否为根项目
	IsExternal bool   `json:"is_external"` // 是否为外部参考目录
}

