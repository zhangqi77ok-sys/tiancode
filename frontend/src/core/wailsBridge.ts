// Wails v2 原生前端生产桥接层 (Zero Demo / 100% 真实原生调用与全量功能)

export interface ChannelConfig {
  id: string
  name: string
  primary: boolean
  status: string
  auth_type: string
  protocol?: string
  endpoint: string
  api_key?: string
  extra_config?: Record<string, string>
  model: string
  extra_models?: string[]
  latency: string
  updated_at: number
}

export interface MCPServerConfig {
  id: string
  name: string
  type: string
  command: string
  args: string[]
  env?: Record<string, string>
  url?: string
  enabled: boolean
  last_error?: string
  updated_at: number
}

export interface MCPTestResult {
  id: string
  name: string
  status: string // 'ONLINE' | 'ERROR'
  latency: string
  tool_count: number
  tools: string[]
  error?: string
}

export interface DiagnosticItem {
  file: string
  line: number
  column: number
  severity: string
  code?: string
  message: string
}

export interface DiagnosticReport {
  success: boolean
  file_path: string
  has_errors: boolean
  error_count: number
  errors: DiagnosticItem[]
  raw_output?: string
}

export interface SkillConfig {
  id: string
  name: string
  description: string
  prompt: string
  enabled: boolean
  updated_at: number
}

export interface RuleConfig {
  id: string
  title: string
  content: string
  scope: string
  enabled: boolean
  updated_at: number
}

export interface GraphNode {
  id: string
  name: string
  type: string
  file: string
  changes: number
  details: string
  children?: string[]
}

export interface SymbolItem {
  name: string
  kind: 'struct' | 'interface' | 'func' | 'type'
  file: string
  line: number
  doc?: string
  methods?: string[]
}

export interface PackageNode {
  id: string
  name: string
  path: string
  layer: string
  layer_name: string
  files: number
  symbols: SymbolItem[]
  imports: string[]
  imported_by: string[]
}

export interface ArchitectureEdge {
  from: string
  to: string
  is_violation: boolean
  violation_reason?: string
}

export interface ContractImpl {
  struct_name: string
  package: string
  file: string
  status: 'compliant' | 'partial'
}

export interface ContractItem {
  interface_name: string
  package: string
  file: string
  methods: string[]
  implementations: ContractImpl[]
}

export interface BlastRadiusReport {
  target_symbol: string
  target_package: string
  risk_level: 'LOW' | 'MEDIUM' | 'HIGH' | 'CRITICAL'
  direct_callers: string[]
  indirect_callers: string[]
  affected_tests: string[]
  suggestion: string
}

export interface ArchitectureReport {
  workspace: string
  module_path: string
  total_packages: number
  total_files: number
  total_symbols: number
  violation_count: number
  packages: PackageNode[]
  edges: ArchitectureEdge[]
  contracts: ContractItem[]
}

export interface GoModuleInfo {
  name: string
  path: string
  rel_path: string
  type: string
  is_root: boolean
  is_external: boolean
}

export interface FileNode {
  name: string
  path: string
  is_dir: boolean
  children?: FileNode[]
  loaded?: boolean
  loading?: boolean
}

export interface SearchMatch {
  path: string
  line?: number
  content?: string
}

export interface HotplugItemInfo {
  id: string
  name: string
  version: string
  type: string
  category: string
  description: string
  healthy: boolean
  latency_ms: number
  message: string
  parameters?: Record<string, any>
  mutating?: boolean
  priority?: number
  metadata?: Record<string, string>
}

export interface HotplugDashboardReport {
  summary: {
    total_tools: number
    active_mcps: number
    active_rails: number
    active_providers: number
    healthy_count: number
  }
  tools: HotplugItemInfo[]
  mcps: HotplugItemInfo[]
  rails: HotplugItemInfo[]
  providers: HotplugItemInfo[]
  updated_at: number
}

