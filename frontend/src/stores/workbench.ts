import { defineStore } from 'pinia'
import { ref, reactive, computed, nextTick, watch } from 'vue'
import {
  wailsBridge,
  type SessionMeta,
  type ChatSession,
  type FileNode,
  type DiffReport,
  type ChannelConfig,
  type MCPServerConfig,
  type SkillConfig,
  type RuleConfig,
  type GraphNode,
  type DiagnosticItem,
  type SearchMatch,
  type HotplugItemInfo,
  type HotplugDashboardReport,
  type ArchitectureReport,
  type PackageNode,
  type BlastRadiusReport,
  type GoModuleInfo
} from '../core/wailsBridge'
import { renderMarkdown } from '../core/markdown'

export interface EditorTabItem {
  path: string
  title: string
  dirty?: boolean
  content?: string
}

export const useWorkbenchStore = defineStore('workbench', () => {
// 1. 活动栏与工作区状态
const activeActivity = ref('chat')
const isDiffOpen = ref(true)
const workspaceView = ref<'chat' | 'split' | 'editor'>('split')

function setWorkspaceView(v: 'chat' | 'split' | 'editor') {
  workspaceView.value = v
  isDiffOpen.value = v !== 'chat'
  if (v !== 'chat') void loadFileTree()
}

async function suggestCommitMessage() {
  try {
    commitMessage.value = await wailsBridge.suggestCommitMessage()
    showToast('✓ 已根据 git status / diff --stat 生成提交说明')
  } catch (err) {
    showToast('无法生成提交说明: ' + err)
  }
}
const activeDiffFile = ref('')
const isSettingsOpen = ref(false)
const isKnowledgeGraphOpen = ref(false)
const isChannelModalOpen = ref(false)
const isMcpModalOpen = ref(false)
const isSkillModalOpen = ref(false)
const isRuleModalOpen = ref(false)
const activeSettingsTab = ref('models')
const isStreaming = ref(false)
const isCommandPaletteOpen = ref(false)
const commandPaletteQuery = ref('')
const commandPaletteIndex = ref(0)
const isConstitutionModalOpen = ref(false)
const isHotplugDashboardOpen = ref(false)
const hotplugReport = ref<HotplugDashboardReport | null>(null)
const isHotplugLoading = ref(false)
const hotplugActiveTab = ref<'overview' | 'tools' | 'mcps' | 'rails' | 'providers' | 'creator'>('tools')
const hotplugSearchQuery = ref('')
const probedItems = reactive<Record<string, HotplugItemInfo>>({})

const activeConstitution = computed(() => {
  const activeRules = rules.value.filter(r => r.enabled)
  const activeSkills = skills.value.filter(s => s.enabled)
  return {
    ruleCount: activeRules.length,
    skillCount: activeSkills.length,
    total: activeRules.length + activeSkills.length,
    rules: activeRules,
    skills: activeSkills
  }
})

const isGraphLoading = ref(false)
const isFileTreeLoading = ref(false)
const isGitLoading = ref(false)

const toastMessage = ref('')
function showToast(msg: string) {
  toastMessage.value = msg
  setTimeout(() => {
    toastMessage.value = ''
  }, 2500)
}

// 2. 真实会话管理 (读写 ~/.tiancode/sessions/)
const sessions = ref<SessionMeta[]>([])
const projects = ref<{ path: string; name: string; opened_at: number }[]>([])
const sessionSearch = ref('')
const collapsedProjects = reactive<Record<string, boolean>>({})
const activeTag = ref('全部')

function samePath(a?: string, b?: string) {
  const n = (p?: string) => (p || '').replace(/\\/g, '/').replace(/\/+$/, '').toLowerCase()
  return n(a) === n(b)
}

const projectTree = computed(() => {
  const q = sessionSearch.value.trim().toLowerCase()
  const tag = activeTag.value
  const rows = projects.value.map((p) => {
    let items = sessions.value.filter((s) => samePath(s.workspace, p.path))
    if (tag && tag !== '全部') {
      items = items.filter((s) => (s.tag || '') === tag)
    }
    if (q) {
      items = items.filter((s) => `${s.title} ${s.desc}`.toLowerCase().includes(q))
    }
    return { ...p, sessions: items }
  })
  if (!q) return rows
  return rows.filter((r) => r.sessions.length > 0 || samePath(r.path, workspacePath.value))
})
const currentSessionId = ref('')
const selectedModel = ref('')
const pendingChoice = ref<any>(null)
const pendingChoiceSelected = ref('')
const pendingChoiceCustomNote = ref('')
const pendingConfirm = ref<any>(null)

const currentSession = ref<ChatSession>({
  id: '',
  title: '新对话',
  model: '',
  tag: '',
  workspace: '',
  created_at: Date.now(),
  updated_at: Date.now(),
  messages: []
})

const currentTaskStatus = computed(() => {
  if (isStreaming.value) return 'running'
  if (pendingDiffFiles.value.length > 0) return 'pending_diff'
  return currentSession.value?.task?.status || 'idle'
})

const availableTags = computed(() => {
  const set = new Set<string>()
  sessions.value.forEach(s => {
    if (s.tag && s.tag.trim()) set.add(s.tag.trim())
  })
  return ['全部', ...Array.from(set)]
})

const filteredSessions = computed(() => {
  if (activeTag.value === '全部') return sessions.value
  return sessions.value.filter(s => (s.tag || '') === activeTag.value)
})

const historyCap = 80
const showFullHistory = ref(false)
const hiddenHistoryCount = computed(() => {
  const n = (currentSession.value.messages || []).length
  if (showFullHistory.value || n <= historyCap) return 0
  return n - historyCap
})
const visibleMessages = computed(() => {
  const msgs = currentSession.value.messages || []
  if (showFullHistory.value || msgs.length <= historyCap) return msgs
  return msgs.slice(-historyCap)
})
function revealFullHistory() {
  showFullHistory.value = true
}

const upstreamFetchedModels = ref<string[]>([])

const availableModels = computed(() => {
  const set = new Set<string>()
  upstreamFetchedModels.value.forEach(m => {
    if (m && m.trim()) set.add(m.trim())
  })
  channels.value.forEach(c => {
    if (c.model && c.model.trim()) set.add(c.model.trim())
    ;(c.extra_models || []).forEach((m) => {
      if (m && m.trim()) set.add(m.trim())
    })
  })
  return Array.from(set)
})

const uiPrefs = reactive({ theme: 'warm', monaco_font: 'JetBrains Mono', monaco_size: 14 })
const sandboxStatus = reactive({ path_isolation: false, dangerous_command: true, secret_strip: true, workspace: '' })
const runtimeInfo = reactive({ product: '湉码', version: '0.0.1', os: '', arch: '', go_version: '', workspace: '', data_dir: '', webview: '' })
const skillTemplates = ref<SkillConfig[]>([])
const extraModelsInput = ref('')

function applyTheme(theme: string) {
  const dark = theme === 'dark' || (theme === 'system' && window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.dataset.theme = dark ? 'dark' : 'warm'
}

async function persistUIPrefs() {
  await wailsBridge.saveUIPrefs({
    theme: uiPrefs.theme,
    monaco_font: uiPrefs.monaco_font,
    monaco_size: uiPrefs.monaco_size
  })
  applyTheme(uiPrefs.theme)
  showToast('✓ 外观偏好已写入 ~/.tiancode/ui_prefs.json')
}

async function importWorkspaceRulesAction() {
  try {
    const n = await wailsBridge.importWorkspaceRules()
    await loadSettingsData()
    showToast(n > 0 ? `✓ 已从工作区导入 ${n} 条规则` : '工作区根目录没有 .cursorrules / AGENTS.md / CLAUDE.md')
  } catch (err) {
    showToast('导入规则失败: ' + err)
  }
}

async function installSkillTemplateAction(id: string) {
  await wailsBridge.installSkillTemplate(id)
  await loadSettingsData()
  showToast('✓ 技能模板已写入 ~/.tiancode/skills.json')
}

async function checkUpdatesAction() {
  try {
    showToast(await wailsBridge.checkForUpdates())
  } catch (err) {
    showToast('检查更新失败: ' + err)
  }
}

async function exportDiagnosticsAction() {
  try {
    const p = await wailsBridge.exportDiagnostics()
    showToast('✓ 诊断已导出: ' + p)
  } catch (err) {
    showToast('导出失败: ' + err)
  }
}

async function loadSessionsList() {
  try {
    const list = await wailsBridge.listSessions()
    sessions.value = list || []
  } catch (err) {
    console.error('Failed to load sessions:', err)
    sessions.value = []
  }
}

async function loadProjects() {
  try {
    projects.value = await wailsBridge.listProjects()
  } catch (err) {
    console.error(err)
    projects.value = []
  }
}

async function applyWorkspace(path: string) {
  await wailsBridge.setWorkspace(path)
  workspacePath.value = path
  await Promise.all([loadFileTree(), loadGitStatus(), loadSessionsList(), loadProjects()])
}

async function activateSession(id: string, workspace?: string) {
  if (workspace && !samePath(workspace, workspacePath.value)) {
    await applyWorkspace(workspace)
  }
  await selectSession(id)
}

async function createSessionInProject(path: string) {
  if (!samePath(path, workspacePath.value)) {
    await applyWorkspace(path)
  }
  await createNewSession()
}

async function openProjectFolder() {
  try {
    const selected = await wailsBridge.openDirectoryDialog()
    if (!selected) return
    await wailsBridge.addProject(selected)
    await applyWorkspace(selected)
    const mine = sessions.value.filter((s) => samePath(s.workspace, selected))
    if (mine.length > 0) {
      await selectSession(mine[0].id)
    } else {
      await createNewSession()
    }
  } catch (err: any) {
    showToast(`打开项目失败: ${err}`)
  }
}

async function unpinProject(path: string) {
  try {
    await wailsBridge.removeProject(path)
    await loadProjects()
    if (samePath(path, workspacePath.value)) {
      if (projects.value.length > 0) {
        await applyWorkspace(projects.value[0].path)
        const mine = sessions.value.filter((s) => samePath(s.workspace, workspacePath.value))
        if (mine.length > 0) await selectSession(mine[0].id)
        else await createNewSession()
      }
    }
  } catch (err: any) {
    showToast(`移除项目失败: ${err}`)
  }
}

function toggleProjectCollapse(path: string) {
  collapsedProjects[path] = !collapsedProjects[path]
}

async function selectSession(id: string) {
  showFullHistory.value = false
  currentSessionId.value = id
  try {
    const sess = await wailsBridge.getSession(id)
    if (sess) {
      currentSession.value = sess
      if (sess.model) selectedModel.value = sess.model
      ensureSessionTab(sess.id, sess.title)
    }
    showToast(`✓ 已载入会话: ${currentSession.value.title}`)
    followLatestChat()
  } catch (err) {
    console.error('Failed to load session:', err)
  }
}

async function createNewSession() {
  if (isStreaming.value) {
    await stopGenerationAction()
  }
  currentSessionId.value = ''
  currentSession.value = emptyDraft()
  showFullHistory.value = false
}

async function deleteSession(id: string) {
  if (currentSessionId.value === id && isStreaming.value) {
    await stopGenerationAction()
  }
  await wailsBridge.deleteSession(id)
  await loadSessionsList()
  if (currentSessionId.value === id) {
    const mine = sessions.value.filter((s) => samePath(s.workspace, workspacePath.value))
    if (mine.length > 0) {
      await selectSession(mine[0].id)
    } else {
      await createNewSession()
    }
  }
}

// 3. 真实工作区、文件树与 Git 状态
const workspacePath = ref('')

function emptyDraft(): ChatSession {
  return {
    id: '',
    title: '新对话',
    model: selectedModel.value,
    tag: '',
    workspace: workspacePath.value,
    created_at: Date.now(),
    updated_at: Date.now(),
    messages: []
  }
}

const workspaceName = computed(() => {
  if (!workspacePath.value) return '湉码'
  const normalized = workspacePath.value.replace(/\\/g, '/')
  const parts = normalized.split('/').filter(Boolean)
  return parts[parts.length - 1] || 'Workspace'
})

async function chooseWorkspace() {
  await openProjectFolder()
}

const fileTree = ref<FileNode[]>([])
const expandedFolders = reactive<Record<string, boolean>>({ 'frontend': true })
const gitStatus = ref<any>({ branch: '', working: [], staged: [], untracked: [] })
const gitBranchLabel = computed(() => {
  const b = (gitStatus.value?.branch || '').trim()
  return b || '无 Git'
})
const commitMessage = ref('')

const stagedTreeFiles = computed(() => {
  const list: { path: string; type: string; color: string }[] = []
  const seen = new Set<string>()
  if (gitStatus.value?.staged && Array.isArray(gitStatus.value.staged)) {
    for (const f of gitStatus.value.staged) {
      const p = typeof f === 'string' ? f.trim() : (f?.path || '').trim()
      if (p && !seen.has(p)) {
        seen.add(p)
        const type = typeof f === 'object' ? (f.staged_code || f.index_code || 'M') : 'M'
        list.push({
          path: p,
          type: type,
          color: type === 'D' ? 'text-red-500' : 'text-[#10A37F]'
        })
      }
    }
  }
  return list
})

const workingTreeFiles = computed(() => {
  const list: { path: string; type: string; color: string }[] = []
  const seen = new Set<string>()
  if (gitStatus.value?.working && Array.isArray(gitStatus.value.working)) {
    for (const f of gitStatus.value.working) {
      const p = typeof f === 'string' ? f.trim() : (f?.path || '').trim()
      if (p && !seen.has(p)) {
        seen.add(p)
        const workCode = typeof f === 'object' ? (f.work_code || 'M') : 'M'
        list.push({
          path: p,
          type: workCode,
          color: workCode === 'D' ? 'text-red-500' : 'text-amber-600'
        })
      }
    }
  }
  if (gitStatus.value?.untracked && Array.isArray(gitStatus.value.untracked)) {
    for (const p of gitStatus.value.untracked) {
      const pathStr = (typeof p === 'string' ? p : (p as any)?.path || '').trim()
      if (pathStr && !seen.has(pathStr)) {
        seen.add(pathStr)
        list.push({
          path: pathStr,
          type: 'U',
          color: 'text-emerald-600'
        })
      }
    }
  }
  return list
})
const pendingDiffFiles = computed(() => [...new Set(workingTreeFiles.value.map(f => f.path.trim()).filter(p => p && !p.endsWith('/')))])
watch(pendingDiffFiles, (newVal) => { if (newVal.length === 0 && isDiffOpen.value) isDiffOpen.value = false })

async function loadFileTree() {
  try {
    fileTree.value = await wailsBridge.getFileTree()
  } catch (err) {
    console.error(err)
    fileTree.value = []
  }
}

async function loadGitStatus() {
  try {
    const st = await wailsBridge.getGitStatus()
    gitStatus.value = st && typeof st === 'object'
      ? st
      : { branch: '', working: [], staged: [], untracked: [] }
  } catch (err) {
    console.error(err)
    gitStatus.value = { branch: '', working: [], staged: [], untracked: [] }
  }
  void loadGitExtras()
}

const gitBranches = ref<string[]>([])
const gitCurrentBranch = ref('')
const gitSnapshots = ref<{ id: string; branch: string; message: string; time: string }[]>([])
const newBranchName = ref('')

async function loadGitExtras() {
  try {
    const b = await wailsBridge.getGitBranches()
    gitBranches.value = b.branches || []
    gitCurrentBranch.value = b.current || (gitStatus.value?.branch || '')
  } catch {
    gitBranches.value = []
    gitCurrentBranch.value = gitStatus.value?.branch || ''
  }
  try {
    gitSnapshots.value = await wailsBridge.gitListSnapshots()
  } catch {
    gitSnapshots.value = []
  }
}

async function checkoutBranch(name: string) {
  if (!name) return
  try {
    await wailsBridge.gitCheckoutBranch(name)
    await loadGitStatus()
    showToast('✓ 已切换到分支 ' + name)
  } catch (err) {
    showToast('切换分支失败: ' + err)
  }
}

async function createBranchAction() {
  const n = newBranchName.value.trim()
  if (!n) {
    showToast('请填写新分支名')
    return
  }
  try {
    await wailsBridge.gitCreateBranch(n)
    newBranchName.value = ''
    await loadGitStatus()
    showToast('✓ 已创建并检出 ' + n)
  } catch (err) {
    showToast('创建分支失败: ' + err)
  }
}

async function createSnapshotAction() {
  try {
    await wailsBridge.gitCreateSnapshot('checkpoint')
    await loadGitExtras()
    showToast('✓ 已暂存当前工作区修改 (git stash)')
  } catch (err) {
    showToast('储藏暂存失败: ' + err)
  }
}

async function restoreSnapshotAction(id: string) {
  try {
    await wailsBridge.gitRestoreSnapshot(id)
    await loadGitStatus()
    showToast('✓ 已恢复暂存修改 (git stash pop)')
  } catch (err) {
    showToast('恢复暂存失败: ' + err)
  }
}

function switchToFileActivity() {
  activeActivity.value = 'chat'
  setWorkspaceView('split')
}

async function gitPullAction() {
  try {
    const out = await wailsBridge.gitPull()
    await loadGitStatus()
    showToast('✓ git pull: ' + (out || 'ok').slice(0, 80))
  } catch (err) {
    showToast('git pull 失败: ' + err)
  }
}

async function gitPushAction() {
  try {
    const out = await wailsBridge.gitPush()
    showToast('✓ git push: ' + (out || 'ok').slice(0, 80))
  } catch (err) {
    showToast('git push 失败: ' + err)
  }
}

async function setSessionTag(id: string, tag: string) {
  try {
    const sess = await wailsBridge.getSession(id)
    if (!sess) return
    sess.tag = tag.trim()
    await wailsBridge.updateSessionTag(sess.id, sess.tag)
    await loadSessionsList()
    if (currentSessionId.value === id) currentSession.value.tag = sess.tag
  } catch (err) {
    showToast('打标签失败: ' + err)
  }
}

function switchToGitActivity() {
  activeActivity.value = 'git'
  void loadGitStatus()
}

async function handleFileClick(node: FileNode) {
  if (node.is_dir) {
    expandedFolders[node.path] = !expandedFolders[node.path]
    // 若点击展开且尚未加载过子级，按需调用底层接口获取直接子项，避免一次性扫描大项目
    if (expandedFolders[node.path] && !node.loaded && (!node.children || node.children.length === 0)) {
      node.loading = true
      try {
        const subNodes = await wailsBridge.getFileTree(node.path)
        node.children = subNodes || []
        node.loaded = true
      } catch (err) {
        showToast('加载目录内容失败: ' + err)
      } finally {
        node.loading = false
      }
    }
  } else {
    editorView.value = 'edit'
    void openFileDiff(node.path)
  }
}

async function handleGitCommit() {
  if (!commitMessage.value.trim()) return
  try {
    await wailsBridge.gitCommit(commitMessage.value.trim())
    commitMessage.value = ''
    await loadGitStatus()
    showToast('✓ Git 变更已成功提交本地仓库！')
  } catch (err) {
    showToast('提交异常: ' + err)
  }
}

// 4. 真实物理代码 Diff
const diffReport = ref<DiffReport | null>(null)

const openEditorTabs = ref<EditorTabItem[]>([])
const fileTreeFilter = ref('')
const isPendingDiffPromptOpen = ref(false)
const forceSendWithPendingDiff = ref(false)

const explorerTab = ref<'tree' | 'search'>('tree')
const searchQuery = ref('')
const searchAction = ref<'grep' | 'find'>('find')
const searchResults = ref<SearchMatch[]>([])
const searchError = ref('')
const isSearching = ref(false)
const targetEditorLine = ref<number | undefined>(undefined)

async function runWorkspaceSearch(action?: 'grep' | 'find', query?: string) {
  if (action) searchAction.value = action
  if (query !== undefined) searchQuery.value = query
  const q = searchQuery.value.trim()
  if (!q) {
    searchResults.value = []
    searchError.value = ''
    return
  }
  explorerTab.value = 'search'
  isSearching.value = true
  searchError.value = ''
  try {
    searchResults.value = await wailsBridge.searchWorkspace(searchAction.value, q, 50)
  } catch (err) {
    searchError.value = String(err)
    showToast('检索失败: ' + err)
    searchResults.value = []
  } finally {
    isSearching.value = false
  }
}

function searchFromFilter() {
  if (!fileTreeFilter.value.trim()) return
  searchQuery.value = fileTreeFilter.value.trim()
  searchAction.value = 'find'
  void runWorkspaceSearch()
}

const gitStatusMap = computed(() => {
  const map: Record<string, { code: string; color: string }> = {}
  for (const f of stagedTreeFiles.value) {
    if (f.path) map[f.path] = { code: f.type, color: 'text-[#10A37F]' }
  }
  for (const f of workingTreeFiles.value) {
    if (f.path && !map[f.path]) map[f.path] = { code: f.type, color: 'text-[#D96B27]' }
  }
  return map
})

function filterTreeNodes(nodes: FileNode[], query: string): FileNode[] {
  if (!query) return nodes
  const q = query.toLowerCase()
  const res: FileNode[] = []
  for (const n of nodes) {
    if (n.is_dir) {
      const filteredChildren = n.children ? filterTreeNodes(n.children, query) : []
      if (filteredChildren.length > 0 || n.name.toLowerCase().includes(q)) {
        expandedFolders[n.path] = true
        res.push({
          ...n,
          children: filteredChildren
        })
      }
    } else {
      if (n.name.toLowerCase().includes(q) || n.path.toLowerCase().includes(q)) {
        res.push(n)
      }
    }
  }
  return res
}

const displayFileTree = computed(() => {
  if (!fileTreeFilter.value.trim()) return fileTree.value
  return filterTreeNodes(fileTree.value, fileTreeFilter.value.trim())
})

const editorView = ref<'edit' | 'diff'>('edit')
const editorContent = ref('')
const editorDirty = ref(false)
const editorDiagnostics = ref<DiagnosticItem[]>([])
const adrNote = ref('')
const sessionTabs = ref<{ id: string; title: string }[]>([])
const tabContextMenu = ref<{ x: number; y: number; id: string } | null>(null)
const pendingCloseTab = ref<string | null>(null)

function markEditorDirty() {
  editorDirty.value = true
  const tab = openEditorTabs.value.find(t => t.path === activeDiffFile.value)
  if (tab) {
    tab.dirty = true
    tab.content = editorContent.value
  }
}

async function loadEditor() {
  if (!activeDiffFile.value) {
    editorContent.value = ''
    editorDirty.value = false
    return
  }
  const tab = openEditorTabs.value.find(t => t.path === activeDiffFile.value)
  if (tab && tab.content !== undefined) {
    editorContent.value = tab.content
    editorDirty.value = !!tab.dirty
    await refreshDiagnostics(activeDiffFile.value)
    return
  }
  try {
    const diskContent = await wailsBridge.readFile(activeDiffFile.value)
    editorContent.value = diskContent
    editorDirty.value = false
    if (tab) {
      tab.dirty = false
      tab.content = diskContent
    }
    await refreshDiagnostics(activeDiffFile.value)
  } catch (err) {
    editorContent.value = ''
    showToast('读取文件失败: ' + err)
  }
}

async function refreshDiagnostics(filePath: string) {
  if (!filePath) {
    editorDiagnostics.value = []
    return
  }
  try {
    const report = await wailsBridge.diagnoseFile(filePath)
    editorDiagnostics.value = report?.errors || []
    if (report?.has_errors) {
      showToast(`诊断: ${filePath} 有 ${report.error_count} 处问题`)
    }
  } catch {
    editorDiagnostics.value = []
  }
}

async function saveEditor() {
  if (!activeDiffFile.value) return
  try {
    await wailsBridge.writeFile(activeDiffFile.value, editorContent.value)
    editorDirty.value = false
    const tab = openEditorTabs.value.find(t => t.path === activeDiffFile.value)
    if (tab) {
      tab.dirty = false
      tab.content = editorContent.value
    }
    await loadDiff()
    await loadGitStatus()
    await refreshDiagnostics(activeDiffFile.value)
    showToast('✓ 已写入 ' + activeDiffFile.value)
  } catch (err) {
    showToast('保存失败: ' + err)
  }
}

function openEditorTab(filePath: string, viewMode: 'edit' | 'diff' = 'edit', line?: number) {
  if (!filePath) return
  targetEditorLine.value = line
  // 如果切换前已有活动标签页，先将当前缓冲区内容落入该标签页对象，防止切走后未保存改动丢失
  if (activeDiffFile.value) {
    const currentTab = openEditorTabs.value.find(t => t.path === activeDiffFile.value)
    if (currentTab) {
      currentTab.content = editorContent.value
      currentTab.dirty = editorDirty.value
    }
  }

  const title = filePath.split('/').pop() || filePath
  let existing = openEditorTabs.value.find(t => t.path === filePath)
  if (!existing) {
    existing = { path: filePath, title, dirty: false }
    openEditorTabs.value.push(existing)
  }
  activeDiffFile.value = filePath
  isDiffOpen.value = true
  editorView.value = viewMode

  if (existing.content !== undefined) {
    editorContent.value = existing.content
    editorDirty.value = !!existing.dirty
    void Promise.all([loadDiff(), refreshDiagnostics(filePath)])
  } else {
    void Promise.all([loadDiff(), loadEditor()])
  }
}

function switchEditorTab(filePath: string) {
  if (!filePath || filePath === activeDiffFile.value) return
  // 切换前持久化暂存当前活动 tab 的编辑状态与内容
  if (activeDiffFile.value) {
    const currentTab = openEditorTabs.value.find(t => t.path === activeDiffFile.value)
    if (currentTab) {
      currentTab.content = editorContent.value
      currentTab.dirty = editorDirty.value
    }
  }

  activeDiffFile.value = filePath
  const targetTab = openEditorTabs.value.find(t => t.path === filePath)
  if (targetTab && targetTab.content !== undefined) {
    editorContent.value = targetTab.content
    editorDirty.value = !!targetTab.dirty
    void Promise.all([loadDiff(), refreshDiagnostics(filePath)])
  } else {
    void Promise.all([loadDiff(), loadEditor()])
  }
}

function closeEditorTab(filePath: string, event?: MouseEvent) {
  if (event) event.stopPropagation()
  const tab = openEditorTabs.value.find(t => t.path === filePath)
  if (!tab) return

  // 如果包含未保存的编辑改动，弹出严谨居中的暖色确认弹窗，严禁使用原生 confirm()
  if (tab.dirty) {
    pendingCloseTab.value = filePath
    return
  }
  forceCloseEditorTab(filePath)
}

function forceCloseEditorTab(filePath: string) {
  const idx = openEditorTabs.value.findIndex(t => t.path === filePath)
  if (idx === -1) return
  openEditorTabs.value.splice(idx, 1)
  if (pendingCloseTab.value === filePath) {
    pendingCloseTab.value = null
  }
  if (activeDiffFile.value === filePath) {
    if (openEditorTabs.value.length > 0) {
      const nextIdx = Math.min(idx, openEditorTabs.value.length - 1)
      switchEditorTab(openEditorTabs.value[nextIdx].path)
    } else {
      activeDiffFile.value = ''
      editorContent.value = ''
      editorDirty.value = false
      diffReport.value = null
    }
  }
}

async function saveAndCloseEditorTab(filePath: string) {
  if (activeDiffFile.value === filePath) {
    await saveEditor()
  } else {
    const tab = openEditorTabs.value.find(t => t.path === filePath)
    if (tab && tab.content !== undefined) {
      try {
        await wailsBridge.writeFile(filePath, tab.content)
        tab.dirty = false
      } catch (err) {
        showToast('保存失败: ' + err)
        return
      }
    }
  }
  forceCloseEditorTab(filePath)
}

async function openFileDiff(filePath: string, viewMode: 'edit' | 'diff' = 'diff') {
  openEditorTab(filePath, viewMode)
}

function ensureSessionTab(id: string, title: string) {
  if (!id) return
  if (!sessionTabs.value.find((t) => t.id === id)) {
    sessionTabs.value.push({ id, title: title || id })
  } else {
    const t = sessionTabs.value.find((x) => x.id === id)
    if (t && title) t.title = title
  }
}

function closeSessionTab(id: string) {
  tabContextMenu.value = null
  sessionTabs.value = sessionTabs.value.filter((t) => t.id !== id)
  if (currentSessionId.value === id) {
    const next = sessionTabs.value[sessionTabs.value.length - 1]
    if (next) void selectSession(next.id)
    else void createNewSession()
  }
}

function closeOtherTabs(id: string) {
  tabContextMenu.value = null
  sessionTabs.value = sessionTabs.value.filter((t) => t.id === id)
  if (currentSessionId.value !== id) void selectSession(id)
}

function closeAllTabs() {
  tabContextMenu.value = null
  sessionTabs.value = []
  void createNewSession()
}

function onTabDragStart(e: DragEvent, id: string) {
  e.dataTransfer?.setData('text/tab-id', id)
}

function onTabDrop(e: DragEvent, targetId: string) {
  const id = e.dataTransfer?.getData('text/tab-id')
  if (!id || id === targetId) return
  const list = [...sessionTabs.value]
  const from = list.findIndex((t) => t.id === id)
  const to = list.findIndex((t) => t.id === targetId)
  if (from < 0 || to < 0) return
  const [item] = list.splice(from, 1)
  list.splice(to, 0, item)
  sessionTabs.value = list
}

function openTabMenu(e: MouseEvent, id: string) {
  e.preventDefault()
  tabContextMenu.value = { x: e.clientX, y: e.clientY, id }
}

async function stagePath(filePath: string, event?: MouseEvent) {
  if (event) event.stopPropagation()
  if (!filePath) return
  try {
    await wailsBridge.gitStage(filePath)
    await loadGitStatus()
    if (activeDiffFile.value === filePath) await loadDiff()
    showToast('✓ 已暂存 ' + filePath)
  } catch (err) {
    showToast('暂存失败: ' + err)
  }
}

async function revertPath(filePath: string, event?: MouseEvent) {
  if (event) event.stopPropagation()
  if (!filePath) return
  try {
    await wailsBridge.revertFile(filePath)
    await loadGitStatus()
    if (activeDiffFile.value === filePath) {
      await loadDiff()
      await loadEditor()
    }
    showToast('✓ 已还原 ' + filePath)
  } catch (err) {
    showToast('还原失败: ' + err)
  }
}

async function stageAllWorking() {
  for (const f of workingTreeFiles.value) {
    if (f.path) await wailsBridge.gitStage(f.path)
  }
  await loadGitStatus()
  showToast('✓ 已暂存全部工作区改动')
}

async function revertAllWorking() {
  for (const f of workingTreeFiles.value) {
    if (f.path) await wailsBridge.revertFile(f.path)
  }
  await loadGitStatus()
  showToast('✓ 已还原全部工作区改动')
}

async function loadDiff() {
  if (!activeDiffFile.value) {
    diffReport.value = null
    return
  }
  try {
    diffReport.value = await wailsBridge.getStructuredDiff(activeDiffFile.value)
  } catch (err) {
    console.error('Diff error:', err)
  }
}

async function unstageFileAction(filePath: string, event?: MouseEvent) {
  if (event) event.stopPropagation()
  if (!filePath) return
  try {
    await wailsBridge.gitUnstage(filePath)
    await loadGitStatus()
    if (activeDiffFile.value === filePath) {
      await loadDiff()
    }
    showToast(`✓ 已取消暂存: ${filePath}`)
  } catch (err) {
    showToast(`取消暂存异常: ${err}`)
  }
}

async function revertFileAction() {
  if (!activeDiffFile.value) return
  const target = activeDiffFile.value
  try {
    await wailsBridge.revertFile(target)
    if (currentSession.value?.task) {
      currentSession.value.task.pending_diff_files = [...pendingDiffFiles.value]
      if (pendingDiffFiles.value.length === 0 && currentSession.value.task.status === 'pending_diff') {
        currentSession.value.task.status = 'completed'
        void wailsBridge.updateTaskStatus(currentSession.value.id, 'completed')
      }
    }
    await loadDiff()
    await loadGitStatus()
    showToast(`✓ 已物理撤回 ${target} 磁盘改动 (Git Checkout)`)
    if (pendingDiffFiles.value.length === 0) {
      isDiffOpen.value = false
    }
  } catch (err) {
    showToast('撤回异常: ' + err)
  }
}

async function stageFileAction() {
  if (!activeDiffFile.value) return
  const target = activeDiffFile.value
  try {
    await wailsBridge.gitStage(target)
    if (currentSession.value?.task) {
      currentSession.value.task.pending_diff_files = [...pendingDiffFiles.value]
      if (pendingDiffFiles.value.length === 0 && currentSession.value.task.status === 'pending_diff') {
        currentSession.value.task.status = 'completed'
        void wailsBridge.updateTaskStatus(currentSession.value.id, 'completed')
      }
    }
    showToast(`✓ 已成功采纳并暂存变更: ${target}`)
    await loadDiff()
    await loadGitStatus()
    if (pendingDiffFiles.value.length === 0) {
      isDiffOpen.value = false
    }
  } catch (err) {
    showToast(`采纳文件变更异常: ${err}`)
  }
}

async function revertAllPendingDiffFilesAction() {
  if (pendingDiffFiles.value.length === 0) return
  const filesToRevert = [...new Set(pendingDiffFiles.value.map(f => f.trim()).filter(Boolean))]
  const failedFiles: string[] = []

  for (const f of filesToRevert) {
    try {
      await wailsBridge.revertFile(f)
    } catch (err) {
      failedFiles.push(`${f}: ${err}`)
    }
  }

  await loadDiff()
  await loadGitStatus()

  if (currentSession.value?.task) {
    currentSession.value.task.pending_diff_files = [...pendingDiffFiles.value]
    if (pendingDiffFiles.value.length === 0 && currentSession.value.task.status === 'pending_diff') {
      currentSession.value.task.status = 'completed'
      void wailsBridge.updateTaskStatus(currentSession.value.id, 'completed')
    }
  }

  if (failedFiles.length > 0) {
    showToast(`⚠️ 部分文件撤回失败: ${failedFiles.join('; ')}`)
  } else {
    showToast(`✓ 已全部放弃并撤回 ${filesToRevert.length} 个文件的改动`)
    isDiffOpen.value = false
  }
}

async function stageAllPendingDiffFilesAction() {
  if (pendingDiffFiles.value.length === 0) return
  const filesToStage = [...new Set(pendingDiffFiles.value.map(f => f.trim()).filter(Boolean))]
  const failedFiles: string[] = []

  for (const f of filesToStage) {
    try {
      await wailsBridge.gitStage(f)
    } catch (err) {
      failedFiles.push(`${f}: ${err}`)
    }
  }

  await loadDiff()
  await loadGitStatus()

  if (currentSession.value?.task) {
    currentSession.value.task.pending_diff_files = [...pendingDiffFiles.value]
    if (pendingDiffFiles.value.length === 0 && currentSession.value.task.status === 'pending_diff') {
      currentSession.value.task.status = 'completed'
      void wailsBridge.updateTaskStatus(currentSession.value.id, 'completed')
    }
  }

  if (failedFiles.length > 0) {
    showToast(`⚠️ 部分文件采纳失败: ${failedFiles.join('; ')}`)
  } else {
    showToast(`✓ 已成功采纳并暂存全部 ${filesToStage.length} 个文件变更`)
    isDiffOpen.value = false
  }
}

async function applyHunkAction(hunkIndex: number, stageOnly: boolean = true) {
  try {
    showToast(`⏳ 正在采纳 [${activeDiffFile.value}] 第 #${hunkIndex + 1} 个变更块...`)
    await wailsBridge.applyDiffHunk(activeDiffFile.value, hunkIndex, stageOnly)
    showToast(`✓ 已成功采纳该块变更 (git apply --cached)`)
    await loadDiff()
    await loadGitStatus()
  } catch (err) {
    showToast(`采纳变更块异常: ${err}`)
  }
}

async function discardHunkAction(hunkIndex: number) {
  try {
    showToast(`⏳ 正在丢弃 [${activeDiffFile.value}] 第 #${hunkIndex + 1} 个变更块...`)
    await wailsBridge.discardDiffHunk(activeDiffFile.value, hunkIndex)
    showToast(`✓ 已成功丢弃撤销该块变更 (git apply --reverse)`)
    await loadDiff()
    await loadGitStatus()
  } catch (err) {
    showToast(`丢弃变更块异常: ${err}`)
  }
}

// 5. 对话输入与流式大模型推理
const inputPrompt = ref('')
const attachedFiles = ref<string[]>([])
const messagesContainerRef = ref<HTMLDivElement | null>(null)
const stickToBottom = ref(true)
let chatScrollRaf = 0

function isChatNearBottom(el: HTMLElement, px = 120) {
  return el.scrollHeight - el.scrollTop - el.clientHeight <= px
}

function onMessagesScroll() {
  const el = messagesContainerRef.value
  if (!el) return
  stickToBottom.value = isChatNearBottom(el)
}

function scrollChatToLatest(force = false) {
  const el = messagesContainerRef.value
  if (!el) return
  if (!force && !stickToBottom.value) return
  if (chatScrollRaf) cancelAnimationFrame(chatScrollRaf)
  chatScrollRaf = requestAnimationFrame(() => {
    chatScrollRaf = 0
    el.scrollTop = el.scrollHeight
  })
}

function followLatestChat() {
  stickToBottom.value = true
  void nextTick(() => scrollChatToLatest(true))
}
const mentionOpen = ref(false)
const mentionKind = ref<'at' | 'slash' | ''>('')
const mentionQuery = ref('')
const mentionIndex = ref(0)
const mentionSearchedFiles = ref<string[]>([])
let searchDebounceTimer: any = null

type MentionItem = { id: string; kind: string; label: string; insert: string }

const mentionItems = computed(() => {
  const q = mentionQuery.value.toLowerCase()
  const items: MentionItem[] = []
  if (mentionKind.value === 'slash') {
    items.push(
      { id: '/tdd', kind: '指令', label: '/tdd 运行工作区测试', insert: '/tdd' },
      { id: '/diff', kind: '指令', label: '/diff 打开 Git 状态', insert: '/diff' },
      { id: '/term', kind: '指令', label: '/term 打开终端', insert: '/term' }
    )
    for (const mcp of mcps.value.filter((m) => m.enabled)) {
      items.push({ id: 'mcp-' + mcp.id, kind: 'MCP', label: mcp.name, insert: '/' + mcp.name })
    }
  } else if (mentionKind.value === 'at') {
    for (const sk of skills.value.filter((s) => s.enabled)) {
      items.push({ id: 'sk-' + sk.id, kind: '技能', label: sk.name, insert: '@' + sk.name })
    }
    if (mentionSearchedFiles.value.length > 0) {
      for (const p of mentionSearchedFiles.value.slice(0, 20)) {
        items.push({ id: 'fl-' + p, kind: '文件', label: p, insert: '@' + p })
      }
    } else {
      for (const f of flattenFiles(fileTree.value).slice(0, 20)) {
        items.push({ id: 'fl-' + f.path, kind: '文件', label: f.path, insert: '@' + f.path })
      }
    }
    for (const sess of sessions.value.slice(0, 5)) {
      items.push({ id: 'se-' + sess.id, kind: '会话', label: sess.title, insert: '@' + (sess.title || sess.id) })
    }
  }
  if (!q) return items
  return items.filter((i) => `${i.kind} ${i.label} ${i.insert}`.toLowerCase().includes(q))
})

watch(inputPrompt, (v) => {
  const sl = v.match(/(^|\s)\/([^\s]*)$/)
  const at = v.match(/(^|\s)@([^\s]*)$/)
  if (sl) {
    mentionKind.value = 'slash'
    mentionQuery.value = sl[2] || ''
    mentionOpen.value = true
    mentionIndex.value = 0
  } else if (at) {
    mentionKind.value = 'at'
    const query = at[2] || ''
    mentionQuery.value = query
    mentionOpen.value = true
    mentionIndex.value = 0

    if (searchDebounceTimer) clearTimeout(searchDebounceTimer)
    searchDebounceTimer = setTimeout(async () => {
      try {
        const matches = await wailsBridge.searchWorkspace('find', query || '.', 20)
        mentionSearchedFiles.value = matches.map(m => m.path)
      } catch {
        mentionSearchedFiles.value = []
      }
    }, 120)
  } else {
    mentionOpen.value = false
    mentionKind.value = ''
  }
})

function applyMention(item: MentionItem) {
  inputPrompt.value = inputPrompt.value.replace(/(^|\s)([@/][^\s]*)$/, (_, sp) => sp + item.insert + ' ')
  mentionOpen.value = false
}

function handleComposerKeydown(e: KeyboardEvent) {
  if (mentionOpen.value && mentionItems.value.length > 0) {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      mentionIndex.value = (mentionIndex.value + 1) % mentionItems.value.length
      return
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault()
      mentionIndex.value = (mentionIndex.value - 1 + mentionItems.value.length) % mentionItems.value.length
      return
    }
    if (e.key === 'Enter' || e.key === 'Tab') {
      e.preventDefault()
      const item = mentionItems.value[mentionIndex.value]
      if (item) applyMention(item)
      return
    }
    if (e.key === 'Escape') {
      e.preventDefault()
      mentionOpen.value = false
      return
    }
  }
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    void handleSend()
  }
}

async function copyMessage(content: string) {
  try {
    await navigator.clipboard.writeText(content || '')
    showToast('✓ 已复制到剪贴板')
  } catch (err) {
    showToast('复制失败: ' + err)
  }
}

async function regenerateLast() {
  const msgs = currentSession.value.messages || []
  const lastUser = [...msgs].reverse().find((m) => m.role === 'user')
  if (!lastUser?.content) {
    showToast('没有可重新生成的用户消息')
    return
  }
  inputPrompt.value = lastUser.content
  await handleSend()
}

function onChatDrop(e: DragEvent) {
  const files = e.dataTransfer?.files
  if (!files?.length) return
  for (let i = 0; i < files.length; i++) {
    const f = files[i] as File & { path?: string }
    const p = f.path || f.name
    if (p && !attachedFiles.value.includes(p)) attachedFiles.value.push(p)
  }
}

const usageMetrics = ref({ total_tokens: 0, total_calls: 0, estimated_cost: '$0', active_sessions: 0, last_updated_time: '' })

async function loadUsageMetrics() {
  try {
    usageMetrics.value = await wailsBridge.getUsageMetrics()
  } catch {
    usageMetrics.value = { total_tokens: 0, total_calls: 0, estimated_cost: '$0', active_sessions: 0, last_updated_time: '' }
  }
}

async function triggerUpload() {
  try {
    const selected = await wailsBridge.openFileDialog()
    if (selected && selected.length > 0) {
      for (const item of selected) {
        if (!attachedFiles.value.includes(item)) attachedFiles.value.push(item)
      }
    }
  } catch (err) {
    console.error('File dialog error:', err)
  }
}



async function submitAgentChoice(optionID: string, customNote: string = '') {
  if (!pendingChoice.value) return
  const choiceCopy = pendingChoice.value
  pendingChoice.value = null
  const app = (window as any).go?.main?.App
  if (app?.ResumeAgentChoice) {
    await app.ResumeAgentChoice(choiceCopy.session_id, choiceCopy.request_id, optionID, customNote)
  } else {
    showToast('microkernel not connected')
  }
}

async function submitAgentConfirm(allow: boolean) {
  if (!pendingConfirm.value) return
  const confirmCopy = pendingConfirm.value
  pendingConfirm.value = null
  const app = (window as any).go?.main?.App
  if (app?.ResumeAgentConfirm) {
    await app.ResumeAgentConfirm(confirmCopy.session_id, confirmCopy.request_id, allow)
  } else {
    showToast('microkernel not connected')
  }
}

async function handleSend() {
  const prompt = inputPrompt.value.trim()
  if (!prompt || isStreaming.value) return
  
  if (pendingChoice.value || pendingConfirm.value) {
    pendingChoice.value = null
    pendingConfirm.value = null
    await wailsBridge.cancelStreaming()
    showToast('已取消当前等待项并中断旧任务。')
  }

  const slash = prompt.split(/\s+/)[0]
  if (slash === '/test' || slash === '/tdd') {
    inputPrompt.value = ''
    showToast('正在运行工作区真实测试…')
    
    // Ensure session ID is initialized
    if (!currentSessionId.value) {
      const newId = 'sess_' + Date.now()
      currentSessionId.value = newId
      currentSession.value.id = newId
      currentSession.value.workspace = workspacePath.value
      currentSession.value.model = selectedModel.value
      currentSession.value.title = 'TDD 自动化验证'
    }
    
    try {
      const report = await wailsBridge.runTDDValidation()
      const snippet = (report.output || '').slice(0, 120)
      if (report.status === 'PASS') {
        showToast(`✓ TDD 全部通过 (${report.passed} passed)`)
      } else {
        const failSummary = report.failed_tests && report.failed_tests.length > 0
          ? `失败用例: ${report.failed_tests.join(', ')}`
          : (report.output || '').slice(0, 100)
        showToast(`❌ TDD 验证未通过 (${report.failed} failed) · ${failSummary}`)
      }
      
      const tddMsgId = 'msg_tdd_' + Date.now()
      currentSession.value.messages.push({ id: 'sys_'+Date.now(), role: 'system', content: `**[系统工具 TDD 自动化验证]**\n\n状态：${report.status === 'PASS' ? '✅ 通过 (PASS)' : '❌ 失败 (FAIL)'}\n耗时：${report.duration}\n\n\`\`\`text\n${report.output || '无输出'}\n\`\`\``, time: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) })
        await wailsBridge.appendSystemMessage(currentSession.value.id, `**[系统工具 TDD 自动化验证]**\n\n状态：${report.status === 'PASS' ? '✅ 通过 (PASS)' : '❌ 失败 (FAIL)'}\n耗时：${report.duration}\n\n\`\`\`text\n${report.output || '无输出'}\n\`\`\``)
      scrollChatToLatest()
    } catch (err) {
      showToast('TDD 无法执行: ' + err)
      currentSession.value.messages.push({ id: 'sys_'+Date.now(), role: 'system', content: `**[系统工具 TDD 自动化验证]**\n\n❌ 执行异常\n\n\`\`\`text\n${err}\n\`\`\``, time: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) })
        await wailsBridge.appendSystemMessage(currentSession.value.id, `**[系统工具 TDD 自动化验证]**\n\n❌ 执行异常\n\n\`\`\`text\n${err}\n\`\`\``)
      scrollChatToLatest()
    }
    return
  }
  if (slash === '/diff') {
    inputPrompt.value = ''
    activeActivity.value = 'git'
    await loadGitStatus()
    isDiffOpen.value = true
    showToast('已打开真实 Git 状态（无改动则为空）')
    return
  }
  if (slash === '/term' || slash === '/terminal') {
    inputPrompt.value = ''
    toggleTerminalDrawer(true)
    return
  }
  const mcpHit = mcps.value.find((m) => slash === '/' + m.name || slash === '/' + m.id)
  if (mcpHit) {
    inputPrompt.value = ''
    await testMcpAction(mcpHit.id)
    return
  }

  if (pendingDiffFiles.value.length > 0 && !forceSendWithPendingDiff.value) {
    isPendingDiffPromptOpen.value = true
    return
  }
  forceSendWithPendingDiff.value = false

  if (!selectedModel.value) {
    showToast('请先在设置中添加模型渠道并选择模型，不会使用内置假模型')
    return
  }

  let fullPrompt = prompt
  if (attachedFiles.value.length > 0) {
    fullPrompt = `[附加关联文件]\n${attachedFiles.value.map(f => `@${f}`).join('\n')}\n\n${fullPrompt}`
    attachedFiles.value = []
  }

  // 保证会话 ID 绝对非空，避免向后端传入空 session_id 生成畸形文件
  if (!currentSessionId.value) {
    const newId = 'sess_' + Date.now()
    currentSessionId.value = newId
    currentSession.value.id = newId
    currentSession.value.workspace = workspacePath.value
    currentSession.value.model = selectedModel.value
    const line = fullPrompt.split('\n')[0].trim()
    currentSession.value.title = line.length > 32 ? line.slice(0, 32) + '…' : (line || '新对话')
  }

  inputPrompt.value = ''
  isStreaming.value = true

  const userMsgId = 'msg_' + Date.now() + '_' + Math.random().toString(36).slice(2, 7)
  currentSession.value.messages.push({
    id: userMsgId,
    role: 'user',
    content: fullPrompt,
    time: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  })

  const asstMsgId = 'asst_' + Date.now() + '_' + Math.random().toString(36).slice(2, 7)
  currentSession.value.messages.push({
    id: asstMsgId,
    role: 'assistant',
    content: '',
    thinking: '',
    time: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  })

  followLatestChat()

  try {
    await wailsBridge.sendMessage(
      {
        session_id: currentSessionId.value,
        prompt: fullPrompt,
        model: selectedModel.value,
        is_full_auto: false,
        strategy: 'implement',
        strategy_note: ''
      },
      {
        onThinking(thinking) {
          const target = currentSession.value.messages.find(m => m.id === asstMsgId)
          if (target) target.thinking = (target.thinking || '') + thinking
          pushAgentTrace('thinking', thinking.slice(0, 80))
          scrollChatToLatest()
        },
        onChunk(delta) {
          const target = currentSession.value.messages.find(m => m.id === asstMsgId)
          if (target) target.content += delta
          scrollChatToLatest()
        },
        onToolStart(tool, args, tcId) {
          pushAgentTrace('tool', `start ${tool}`)
          const target = currentSession.value.messages.find(m => m.id === asstMsgId)
          let parsedArgs = args
          if (typeof args === 'string') {
            try {
              parsedArgs = JSON.parse(args)
            } catch (_) {
              parsedArgs = args
            }
          }
          if (target) {
            const toolRecord = { id: tcId || 'tool_' + Date.now(), name: tool, args: parsedArgs, output: '正在执行...' }
            target.tool = toolRecord
            if (!target.tools) target.tools = []
            target.tools.push(toolRecord)
          }
          scrollChatToLatest()
        },
        onToolEnd(tool, output, tcId) {
          pushAgentTrace('tool', `end ${tool}`)
          const target = currentSession.value.messages.find(m => m.id === asstMsgId)
          if (target) {
            if (target.tool && target.tool.name === tool) {
              target.tool.output = output
            }
            if (target.tools && target.tools.length > 0) {
              const matched = tcId ? target.tools.find(t => t.id === tcId) : target.tools[target.tools.length - 1]
              if (matched) matched.output = output
            }
          }
          scrollChatToLatest()
        },
        onChoice(data) {
          pendingChoice.value = data
          pendingChoiceCustomNote.value = ''
          pendingChoiceSelected.value = ''
          if (data && data.options) {
            const rec = data.options.find((o: any) => o.recommended)
            if (rec) pendingChoiceSelected.value = rec.id
            else if (data.options.length > 0) pendingChoiceSelected.value = data.options[0].id
          }
          scrollChatToLatest()
        },
        onConfirm(data) {
          pendingConfirm.value = data
          scrollChatToLatest()
        },
        onFilesChanged(file) {
          pushAgentTrace('file', `changed: ${file}`)
          openEditorTab(file, 'diff')
          void loadGitStatus()
          showToast(`已写入工作区，请审查 Diff：${file}`)
        },
        onDiagnostic(file, errors) {
          if (!file || file === activeDiffFile.value) {
            editorDiagnostics.value = errors
          }
        },
        onDone() {
          pushAgentTrace('done', 'stream complete')
          isStreaming.value = false
          currentSession.value.workspace = workspacePath.value
          if (pendingDiffFiles.value.length > 0 && currentSession.value.task) {
            currentSession.value.task.status = 'pending_diff'
            currentSession.value.task.pending_diff_files = [...pendingDiffFiles.value]
          }
          // Removed whole-session save. UI relies on incremental state now.
          void loadSessionsList()
          ensureSessionTab(currentSessionId.value, currentSession.value.title)
          if (activeDiffFile.value) void refreshDiagnostics(activeDiffFile.value)
          scrollChatToLatest()
        }
      }
    )
  } catch (err) {
    isStreaming.value = false
    showToast('请求异常: ' + err)
  }
}