export interface TaskModel {
  goal: string
  status: 'idle' | 'running' | 'completed' | 'capped' | 'interrupted' | 'failed' | 'pending_diff' | 'tdd_failed'
  tool_budget: number
  tools_used: number
  summary: string
  tdd_passed?: boolean
  pending_diff_files?: string[]
}

export interface SessionMeta {
  id: string
  title: string
  model: string
  tag: string
  time: string
  desc: string
  updated_at: number
  workspace?: string
  task_status?: string
}

export interface SessionMessage {
  id: string
  role: string
  content: string
  thinking?: string
  _thinkingExpanded?: boolean
  tool?: {
    name: string
    args: any
    output: string
  }
  tools?: Array<{
    id?: string
    name: string
    args: any
    output: string
  }>
  _toolsExpanded?: boolean
  time: string
}

export interface ChatSession {
  id: string
  title: string
  model: string
  tag: string
  workspace?: string
  created_at: number
  updated_at: number
  messages: SessionMessage[]
  task?: TaskModel
}

export interface DiffLine {
  type: 'add' | 'del' | 'ctx'
  text: string
  label?: string
}

export interface DiffHunk {
  index: number
  header: string
  lines: DiffLine[]
  add_count: number
  del_count: number
  raw_patch: string
}

export interface DiffReport {
  file_path: string
  lang: string
  stats: string
  header: string
  lines: DiffLine[]
  hunks?: DiffHunk[]
}

function getApp(): any {
  return (window as any).go?.main?.App
}

function getRuntime(): any {
  return (window as any).runtime
}

export const wailsBridge = {
  // 1. 调起操作系统真实多文件选择对话框
  async openFileDialog(): Promise<string[]> {
    const app = getApp()
    if (app?.OpenFileDialog) {
      return await app.OpenFileDialog()
    }
    return new Promise((resolve) => {
      const input = document.createElement('input')
      input.type = 'file'
      input.multiple = true
      input.onchange = () => {
        const files: string[] = []
        if (input.files) {
          for (let i = 0; i < input.files.length; i++) {
            files.push(input.files[i].name)
          }
        }
        resolve(files)
      }
      input.click()
    })
  },

  // 1.1 调起操作系统真实文件夹选择对话框 (系统原生文件夹选择)
  async openDirectoryDialog(): Promise<string> {
    const app = getApp()
    if (app?.OpenDirectoryDialog) {
      return await app.OpenDirectoryDialog()
    }
    return ''
  },

  async getWorkspace(): Promise<string> {
    const app = getApp()
    if (app?.GetWorkspace) {
      return await app.GetWorkspace()
    }
    return ''
  },

  async setWorkspace(dir: string): Promise<void> {
    const app = getApp()
    if (app?.SetWorkspace) {
      await app.SetWorkspace(dir)
      return
    }
    throw new Error('microkernel not connected: SetWorkspace unavailable')
  },

  // 2. 会话历史管理 (真实读写 ~/.tiancode/sessions/)
  async listSessions(): Promise<SessionMeta[]> {
    const app = getApp()
    if (app?.ListAllSessions) {
      return await app.ListAllSessions()
    }
    if (app?.ListSessions) {
      return await app.ListSessions()
    }
    return []
  },

  async listProjects(): Promise<{ path: string; name: string; opened_at: number }[]> {
    const app = getApp()
    if (app?.ListProjects) return await app.ListProjects()
    return []
  },

  async addProject(path: string): Promise<void> {
    const app = getApp()
    if (app?.AddProject) {
      await app.AddProject(path)
      return
    }
    throw new Error('microkernel not connected: AddProject unavailable')
  },

  async removeProject(path: string): Promise<void> {
    const app = getApp()
    if (app?.RemoveProject) {
      await app.RemoveProject(path)
      return
    }
    throw new Error('microkernel not connected: RemoveProject unavailable')
  },

  async getSession(id: string): Promise<ChatSession | null> {
    const app = getApp()
    if (app?.GetSession) {
      return await app.GetSession(id)
    }
    return null
  },

  async updateSessionTag(id: string, tag: string): Promise<void> {
    const app = getApp()
    if (app?.UpdateSessionTag) {
      await app.UpdateSessionTag(id, tag)
      return
    }
  },

  async appendSystemMessage(id: string, content: string): Promise<void> {
    const app = getApp()
    if (app?.AppendSystemMessage) {
      await app.AppendSystemMessage(id, content)
      return
    }
  },

  async updateTaskStatus(id: string, status: string): Promise<void> {
    const app = getApp()
    if (app?.UpdateTaskStatus) {
      await app.UpdateTaskStatus(id, status)
      return
    }
  },

  async deleteSession(id: string): Promise<void> {
    const app = getApp()
    if (app?.DeleteSession) {
      await app.DeleteSession(id)
      return
    }
    throw new Error('microkernel not connected: DeleteSession unavailable')
  },

  // 3. 真实物理代码 Diff 计算与回滚
  async getStructuredDiff(filePath: string): Promise<DiffReport> {
    const app = getApp()
    if (app?.GetStructuredDiff) {
      return await app.GetStructuredDiff(filePath)
    }
    return {
      file_path: filePath,
      lang: 'Clean',
      stats: '0 行修改',
      header: '@@ 暂无代码改动 @@',
      lines: []
    }
  },

  async revertFile(filePath: string): Promise<void> {
    const app = getApp()
    if (app?.RevertFile) {
      await app.RevertFile(filePath)
      return
    }
    throw new Error('microkernel not connected: RevertFile unavailable')
  },

  async applyDiffHunk(filePath: string, hunkIndex: number, stageOnly: boolean = true): Promise<void> {
    const app = getApp()
    if (app?.ApplyDiffHunk) {
      await app.ApplyDiffHunk(filePath, hunkIndex, stageOnly)
      return
    }
    throw new Error('microkernel not connected: ApplyDiffHunk unavailable')
  },

  async discardDiffHunk(filePath: string, hunkIndex: number): Promise<void> {
    const app = getApp()
    if (app?.DiscardDiffHunk) {
      await app.DiscardDiffHunk(filePath, hunkIndex)
      return
    }
    throw new Error('microkernel not connected: DiscardDiffHunk unavailable')
  },

  // 终端执行与流式监听
  async execTerminalStream(
    command: string,
    callbacks: {
      onStart?: (data: { command: string; start_time: number }) => void
      onData?: (chunk: string) => void
      onExit?: (data: { command: string; exit_code: number; duration_ms: number; error?: string }) => void
    }
  ): Promise<void> {
    const runtime = getRuntime()
    const app = getApp()

    if (runtime && app?.ExecTerminalStream) {
      let isCleaned = false
      const cleanTerminalEvents = () => {
        if (isCleaned) return
        isCleaned = true
        if (runtime.EventsOff) {
          runtime.EventsOff('terminal:start')
          runtime.EventsOff('terminal:data')
          runtime.EventsOff('terminal:exit')
        }
      }

      runtime.EventsOn('terminal:start', (d: any) => callbacks.onStart?.(d))
      runtime.EventsOn('terminal:data', (chunk: string) => callbacks.onData?.(chunk))
      runtime.EventsOn('terminal:exit', (d: any) => {
        callbacks.onExit?.(d)
        cleanTerminalEvents()
      })

      try {
        await app.ExecTerminalStream(command)
      } catch (err) {
        cleanTerminalEvents()
        throw err
      }
      return
    }

    throw new Error('microkernel not connected: ExecTerminalStream unavailable')
  },

  async cancelTerminalCommand(): Promise<void> {
    const app = getApp()
    if (app?.CancelTerminalCommand) {
      await app.CancelTerminalCommand()
    }
  },

  async cancelAgentStream(): Promise<void> {
    const app = getApp()
    if (app?.CancelAgentStream) {
      await app.CancelAgentStream()
    }
  },

  async fetchUpstreamModels(endpoint?: string, apiKey?: string): Promise<string[]> {
    const app = getApp()
    if (app?.FetchUpstreamModels) {
      return await app.FetchUpstreamModels(endpoint || '', apiKey || '')
    }
    return []
  },

  async gitCommit(msg: string): Promise<string> {
    const app = getApp()
    if (app?.GitCommit) {
      return await app.GitCommit(msg)
    }
    throw new Error('microkernel not connected: GitCommit unavailable')
  },

  async gitStage(filePath: string): Promise<void> {
    const app = getApp()
    if (app?.GitStage) {
      await app.GitStage(filePath)
      return
    }
    throw new Error('microkernel not connected: GitStage unavailable')
  },

  async gitUnstage(filePath: string): Promise<void> {
    const app = getApp()
    if (app?.GitUnstage) {
      await app.GitUnstage(filePath)
      return
    }
    throw new Error('microkernel not connected: GitUnstage unavailable')
  },

  async getGitBranches(): Promise<{ branches: string[]; current: string }> {
    const app = getApp()
    if (app?.GetGitBranches) {
      const r = await app.GetGitBranches()
      return {
        branches: Array.isArray(r?.branches) ? r.branches : [],
        current: r?.current || ''
      }
    }
    if (app?.GitListBranches) {
      const r = await app.GitListBranches()
      if (Array.isArray(r)) return { branches: r, current: '' }
    }
    return { branches: [], current: '' }
  },

  async gitCheckoutBranch(name: string): Promise<void> {
    const app = getApp()
    if (app?.GitCheckoutBranch) {
      await app.GitCheckoutBranch(name)
      return
    }
    throw new Error('microkernel not connected: GitCheckoutBranch unavailable')
  },

  async gitCreateBranch(name: string): Promise<void> {
    const app = getApp()
    if (app?.GitCreateBranch) {
      await app.GitCreateBranch(name)
      return
    }
    throw new Error('microkernel not connected: GitCreateBranch unavailable')
  },

  async gitListSnapshots(): Promise<{ id: string; branch: string; message: string; time: string }[]> {
    const app = getApp()
    if (app?.GitListSnapshots) {
      const r = await app.GitListSnapshots()
      return Array.isArray(r) ? r : []
    }
    return []
  },

  async gitCreateSnapshot(msg: string): Promise<void> {
    const app = getApp()
    if (app?.GitCreateSnapshot) {
      await app.GitCreateSnapshot(msg)
      return
    }
    throw new Error('microkernel not connected: GitCreateSnapshot unavailable')
  },

  async gitPull(): Promise<string> {
    const app = getApp()
    if (app?.GitPull) return await app.GitPull()
    throw new Error('microkernel not connected: GitPull unavailable')
  },

  async suggestCommitMessage(): Promise<string> {
    const app = getApp()
    if (app?.SuggestCommitMessage) return await app.SuggestCommitMessage()
    throw new Error('microkernel not connected: SuggestCommitMessage unavailable')
  },

  async gitPush(): Promise<string> {
    const app = getApp()
    if (app?.GitPush) return await app.GitPush()
    throw new Error('microkernel not connected: GitPush unavailable')
  },

  async gitRestoreSnapshot(id: string): Promise<void> {
    const app = getApp()
    if (app?.GitRestoreSnapshot) {
      await app.GitRestoreSnapshot(id)
      return
    }
    throw new Error('microkernel not connected: GitRestoreSnapshot unavailable')
  },

  async runSecurityAudit(): Promise<{ status?: string; output?: string; issues?: any[] }> {
    const app = getApp()
    if (app?.RunSecurityAudit) return await app.RunSecurityAudit()
    throw new Error('microkernel not connected: RunSecurityAudit unavailable')
  },

  async getUsageMetrics(): Promise<{
    total_tokens: number
    total_calls: number
    estimated_cost: string
    active_sessions: number
    last_updated_time: string
  }> {
    const app = getApp()
    if (app?.GetUsageMetrics) return await app.GetUsageMetrics()
    return { total_tokens: 0, total_calls: 0, estimated_cost: '$0', active_sessions: 0, last_updated_time: '' }
  },

  // 4. 渠道与设置管理
  async listChannels(): Promise<ChannelConfig[]> {
    const app = getApp()
    if (app?.ListChannels) {
      return await app.ListChannels()
    }
    return []
  },

  async saveChannel(cfg: ChannelConfig): Promise<void> {
    const app = getApp()
    if (app?.SaveChannel) {
      await app.SaveChannel(cfg)
      return
    }
    throw new Error('microkernel not connected: SaveChannel unavailable')
  },

  async deleteChannel(id: string): Promise<void> {
    const app = getApp()
    if (app?.DeleteChannel) {
      await app.DeleteChannel(id)
      return
    }
    throw new Error('microkernel not connected: DeleteChannel unavailable')
  },

  async pingChannel(id: string): Promise<string> {
    const app = getApp()
    if (app?.PingChannel) return await app.PingChannel(id)
    return 'timeout'
  },

  async listMCPs(): Promise<MCPServerConfig[]> {
    const app = getApp()
    if (app?.ListMCPs) return await app.ListMCPs()
    return []
  },

  async saveMCP(cfg: MCPServerConfig): Promise<void> {
    const app = getApp()
    if (app?.SaveMCP) {
      await app.SaveMCP(cfg)
      return
    }
    throw new Error('microkernel not connected: SaveMCP unavailable')
  },

  async deleteMCP(id: string): Promise<void> {
    const app = getApp()
    if (app?.DeleteMCP) {
      await app.DeleteMCP(id)
      return
    }
    throw new Error('microkernel not connected: DeleteMCP unavailable')
  },

  async testMCPServer(id: string): Promise<MCPTestResult> {
    const app = getApp()
    if (app?.TestMCPServer) return await app.TestMCPServer(id)
    return {
      id,
      name: id,
      status: 'ERROR',
      latency: '0ms',
      tool_count: 0,
      tools: [],
      error: 'MCP 服务未连接'
    }
  },

  async getADR(nodeID: string): Promise<string> {
    const app = getApp()
    if (app?.GetADR) return await app.GetADR(nodeID)
    return ''
  },

  async saveADR(nodeID: string, note: string): Promise<void> {
    const app = getApp()
    if (app?.SaveADR) {
      await app.SaveADR(nodeID, note)
      return
    }
    throw new Error('microkernel not connected: SaveADR unavailable')
  },

  async diagnoseFile(relPath: string): Promise<DiagnosticReport | null> {
    const app = getApp()
    if (app?.DiagnoseFile) return await app.DiagnoseFile(relPath)
    return null
  },

  async listSkills(): Promise<SkillConfig[]> {
    const app = getApp()
    if (app?.ListSkills) return await app.ListSkills()
    return []
  },

  async saveSkill(cfg: SkillConfig): Promise<void> {
    const app = getApp()
    if (app?.SaveSkill) {
      await app.SaveSkill(cfg)
      return
    }
    throw new Error('microkernel not connected: SaveSkill unavailable')
  },

  async deleteSkill(id: string): Promise<void> {
    const app = getApp()
    if (app?.DeleteSkill) {
      await app.DeleteSkill(id)
      return
    }
    throw new Error('microkernel not connected: DeleteSkill unavailable')
  },

  async importSkillFromDialog(): Promise<SkillConfig | null> {
    const app = getApp()
    if (app?.ImportSkillFromDialog) return await app.ImportSkillFromDialog()
    return null
  },

  async importSkillMarkdown(filePath: string): Promise<SkillConfig | null> {
    const app = getApp()
    if (app?.ImportSkillMarkdown) return await app.ImportSkillMarkdown(filePath)
    return null
  },

  async listRules(): Promise<RuleConfig[]> {
    const app = getApp()
    if (app?.ListRules) return await app.ListRules()
    return []
  },

  async saveRule(cfg: RuleConfig): Promise<void> {
    const app = getApp()
    if (app?.SaveRule) {
      await app.SaveRule(cfg)
      return
    }
    throw new Error('microkernel not connected: SaveRule unavailable')
  },

  async deleteRule(id: string): Promise<void> {
    const app = getApp()
    if (app?.DeleteRule) {
      await app.DeleteRule(id)
      return
    }
    throw new Error('microkernel not connected: DeleteRule unavailable')
  },

  async getUIPrefs(): Promise<{ theme: string; monaco_font: string; monaco_size: number }> {
    const app = getApp()
    if (app?.GetUIPrefs) return await app.GetUIPrefs()
    return { theme: 'warm', monaco_font: 'JetBrains Mono', monaco_size: 14 }
  },

  // 5. 热插拔插件中心与 DSH 算子大盘
  async getHotplugDashboard(): Promise<HotplugDashboardReport> {
    const app = getApp()
    if (app?.GetHotplugDashboard) {
      return await app.GetHotplugDashboard()
    }
    return {
      summary: {
        total_tools: 0,
        active_mcps: 0,
        active_rails: 0,
        active_providers: 0,
        healthy_count: 0
      },
      tools: [],
      mcps: [],
      rails: [],
      providers: [],
      updated_at: Date.now()
    }
  },

  async reloadHotplugRegistry(): Promise<HotplugDashboardReport> {
    const app = getApp()
    if (app?.ReloadHotplugRegistry) {
      return await app.ReloadHotplugRegistry()
    }
    return await this.getHotplugDashboard()
  },

  async probeHotplugItem(itemId: string, itemType: string): Promise<HotplugItemInfo> {
    const app = getApp()
    if (app?.ProbeHotplugItem) {
      return await app.ProbeHotplugItem(itemId, itemType)
    }
    throw new Error('microkernel not connected: ProbeHotplugItem unavailable')
  },

  async exportHotplugManifest(): Promise<string> {
    const app = getApp()
    if (app?.ExportHotplugManifest) {
      return await app.ExportHotplugManifest()
    }
    throw new Error('microkernel not connected: ExportHotplugManifest unavailable')
  },

  async saveUIPrefs(p: { theme: string; monaco_font: string; monaco_size: number }): Promise<void> {
    const app = getApp()
    if (app?.SaveUIPrefs) {
      await app.SaveUIPrefs(p)
      return
    }
    throw new Error('microkernel not connected: SaveUIPrefs unavailable')
  },

  async importWorkspaceRules(): Promise<number> {
    const app = getApp()
    if (app?.ImportWorkspaceRules) return await app.ImportWorkspaceRules()
    throw new Error('microkernel not connected: ImportWorkspaceRules unavailable')
  },

  async getSandboxStatus(): Promise<{ path_isolation: boolean; dangerous_command: boolean; secret_strip: boolean; workspace: string }> {
    const app = getApp()
    if (app?.GetSandboxStatus) return await app.GetSandboxStatus()
    return { path_isolation: false, dangerous_command: true, secret_strip: true, workspace: '' }
  },

  async getRuntimeInfo(): Promise<{ product: string; version: string; os: string; arch: string; go_version: string; workspace: string; data_dir: string; webview: string }> {
    const app = getApp()
    if (app?.GetRuntimeInfo) return await app.GetRuntimeInfo()
    return { product: '湉码', version: '0.0.1', os: '', arch: '', go_version: '', workspace: '', data_dir: '', webview: '' }
  },

  async exportDiagnostics(): Promise<string> {
    const app = getApp()
    if (app?.ExportDiagnostics) return await app.ExportDiagnostics()
    throw new Error('microkernel not connected: ExportDiagnostics unavailable')
  },

  async checkForUpdates(): Promise<string> {
    const app = getApp()
    if (app?.CheckForUpdates) return await app.CheckForUpdates()
    throw new Error('microkernel not connected: CheckForUpdates unavailable')
  },

  async listSkillTemplates(): Promise<SkillConfig[]> {
    const app = getApp()
    if (app?.ListSkillTemplates) return await app.ListSkillTemplates()
    return []
  },

  async installSkillTemplate(id: string): Promise<void> {
    const app = getApp()
    if (app?.InstallSkillTemplate) {
      await app.InstallSkillTemplate(id)
      return
    }
    throw new Error('microkernel not connected: InstallSkillTemplate unavailable')
  },

  async getFileTree(dir: string = ''): Promise<FileNode[]> {
    const app = getApp()
    if (app?.GetFileTree) return await app.GetFileTree(dir)
    return []
  },

  async searchWorkspace(action: 'grep' | 'find', query: string, maxResults: number = 40): Promise<SearchMatch[]> {
    const app = getApp()
    if (app?.SearchWorkspace) return await app.SearchWorkspace(action, query, maxResults)
    return []
  },

  async getGitStatus(): Promise<any> {
    const app = getApp()
    if (app?.GetGitStatus) return await app.GetGitStatus()
    return { branch: '', staged: [], working: [], untracked: [] }
  },

  async runTDDValidation(): Promise<{ status: string; passed: number; failed: number; failed_tests?: string[]; output: string }> {
    const app = getApp()
    if (app?.RunTDDValidation) return await app.RunTDDValidation()
    throw new Error('microkernel not connected: RunTDDValidation unavailable')
  },

  async getProjectASTGraph(): Promise<GraphNode[]> {
    const app = getApp()
    if (app?.GetProjectASTGraph) return await app.GetProjectASTGraph()
    return []
  },

  async getArchitectureReport(customPath?: string): Promise<ArchitectureReport> {
    const app = getApp()
    if (app?.GetArchitectureReport) return await app.GetArchitectureReport(customPath || '')
    throw new Error('microkernel not connected: GetArchitectureReport unavailable')
  },

  async getBlastRadiusReport(customPath: string, targetSymbol: string): Promise<BlastRadiusReport> {
    const app = getApp()
    if (app?.GetBlastRadiusReport) return await app.GetBlastRadiusReport(customPath || '', targetSymbol)
    throw new Error('microkernel not connected: GetBlastRadiusReport unavailable')
  },

  async discoverWorkspaceGoModules(): Promise<GoModuleInfo[]> {
    const app = getApp()
    if (app?.DiscoverWorkspaceGoModules) return await app.DiscoverWorkspaceGoModules()
    return []
  },

  async readFile(relPath: string): Promise<string> {
    const app = getApp()
    if (app?.ReadFile) return await app.ReadFile(relPath)
    throw new Error('microkernel not connected: ReadFile unavailable')
  },

  async writeFile(relPath: string, content: string): Promise<void> {
    const app = getApp()
    if (app?.WriteFile) {
      await app.WriteFile(relPath, content)
      return
    }
    throw new Error('microkernel not connected: WriteFile unavailable')
  },

  // 5. 真实流式对话调用与事件订阅
  async sendMessage(
    req: { session_id: string; prompt: string; model: string; is_full_auto: boolean; strategy?: string; strategy_note?: string },
    callbacks: {
      onThinking?: (text: string) => void
      onChunk?: (delta: string) => void
      onToolStart?: (tool: string, args: any, tcId?: string, turn?: number) => void
      onToolEnd?: (tool: string, output: string, tcId?: string, turn?: number) => void
      onDone?: () => void
      onDiagnostic?: (file: string, errors: DiagnosticItem[]) => void
      onFilesChanged?: (file: string) => void
      onChoice?: (data: any) => void
      onConfirm?: (payload: any) => void
    }
  ): Promise<void> {
    const runtime = getRuntime()
    const app = getApp()

    if (runtime && app?.SendMessage) {
      // 治理内存泄漏与 Chunk 重复打印：清理残留监听器
      const agentEvents = [
        'agent:start',
        'agent:thinking',
        'agent:chunk',
        'agent:tool_start',
        'agent:tool_end',
        'agent:files_changed',
        'agent:done',
        'agent:complete',
        'agent:interrupted',
        'agent:choice',
        'agent:confirm',
        'lsp:diagnostic'
      ]

      try {
        if (runtime.EventsOff) {
          for (const ev of agentEvents) {
            runtime.EventsOff(ev)
          }
        }
      } catch (_) {}

      const cleanAll = () => {
        try {
          if (runtime.EventsOff) {
            for (const ev of agentEvents) {
              runtime.EventsOff(ev)
            }
          }
        } catch (_) {}
      }

      runtime.EventsOn('agent:thinking', (data: any) => {
        if (data.session_id === req.session_id && callbacks.onThinking) {
          callbacks.onThinking(data.thinking)
        }
      })
      runtime.EventsOn('agent:chunk', (data: any) => {
        if (data.session_id === req.session_id && callbacks.onChunk) {
          callbacks.onChunk(data.delta)
        }
      })
      runtime.EventsOn('agent:tool_start', (data: any) => {
        if (data.session_id === req.session_id && callbacks.onToolStart) {
          callbacks.onToolStart(data.tool, data.args, data.id, data.turn)
        }
      })
      runtime.EventsOn('agent:tool_end', (data: any) => {
        if (data.session_id === req.session_id && callbacks.onToolEnd) {
          callbacks.onToolEnd(data.tool, data.output, data.id, data.turn)
        }
      })
      runtime.EventsOn('agent:files_changed', (data: any) => {
        if (data.session_id === req.session_id && callbacks.onFilesChanged) {
          callbacks.onFilesChanged(data.file)
        }
      })
      runtime.EventsOn('agent:choice', (data: any) => {
        if (data.session_id === req.session_id && callbacks.onChoice) {
          callbacks.onChoice(data)
        }
      })
      runtime.EventsOn('agent:confirm', (data: any) => {
        if (data.session_id === req.session_id && callbacks.onConfirm) {
          callbacks.onConfirm(data)
        }
      })
      runtime.EventsOn('lsp:diagnostic', (data: any) => {
        if (callbacks.onDiagnostic && Array.isArray(data?.errors)) {
          callbacks.onDiagnostic(data.file || '', data.errors)
        }
      })
      const handleFinish = (data: any) => {
        if (!data?.session_id || data.session_id === req.session_id) {
          cleanAll()
          if (callbacks.onDone) callbacks.onDone()
        }
      }
      runtime.EventsOn('agent:done', handleFinish)
      runtime.EventsOn('agent:complete', handleFinish)
      runtime.EventsOn('agent:interrupted', handleFinish)

      try {
        await app.SendMessage(req)
      } catch (err) {
        cleanAll()
        throw err
      }
      return
    }

    // 纯前端或非 Wails 环境：Fail-Closed 提示
    if (callbacks.onChunk) {
      callbacks.onChunk('\n[提示] 当前不在 湉码 桌面端（Wails 未连接），无法调用真实微内核。')
    }
    if (callbacks.onDone) callbacks.onDone()
    return
  },

  // 6. 沉浸式无边框窗口原生控制
  windowMinimise(): void {
    const runtime = getRuntime()
    if (runtime?.WindowMinimise) {
      runtime.WindowMinimise()
    }
  },
  windowToggleMaximise(): void {
    const runtime = getRuntime()
    if (runtime?.WindowToggleMaximise) {
      runtime.WindowToggleMaximise()
    }
  },
  windowClose(): void {
    const runtime = getRuntime()
    if (runtime?.Quit) {
      runtime.Quit()
    } else if (runtime?.WindowClose) {
      runtime.WindowClose()
    }
  }
}