async function stopGenerationAction() {
  await wailsBridge.cancelAgentStream()
  isStreaming.value = false
  if (currentSession.value && currentSession.value.id) {
    try {
      // await wailsBridge.saveSession(...) removed
    } catch (_) {}
  }
  showToast('已中断本次推理并保存当前内容')
}

// 6. 设置中枢 (渠道、MCP、Skill、Rule)
const channels = ref<ChannelConfig[]>([])
const mcps = ref<MCPServerConfig[]>([])
const skills = ref<SkillConfig[]>([])
const rules = ref<RuleConfig[]>([])
const pingLoadingMap = reactive<Record<string, boolean>>({})

const primaryChannel = computed(() => channels.value.find(c => c.primary))

const modelHealthStatus = computed<{
  state: 'streaming' | 'online' | 'standby' | 'offline' | 'unconfigured'
  text: string
  dotClass: string
  badgeClass: string
}>(() => {
  if (isStreaming.value) {
    return {
      state: 'streaming',
      text: '推理中',
      dotClass: 'bg-[#D96B27] animate-pulse',
      badgeClass: 'bg-[#D96B27]/10 text-[#D96B27]'
    }
  }
  const p = primaryChannel.value
  if (!p || !p.endpoint) {
    return {
      state: 'unconfigured',
      text: '未配置渠道',
      dotClass: 'bg-zinc-400',
      badgeClass: 'bg-zinc-100 text-zinc-600'
    }
  }
  if (p.status === 'offline') {
    return {
      state: 'offline',
      text: '离线/异常',
      dotClass: 'bg-red-500',
      badgeClass: 'bg-red-50 text-red-600'
    }
  }
  if (p.status === 'online') {
    const latStr = p.latency && p.latency !== '未测速' ? `在线 · ${p.latency}` : '在线'
    return {
      state: 'online',
      text: latStr,
      dotClass: 'bg-[#10A37F]',
      badgeClass: 'bg-[#10A37F]/10 text-[#10A37F]'
    }
  }
  return {
    state: 'standby',
    text: '未探活',
    dotClass: 'bg-amber-500',
    badgeClass: 'bg-amber-50 text-amber-700'
  }
})

const channelForm = reactive({
  id: '',
  name: '',
  protocol: 'openai',
  auth_type: 'api_key',
  endpoint: '',
  api_key: '',
  extra_models: '',
  api_version: '2024-02-15-preview',
  token_endpoint: '',
  client_id: '',
  client_secret: ''
})

async function loadSettingsData() {
  try {
    channels.value = await wailsBridge.listChannels()
    mcps.value = await wailsBridge.listMCPs()
    skills.value = await wailsBridge.listSkills()
    rules.value = await wailsBridge.listRules()
    const prefs = await wailsBridge.getUIPrefs()
    Object.assign(uiPrefs, prefs)
    applyTheme(uiPrefs.theme)
    Object.assign(sandboxStatus, await wailsBridge.getSandboxStatus())
    Object.assign(runtimeInfo, await wailsBridge.getRuntimeInfo())
    skillTemplates.value = await wailsBridge.listSkillTemplates()
    const primary = channels.value.find(c => c.primary)
    if (primary && primary.model) {
      selectedModel.value = primary.model
    }
  } catch (err) {
    console.error(err)
  }
}

function openSettingsTab(tab: string) {
  activeSettingsTab.value = tab
  isSettingsOpen.value = true
  if (tab === 'about') void loadUsageMetrics()
}

async function executePing(id: string) {
  pingLoadingMap[id] = true
  try {
    const latency = await wailsBridge.pingChannel(id)
    const target = channels.value.find(c => c.id === id)
    if (target) {
      target.latency = latency
      target.status = 'online'
    }
    showToast(`✓ 渠道真实网络往返延迟: ${latency}`)
  } catch (err) {
    const target = channels.value.find(c => c.id === id)
    if (target) target.status = 'offline'
    showToast('测速失败: ' + err)
  } finally {
    pingLoadingMap[id] = false
  }
}

async function pingAllChannels() {
  for (const ch of channels.value) {
    await executePing(ch.id)
  }
}

function setPrimaryChannel(id: string) {
  channels.value.forEach(c => c.primary = (c.id === id))
  const cur = channels.value.find(c => c.id === id)
  if (cur) wailsBridge.saveChannel(cur)
}

function openAddChannelModal() {
  channelForm.id = ''
  channelForm.name = ''
  channelForm.protocol = 'openai'
  channelForm.auth_type = 'api_key'
  channelForm.endpoint = ''
  channelForm.api_key = ''
  channelForm.extra_models = ''
  channelForm.api_version = '2024-02-15-preview'
  channelForm.token_endpoint = ''
  channelForm.client_id = ''
  channelForm.client_secret = ''
  isChannelModalOpen.value = true
}

function editChannel(ch: ChannelConfig) {
  channelForm.id = ch.id
  channelForm.name = ch.name
  channelForm.protocol = ch.protocol || ch.auth_type || 'openai'
  channelForm.auth_type = ch.auth_type || (channelForm.protocol === 'ollama' ? 'none' : 'api_key')
  channelForm.endpoint = ch.endpoint
  channelForm.api_key = ch.api_key || ''
  channelForm.extra_models = (ch.extra_models || []).join(', ')
  channelForm.api_version = ch.extra_config?.api_version || '2024-02-15-preview'
  channelForm.token_endpoint = ch.extra_config?.token_endpoint || ''
  channelForm.client_id = ch.extra_config?.client_id || ''
  channelForm.client_secret = ch.extra_config?.client_secret || ''
  isChannelModalOpen.value = true
}

async function deleteChannel(id: string) {
  await wailsBridge.deleteChannel(id)
  channels.value = channels.value.filter(c => c.id !== id)
  showToast('✓ 渠道已移除')
}

async function fetchModelsAction() {
  try {
    let ep = channelForm.endpoint.trim()
    if (ep && !ep.startsWith('http://') && !ep.startsWith('https://')) {
      ep = (ep.includes('localhost') || ep.includes('127.0.0.1')) ? 'http://' + ep : 'https://' + ep
      channelForm.endpoint = ep
    }
    const models = await wailsBridge.fetchUpstreamModels(channelForm.endpoint, channelForm.api_key)
    if (models && models.length > 0) {
      upstreamFetchedModels.value = models
      if (!channelForm.extra_models) {
        channelForm.extra_models = models.join(', ')
      }
      showToast(`✓ 成功从上游网关探测到 ${models.length} 个真实在线模型！`)
    } else {
      showToast('未探测到可用模型列表')
    }
  } catch (err) {
    showToast('拉取模型异常: ' + err)
  }
}

async function saveChannelAction() {
  const extraConfig: Record<string, string> = {}
  if (channelForm.api_version) extraConfig.api_version = channelForm.api_version
  if (channelForm.token_endpoint) extraConfig.token_endpoint = channelForm.token_endpoint
  if (channelForm.client_id) extraConfig.client_id = channelForm.client_id
  if (channelForm.client_secret) extraConfig.client_secret = channelForm.client_secret

  await wailsBridge.saveChannel({
    id: channelForm.id || 'ch_' + Date.now(),
    name: channelForm.name,
    primary: false,
    status: 'standby',
    protocol: channelForm.protocol || 'openai',
    auth_type: channelForm.auth_type || 'api_key',
    endpoint: channelForm.endpoint,
    api_key: channelForm.api_key,
    extra_config: extraConfig,
    model: selectedModel.value,
    extra_models: channelForm.extra_models.split(/[,，\s]+/).map((x) => x.trim()).filter(Boolean),
    latency: '未测速',
    updated_at: Date.now()
  })
  isChannelModalOpen.value = false
  await loadSettingsData()
  showToast('✓ 渠道配置已保存至 ~/.tiancode/channels.json')
}

async function toggleMcp(mcp: MCPServerConfig) {
  await wailsBridge.saveMCP(mcp)
}

async function deleteMcpAction(id: string) {
  await wailsBridge.deleteMCP(id)
  await loadSettingsData()
  showToast('✓ MCP 已删除')
}

async function testMcpAction(id: string) {
  try {
    const r = await wailsBridge.testMCPServer(id)
    const mcp = mcps.value.find(m => m.id === id)
    if (r.status === 'ERROR' || r.error) {
      if (mcp) mcp.last_error = r.error
      showToast(`❌ MCP 探活失败 [${r.name}]`)
    } else {
      if (mcp) mcp.last_error = ''
      showToast((r.status || 'OK') + ' · 工具 ' + (r.tool_count || 0) + ' · ' + (r.latency || ''))
    }
  } catch (err) {
    showToast('MCP 探活异常')
    const mcp = mcps.value.find(m => m.id === id)
    if (mcp) mcp.last_error = String(err)
  }
}

async function toggleSkill(skill: SkillConfig) {
  await wailsBridge.saveSkill(skill)
}

async function toggleRule(rule: RuleConfig) {
  await wailsBridge.saveRule(rule)
}

const mcpForm = reactive({
  name: '',
  type: 'stdio',
  command: '',
  args: [] as string[]
})
const mcpArgsInput = ref('')
const mcpEnvInput = ref('')

const skillForm = reactive({
  name: '',
  description: '',
  content: ''
})

const ruleForm = reactive({
  title: '',
  content: ''
})

async function saveMcpAction() {
  if (!mcpForm.name.trim() || !mcpForm.command.trim()) {
    showToast('请完整填写 MCP 服务名称与启动命令')
    return
  }
  const args = mcpArgsInput.value.trim() ? mcpArgsInput.value.trim().split(/\s+/) : []
  
  const envMap: Record<string, string> = {}
  if (mcpEnvInput.value.trim()) {
    mcpEnvInput.value.split('\n').forEach(line => {
      const parts = line.split('=')
      if (parts.length >= 2) {
        const k = parts[0].trim()
        const v = parts.slice(1).join('=').trim()
        if (k) envMap[k] = v
      }
    })
  }

  await wailsBridge.saveMCP({
    id: 'mcp_' + Date.now(),
    name: mcpForm.name.trim(),
    type: mcpForm.type,
    command: mcpForm.command.trim(),
    args: args,
    env: envMap,
    enabled: true,
    updated_at: Date.now()
  })
  isMcpModalOpen.value = false
  mcpForm.name = ''
  mcpForm.command = ''
  mcpArgsInput.value = ''
  mcpEnvInput.value = ''
  await loadSettingsData()
  showToast('✓ MCP 服务已成功注册并保存')
}

async function saveSkillAction() {
  if (!skillForm.name.trim()) {
    showToast('请填写技能名称')
    return
  }
  await wailsBridge.saveSkill({
    id: 'skill_' + Date.now(),
    name: skillForm.name.trim(),
    description: skillForm.description.trim(),
    prompt: skillForm.content.trim(),
    enabled: true,
    updated_at: Date.now()
  })
  isSkillModalOpen.value = false
  skillForm.name = ''
  skillForm.description = ''
  skillForm.content = ''
  await loadSettingsData()
  showToast('✓ 技能已成功添加至本地技能库')
}

async function deleteSkillAction(id: string) {
  await wailsBridge.deleteSkill(id)
  await loadSettingsData()
  showToast('✓ 技能已从本地技能库移除')
}

async function importSkillFileAction() {
  try {
    const skill = await wailsBridge.importSkillFromDialog()
    if (skill) {
      await loadSettingsData()
      showToast(`✓ 已成功导入本地技能：${skill.name}`)
    }
  } catch (err) {
    showToast(`导入技能异常: ${err}`)
  }
}

async function saveRuleAction() {
  if (!ruleForm.title.trim() || !ruleForm.content.trim()) {
    showToast('请完整填写规则名称与规则内容')
    return
  }
  await wailsBridge.saveRule({
    id: 'rule_' + Date.now(),
    title: ruleForm.title.trim(),
    content: ruleForm.content.trim(),
    scope: 'workspace',
    enabled: true,
    updated_at: Date.now()
  })
  isRuleModalOpen.value = false
  ruleForm.title = ''
  ruleForm.content = ''
  await loadSettingsData()
  showToast('✓ 工程规约已成功添加')
}

async function deleteRuleAction(id: string) {
  await wailsBridge.deleteRule(id)
  await loadSettingsData()
  showToast('✓ 工程规约已删除')
}

// 7. 真实 AST 代码拓扑知识图谱
const astNodes = ref<GraphNode[]>([])
const selectedAstNode = ref<GraphNode | null>(null)

// =========================================================================
// 5. 架构透视与依赖治理工作板 (Architecture & Dependency Workbench)
// =========================================================================
const architectureReport = ref<ArchitectureReport | null>(null)
const isArchitectureLoading = ref(false)
const selectedArchitectureNode = ref<PackageNode | null>(null)
const activeArchitectureView = ref<'dag' | 'matrix' | 'blast'>('dag')
const architectureFilterViolationsOnly = ref(false)
const architectureSearchQuery = ref('')
const blastRadiusReport = ref<BlastRadiusReport | null>(null)
const isBlastRadiusLoading = ref(false)

// 多项目与子模块状态
const architectureCurrentPath = ref('')
const availableGoModules = ref<GoModuleInfo[]>([])
const recentAnalysisProjects = ref<string[]>(JSON.parse(localStorage.getItem('tiancode:recent_analysis_projects') || '[]'))

// 计算当前是否处于非主工作区的外部/子模块参考模式
const isExternalArchitectureProject = computed(() => {
  if (!architectureCurrentPath.value) return false
  const normCurrent = architectureCurrentPath.value.replace(/\\/g, '/').toLowerCase()
  const normWorkspace = workspacePath.value.replace(/\\/g, '/').toLowerCase()
  return normCurrent !== normWorkspace
})

async function loadWorkspaceGoModules() {
  try {
    const mods = await wailsBridge.discoverWorkspaceGoModules()
    availableGoModules.value = mods
  } catch (err) {
    console.warn('探测工作区模块失败:', err)
  }
}

function openArchitectureModal() {
  isKnowledgeGraphOpen.value = true
  if (!architectureCurrentPath.value) {
    architectureCurrentPath.value = workspacePath.value
  }
  loadWorkspaceGoModules()
  if (!architectureReport.value && !isArchitectureLoading.value) {
    scanArchitecture(architectureCurrentPath.value)
  }
}

const openKnowledgeGraphModal = openArchitectureModal

async function scanArchitecture(customPath?: string) {
  const targetPath = customPath || architectureCurrentPath.value || workspacePath.value
  architectureCurrentPath.value = targetPath
  isArchitectureLoading.value = true
  try {
    const report = await wailsBridge.getArchitectureReport(targetPath)
    architectureReport.value = report
    if (report.packages && report.packages.length > 0) {
      if (!selectedArchitectureNode.value || !report.packages.some(p => p.id === selectedArchitectureNode.value?.id)) {
        selectedArchitectureNode.value = report.packages[0]
      }
    }
  } catch (err) {
    showToast('架构拓扑扫描失败: ' + err)
  } finally {
    isArchitectureLoading.value = false
  }
}

const scanASTGraph = scanArchitecture

async function switchArchitectureProject(targetPath: string) {
  if (!targetPath) return
  architectureCurrentPath.value = targetPath
  selectedArchitectureNode.value = null
  blastRadiusReport.value = null
  await scanArchitecture(targetPath)
}

async function pickExternalArchitectureProject() {
  try {
    const dir = await wailsBridge.openDirectoryDialog()
    if (!dir) return
    const list = recentAnalysisProjects.value.filter(p => p !== dir)
    list.unshift(dir)
    if (list.length > 5) list.length = 5
    recentAnalysisProjects.value = list
    localStorage.setItem('tiancode:recent_analysis_projects', JSON.stringify(list))

    await switchArchitectureProject(dir)
    showToast(`✓ 已切换至外部参考项目: ${dir}`)
  } catch (err) {
    showToast('选择外部项目失败: ' + err)
  }
}

function resetArchitectureToWorkspace() {
  architectureCurrentPath.value = workspacePath.value
  selectedArchitectureNode.value = null
  blastRadiusReport.value = null
  scanArchitecture(workspacePath.value)
  showToast('✓ 已切回当前活动工作区')
}

function closeArchitectureModal() {
  isKnowledgeGraphOpen.value = false
  architectureCurrentPath.value = workspacePath.value
  blastRadiusReport.value = null
}

async function runBlastRadiusAnalysis(symbol: string) {
  if (!symbol) return
  isBlastRadiusLoading.value = true
  try {
    const res = await wailsBridge.getBlastRadiusReport(architectureCurrentPath.value || workspacePath.value, symbol)
    blastRadiusReport.value = res
  } catch (err) {
    showToast('影响面分析失败: ' + err)
  } finally {
    isBlastRadiusLoading.value = false
  }
}

function injectArchitectureContext(node?: PackageNode | null) {
  // 防投毒物理阻断
  if (isExternalArchitectureProject.value) {
    showToast('⚠️ 当前为外部参考项目，仅当前活动工作区支持注入 Agent 会话，防止上下文投毒！')
    return
  }

  const target = node || selectedArchitectureNode.value
  let text = ''
  if (target) {
    text = `\n> 架构模块拓扑约束: \`${target.name}\` [${target.layer_name}]\n> 物理路径: \`${target.path}\`\n> 核心导出: ${target.symbols.map(s => s.name).join(', ') || '无'}\n> 内部依赖: ${target.imports.join(', ') || '无'}\n`
  } else if (architectureReport.value) {
    text = `\n> 工程全局架构概览: 共 ${architectureReport.value.total_packages} 个模块，${architectureReport.value.total_symbols} 个导出符号，架构违规数: ${architectureReport.value.violation_count}\n`
  }
  if (text) {
    inputPrompt.value = inputPrompt.value ? inputPrompt.value + text : text
    showToast(`✓ 已将架构约束注入 Agent 提示词`)
  }
}

// 兼容旧接口
const astGraph = computed(() => {
  return { pos: [], edges: [], maxX: 400, maxY: 240 }
})

watch(selectedAstNode, async (node) => {
  if (!node) {
    adrNote.value = ''
    return
  }
  try {
    adrNote.value = (await wailsBridge.getADR(node.id)) || ''
  } catch {
    adrNote.value = ''
  }
})

async function saveAdrNote() {
  if (!selectedAstNode.value) return
  try {
    await wailsBridge.saveADR(selectedAstNode.value.id, adrNote.value)
    showToast('✓ ADR 已写入 ~/.tiancode/adr.json')
  } catch (err) {
    showToast('保存 ADR 失败: ' + err)
  }
}

function injectNodeToPrompt() {
  injectArchitectureContext()
}

// =========================================================================
// 6. 底部集成式可折叠流式终端抽屉 (Terminal Drawer)
// =========================================================================
interface TerminalOutputItem {
  type: 'cmd' | 'output' | 'exit'
  text?: string
  exitCode?: number
  durationMs?: number
}

const isTerminalOpen = ref(false)
const isTerminalMaximized = ref(false)
const terminalHeight = ref(240)
const activeTerminalTab = ref<'shell' | 'logs'>('shell')
const isTerminalRunning = ref(false)
const terminalInputCmd = ref('')
const currentTerminalBuffer = ref('')
const terminalOutputs = ref<TerminalOutputItem[]>([])
const commandHistory = ref<string[]>([])
const historyIndex = ref(-1)
const terminalScrollRef = ref<HTMLDivElement | null>(null)

const agentTraceLogs = ref<{ time: string; phase: string; message: string }[]>([])

function pushAgentTrace(phase: string, message: string) {
  const time = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
  agentTraceLogs.value.push({ time, phase, message })
  if (agentTraceLogs.value.length > 200) {
    agentTraceLogs.value.splice(0, agentTraceLogs.value.length - 200)
  }
}

type PaletteItem = { id: string; kind: string; label: string; hint: string; run: () => void }

function flattenFiles(nodes: FileNode[], acc: FileNode[] = []): FileNode[] {
  for (const n of nodes) {
    if (!n.is_dir) acc.push(n)
    if (n.children?.length) flattenFiles(n.children, acc)
  }
  return acc
}

const commandPaletteItems = computed(() => {
  const q = commandPaletteQuery.value.trim().toLowerCase()
  const items: PaletteItem[] = [
    { id: 'settings', kind: '设置', label: '打开设置', hint: '渠道 / MCP / 技能', run: () => { isSettingsOpen.value = true } },
    { id: 'hotplug', kind: '算子', label: '热插拔插件中心与 DSH 算子大盘', hint: '微内核算子 / MCP / SafetyRail / 协议驱动', run: () => { void openHotplugDashboard() } },
    { id: 'terminal', kind: '终端', label: '打开终端', hint: 'Ctrl+`', run: () => toggleTerminalDrawer(true) },
    { id: 'git', kind: 'Git', label: '源代码管理', hint: gitBranchLabel.value, run: () => switchToGitActivity() },
    { id: 'project', kind: '项目', label: '打开项目文件夹', hint: workspaceName.value, run: () => { void openProjectFolder() } }
  ]
  for (const sess of sessions.value.slice(0, 30)) {
    items.push({
      id: 's-' + sess.id,
      kind: '会话',
      label: sess.title || sess.id,
      hint: sess.workspace || '',
      run: () => { void selectSession(sess.id) }
    })
  }
  for (const f of flattenFiles(fileTree.value).slice(0, 50)) {
    items.push({
      id: 'f-' + f.path,
      kind: '文件',
      label: f.name,
      hint: f.path,
      run: () => handleFileClick(f)
    })
  }
  if (!q) return items
  return items.filter((i) => `${i.kind} ${i.label} ${i.hint}`.toLowerCase().includes(q))
})

function openCommandPalette() {
  commandPaletteQuery.value = ''
  commandPaletteIndex.value = 0
  isCommandPaletteOpen.value = true
}

function moveCommandPalette(delta: number) {
  const n = commandPaletteItems.value.length
  if (n === 0) return
  commandPaletteIndex.value = (commandPaletteIndex.value + delta + n) % n
}

function runCommandPaletteItem(item: PaletteItem) {
  isCommandPaletteOpen.value = false
  item.run()
}

function confirmCommandPalette() {
  const item = commandPaletteItems.value[commandPaletteIndex.value]
  if (item) runCommandPaletteItem(item)
}

async function loadHotplugReport() {
  isHotplugLoading.value = true
  try {
    const report = await wailsBridge.getHotplugDashboard()
    hotplugReport.value = report
  } catch (err) {
    showToast('获取算子大盘失败: ' + err)
  } finally {
    isHotplugLoading.value = false
  }
}

async function openHotplugDashboard(initialTab?: 'overview' | 'tools' | 'mcps' | 'rails' | 'providers' | 'creator') {
  if (initialTab) {
    hotplugActiveTab.value = initialTab
  }
  isHotplugDashboardOpen.value = true
  await loadHotplugReport()
}

function closeHotplugDashboard() {
  isHotplugDashboardOpen.value = false
}

async function reloadHotplugRegistryAction() {
  isHotplugLoading.value = true
  try {
    const rep = await wailsBridge.reloadHotplugRegistry()
    hotplugReport.value = rep
    showToast('✓ 插件中心与算子大盘已动态热重载并同步')
  } catch (err) {
    showToast('热重载失败: ' + err)
  } finally {
    isHotplugLoading.value = false
  }
}

async function probeHotplugItemAction(itemId: string, itemType: string) {
  try {
    const res = await wailsBridge.probeHotplugItem(itemId, itemType)
    probedItems[itemId] = res
    if (hotplugReport.value) {
      if (itemType === 'tool') {
        const idx = hotplugReport.value.tools.findIndex(t => t.id === itemId)
        if (idx !== -1) hotplugReport.value.tools[idx] = res
      } else if (itemType === 'provider') {
        const idx = hotplugReport.value.providers.findIndex(p => p.id === itemId)
        if (idx !== -1) hotplugReport.value.providers[idx] = res
      } else if (itemType === 'rail') {
        const idx = hotplugReport.value.rails.findIndex(r => r.id === itemId)
        if (idx !== -1) hotplugReport.value.rails[idx] = res
      } else if (itemType === 'mcp') {
        const idx = hotplugReport.value.mcps.findIndex(m => m.id === itemId)
        if (idx !== -1) hotplugReport.value.mcps[idx] = res
      }
    }
    showToast(`✓ [${res.name}] 探活成功 (${res.latency_ms}ms)`)
  } catch (err) {
    showToast('探活失败: ' + err)
  }
}

async function exportHotplugManifestAction() {
  try {
    const jsonStr = await wailsBridge.exportHotplugManifest()
    if (navigator.clipboard) {
      await navigator.clipboard.writeText(jsonStr)
      showToast('✓ 已将 DSH 算子清单 JSON 复制到剪贴板')
    } else {
      showToast('✓ 已生成清单 (长度: ' + jsonStr.length + ' 字符)')
    }
  } catch (err) {
    showToast('导出失败: ' + err)
  }
}

function toggleTerminalDrawer(forceState?: boolean) {
  isTerminalOpen.value = forceState !== undefined ? forceState : !isTerminalOpen.value
  if (isTerminalOpen.value) {
    scrollToBottomTerminal()
  }
}

function clearTerminalLogs() {
  terminalOutputs.value = []
  currentTerminalBuffer.value = ''
}

function scrollToBottomTerminal() {
  nextTick(() => {
    if (terminalScrollRef.value) {
      terminalScrollRef.value.scrollTop = terminalScrollRef.value.scrollHeight
    }
  })
}

function navigateCommandHistory(direction: number) {
  if (commandHistory.value.length === 0) return
  if (historyIndex.value === -1) {
    historyIndex.value = commandHistory.value.length
  }
  historyIndex.value += direction
  if (historyIndex.value < 0) {
    historyIndex.value = 0
  } else if (historyIndex.value >= commandHistory.value.length) {
    historyIndex.value = commandHistory.value.length
    terminalInputCmd.value = ''
    return
  }
  terminalInputCmd.value = commandHistory.value[historyIndex.value] || ''
}

async function submitTerminalCommand() {
  const cmd = terminalInputCmd.value.trim()
  if (!cmd || isTerminalRunning.value) return

  if (cmd === 'clear' || cmd === 'cls') {
    clearTerminalLogs()
    terminalInputCmd.value = ''
    return
  }

  if (!commandHistory.value.includes(cmd)) {
    commandHistory.value.push(cmd)
  }
  historyIndex.value = -1

  terminalOutputs.value.push({ type: 'cmd', text: cmd })
  terminalInputCmd.value = ''
  currentTerminalBuffer.value = ''
  isTerminalRunning.value = true
  scrollToBottomTerminal()

  try {
    await wailsBridge.execTerminalStream(cmd, {
      onData: (chunk: string) => {
        currentTerminalBuffer.value += chunk
        scrollToBottomTerminal()
      },
      onExit: (data) => {
        if (currentTerminalBuffer.value) {
          terminalOutputs.value.push({ type: 'output', text: currentTerminalBuffer.value })
          currentTerminalBuffer.value = ''
        }
        terminalOutputs.value.push({
          type: 'exit',
          exitCode: data.exit_code,
          durationMs: data.duration_ms
        })
        isTerminalRunning.value = false
        scrollToBottomTerminal()
      }
    })
  } catch (err) {
    terminalOutputs.value.push({ type: 'output', text: `[Execution Error]: ${err}` })
    isTerminalRunning.value = false
    scrollToBottomTerminal()
  }
}

async function cancelTerminalAction() {
  try {
    await wailsBridge.cancelTerminalCommand()
    isTerminalRunning.value = false
  } catch (err) {
    console.error('Cancel terminal error:', err)
  }
}

async function runTerminalCommand(cmd: string) {
  const c = cmd.trim()
  if (!c) return
  isTerminalOpen.value = true
  terminalInputCmd.value = c
  await submitTerminalCommand()
}

function handleGlobalKeydown(e: KeyboardEvent) {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
    e.preventDefault()
    if (isCommandPaletteOpen.value) isCommandPaletteOpen.value = false
    else openCommandPalette()
    return
  }

  if (e.key === 'Escape') {
    // 优先级 1: mention/slash 浮层 或 tab 上下文菜单
    if (mentionOpen.value) { mentionOpen.value = false; return }
    if (isCommandPaletteOpen.value) { isCommandPaletteOpen.value = false; return }
    if (tabContextMenu.value) { tabContextMenu.value = null; return }

    // 优先级 2: 二级弹窗 (choice/confirm, 渠道/MCP/技能/pending Diff/策略等)
    if (pendingChoice.value) {
      const choiceCopy = pendingChoice.value
      pendingChoice.value = null
      const app = (window as any).go?.main?.App
      if (app?.ResumeAgentChoice) {
        app.ResumeAgentChoice(choiceCopy.session_id, choiceCopy.request_id, '', '')
      }
      return
    }
    if (pendingConfirm.value) {
      const confirmCopy = pendingConfirm.value
      pendingConfirm.value = null
      const app = (window as any).go?.main?.App
      if (app?.ResumeAgentConfirm) {
        app.ResumeAgentConfirm(confirmCopy.session_id, confirmCopy.request_id, false)
      }
      return
    }
    if (isChannelModalOpen.value) { isChannelModalOpen.value = false; return }
    if (isMcpModalOpen.value) { isMcpModalOpen.value = false; return }
    if (isSkillModalOpen.value) { isSkillModalOpen.value = false; return }
    if (isRuleModalOpen.value) { isRuleModalOpen.value = false; return }
    if (isPendingDiffPromptOpen.value) { isPendingDiffPromptOpen.value = false; return }

    // 优先级 3: 设置/图谱/算子大盘等一级面板
    if (isHotplugDashboardOpen.value) { isHotplugDashboardOpen.value = false; return }
    if (isSettingsOpen.value) { isSettingsOpen.value = false; return }
    if (isKnowledgeGraphOpen.value) { isKnowledgeGraphOpen.value = false; return }
    if (isTerminalOpen.value) { isTerminalOpen.value = false; return }
    
    // 无操作
  }

  if (e.ctrlKey && (e.key === '`' || e.key === '~')) {
    e.preventDefault()
    toggleTerminalDrawer()
  }

  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
    e.preventDefault()
    void saveEditor()
  }
}

function initWorkbench() {
  window.addEventListener('keydown', handleGlobalKeydown)
  void loadSessionsList()
  void loadSettingsData()
  void loadProjects()
  void loadUsageMetrics()
  void loadHotplugReport()
  void wailsBridge.getWorkspace().then(async (ws) => {
    if (!ws) return
    workspacePath.value = ws
    await wailsBridge.addProject(ws)
    isDiffOpen.value = true
    await Promise.all([loadFileTree(), loadGitStatus(), loadSessionsList(), loadProjects()])
    const mine = sessions.value.filter((s) => samePath(s.workspace, ws))
    if (mine.length > 0) {
      await selectSession(mine[0].id)
    }
  }).catch((err) => console.error(err))
  return () => window.removeEventListener('keydown', handleGlobalKeydown)
}


  return {
    activeActivity,
    activeDiffFile,
    activeSettingsTab,
    activeTag,
    activeTerminalTab,
    agentTraceLogs,
    adrNote,
    activateSession,
    commandPaletteIndex,
    commandPaletteItems,
    commandPaletteQuery,
    confirmCommandPalette,
    applyHunkAction,
    applyMention,
    astGraph,
    astNodes,
    attachedFiles,
    availableModels,
    availableTags,
    cancelTerminalAction,
    channelForm,
    channels,
    checkoutBranch,
    chooseWorkspace,
    closeAllTabs,
    closeOtherTabs,
    closeSessionTab,
    clearTerminalLogs,
    commandHistory,
    collapsedProjects,
    commitMessage,
    copyMessage,
    createBranchAction,
    createNewSession,
    createSessionInProject,
    createSnapshotAction,
    currentSession,
    currentSessionId,
    currentTaskStatus,
    currentTerminalBuffer,
    deleteMcpAction,
    deleteChannel,
    deleteRuleAction,
    deleteSession,
    deleteSkillAction,
    diffReport,
    discardHunkAction,
    editorContent,
    editorDirty,
    editorDiagnostics,
    editorView,
    editChannel,
    executePing,
    expandedFolders,
    fetchModelsAction,
    fileTree,
    filteredSessions,
    gitBranchLabel,
    gitBranches,
    gitCurrentBranch,
    gitSnapshots,
    gitPullAction,
    gitPushAction,
    gitStatus,
    handleComposerKeydown,
    handleFileClick,
    handleGitCommit,
    handleGlobalKeydown,
    handleSend,
    hiddenHistoryCount,
    revealFullHistory,
    historyIndex,
    initWorkbench,
    injectNodeToPrompt,
    inputPrompt,
    isChannelModalOpen,
    isCommandPaletteOpen,
    isDiffOpen,
    isFileTreeLoading,
    isGitLoading,
    isGraphLoading,
    isKnowledgeGraphOpen,
    architectureReport,
    isArchitectureLoading,
    selectedArchitectureNode,
    activeArchitectureView,
    architectureFilterViolationsOnly,
    architectureSearchQuery,
    blastRadiusReport,
    isBlastRadiusLoading,
    architectureCurrentPath,
    availableGoModules,
    recentAnalysisProjects,
    isExternalArchitectureProject,
    openArchitectureModal,
    scanArchitecture,
    switchArchitectureProject,
    pickExternalArchitectureProject,
    resetArchitectureToWorkspace,
    closeArchitectureModal,
    runBlastRadiusAnalysis,
    injectArchitectureContext,
    isHotplugDashboardOpen,
    hotplugReport,
    isHotplugLoading,
    hotplugActiveTab,
    hotplugSearchQuery,
    probedItems,
    openHotplugDashboard,
    closeHotplugDashboard,
    loadHotplugReport,
    reloadHotplugRegistryAction,
    probeHotplugItemAction,
    exportHotplugManifestAction,
    isMcpModalOpen,
    isRuleModalOpen,
    isSettingsOpen,
    isSkillModalOpen,
    importSkillFileAction,
    isStreaming,
    isTerminalMaximized,
    isTerminalOpen,
    isTerminalRunning,
    loadDiff,
    loadFileTree,
    loadGitStatus,
    loadEditor,
    loadGitExtras,
    markEditorDirty,
    loadSessionsList,
    loadSettingsData,
    moveCommandPalette,
    mcpArgsInput,
    mcpEnvInput,
    mcpForm,
    mcps,
    mentionIndex,
    mentionItems,
    mentionKind,
    mentionOpen,
    mentionQuery,
    messagesContainerRef,
    stickToBottom,
    onMessagesScroll,
    followLatestChat,
    scrollChatToLatest,
    navigateCommandHistory,
    newBranchName,
    onChatDrop,
    onTabDragStart,
    onTabDrop,
    openTabMenu,
    openCommandPalette,
    openAddChannelModal,
    openFileDiff,
    openKnowledgeGraphModal,
    openProjectFolder,
    openSettingsTab,
    pingAllChannels,
    pingLoadingMap,
    primaryChannel,
    modelHealthStatus,
    projects,
    regenerateLast,
    projectTree,
    renderMarkdown,
    restoreSnapshotAction,
    revertAllWorking,
    revertFileAction,
    revertAllPendingDiffFilesAction,
    revertPath,
    runCommandPaletteItem,
    ruleForm,
    rules,
    saveAdrNote,
    saveEditor,
    saveChannelAction,
    saveMcpAction,
    saveRuleAction,
    saveSkillAction,
    scanASTGraph,
    scrollToBottomTerminal,
    sessionTabs,
    sessionSearch,
    selectSession,
    selectedAstNode,
    selectedModel,
    sessions,
    setWorkspaceView,
    setSessionTag,
    setPrimaryChannel,
    showToast,
    skillForm,
    skills,
    stageAllWorking,
    stageFileAction,
    stageAllPendingDiffFilesAction,
    stagePath,
    stagedTreeFiles,
    stopGenerationAction,
    suggestCommitMessage,
    submitTerminalCommand,
    runTerminalCommand,
    testMcpAction,
    tabContextMenu,
    switchToFileActivity,
    switchToGitActivity,
    terminalHeight,
    terminalInputCmd,
    terminalOutputs,
    terminalScrollRef,
    toastMessage,
    toggleProjectCollapse,
    toggleMcp,
    toggleRule,
    toggleSkill,
    toggleTerminalDrawer,
    triggerUpload,
    unpinProject,
    unstageFileAction,
    uiPrefs,
    persistUIPrefs,
    sandboxStatus,
    runtimeInfo,
    skillTemplates,
    extraModelsInput,
    importWorkspaceRulesAction,
    installSkillTemplateAction,
    checkUpdatesAction,
    exportDiagnosticsAction,
    usageMetrics,
    visibleMessages,
    upstreamFetchedModels,
    workspaceView,
    workingTreeFiles,
    workspaceName,
    workspacePath,
    pendingDiffFiles,
    pendingChoice,
    pendingChoiceSelected,
    pendingChoiceCustomNote,
    submitAgentChoice,
    pendingConfirm,
    submitAgentConfirm,
    activeConstitution,
    isConstitutionModalOpen,
    openEditorTabs,
    openEditorTab,
    switchEditorTab,
    closeEditorTab,
    pendingCloseTab,
    forceCloseEditorTab,
    saveAndCloseEditorTab,
    fileTreeFilter,
    displayFileTree,
    gitStatusMap,
    isPendingDiffPromptOpen,
    forceSendWithPendingDiff,
    explorerTab,
    searchQuery,
    searchAction,
    searchResults,
    searchError,
    targetEditorLine,
    isSearching,
    runWorkspaceSearch,
    searchFromFilter
  }
})




