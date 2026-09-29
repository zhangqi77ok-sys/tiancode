import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { McpDraft, SkillDraft } from '../catalogImport'
import { bridge } from '../wails'

// MCP 服务器与 Skill 的本机清单。存在浏览器存储里，和会话账本分开。
// 清单同时写入本机 extensions.json。启用项会在下一轮对话里告诉模型。
//
// 写路径纪律（0.2.27）：每个写操作都返回可读错误（'' = 成功）、失败回滚乐观改动、
// busy 置位供 UI 禁用——此前 persist 未包 try，SaveExtensions 失败（或浏览器调试桩）
// 会变成未处理 rejection，界面表现是"点保存毫无反应"。

export interface McpServer {
  id: string
  name: string
  transport: 'stdio' | 'http'
  command: string
  args: string
  env: string
  url: string
  headers: string
  enabled: boolean
}

export interface SkillItem {
  id: string
  name: string
  description: string
  body: string
  enabled: boolean
}

export interface ImportResult {
  imported: number
  error: string
}

const MCP_KEY = 'tiancode.mcp'
const SKILL_KEY = 'tiancode.skills'

function load<T>(key: string): T[] {
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return []
    const parsed = JSON.parse(raw) as T[]
    return Array.isArray(parsed) ? parsed : []
  } catch {
    return []
  }
}

function nid(): string {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`
}

function normalizeMcp(row: McpServer): McpServer {
  const transport = row.transport === 'http' || (!row.command && row.url) ? 'http' : 'stdio'
  return {
    id: row.id,
    name: row.name,
    transport,
    command: row.command ?? '',
    args: row.args ?? '',
    env: row.env ?? '',
    url: row.url ?? '',
    headers: row.headers ?? '',
    enabled: row.enabled !== false,
  }
}

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

export const useCatalogStore = defineStore('catalog', () => {
  const mcp = ref<McpServer[]>(load<McpServer>(MCP_KEY).map(normalizeMcp))
  const skills = ref<SkillItem[]>(load<SkillItem>(SKILL_KEY))
  // 写操作进行中（组件据此禁用按钮并显示"保存中…"）
  const busy = ref(false)

  // persist 落盘：本机 extensions.json 是事实源，失败必须抛出可读原因；
  // 浏览器存储只是缓存，不可用不影响功能（不抛）
  async function persist() {
    try {
      await bridge().app.SaveExtensions({ mcp: mcp.value, skills: skills.value })
    } catch (e) {
      throw new Error(`保存扩展清单失败：${errText(e)}`)
    }
    try {
      localStorage.setItem(MCP_KEY, JSON.stringify(mcp.value))
      localStorage.setItem(SKILL_KEY, JSON.stringify(skills.value))
    } catch {
      // 缓存写入失败：本机 JSON 已成功，不打断用户
    }
  }

  async function reload() {
    try {
      const remote = await bridge().app.GetExtensions()
      const localMcp = load<McpServer>(MCP_KEY).map(normalizeMcp)
      const localSkills = load<SkillItem>(SKILL_KEY)
      const remoteEmpty = !remote?.mcp?.length && !remote?.skills?.length
      if (remoteEmpty && (localMcp.length || localSkills.length)) {
        mcp.value = localMcp
        skills.value = localSkills
        await persist()
        return
      }
      mcp.value = (remote?.mcp ?? []).map(normalizeMcp)
      skills.value = remote?.skills ?? []
    } catch {
      mcp.value = load<McpServer>(MCP_KEY).map(normalizeMcp)
      skills.value = load<SkillItem>(SKILL_KEY)
    }
  }

  // 写操作统一壳：busy 置位 + 失败回滚 + 可读错误返回
  async function guarded<T>(mutate: () => T, rollback: (undo: T) => void): Promise<string> {
    const undo = mutate()
    busy.value = true
    try {
      await persist()
      return ''
    } catch (e) {
      rollback(undo)
      return errText(e)
    } finally {
      busy.value = false
    }
  }

  async function upsertMcp(item: Omit<McpServer, 'id'> & { id?: string }): Promise<string> {
    const name = item.name.trim()
    const transport = item.transport === 'http' ? 'http' : 'stdio'
    if (!name) return '名称不能空'
    if (transport === 'stdio' && !item.command.trim()) return 'stdio 需要启动命令'
    if (transport === 'http' && !item.url.trim()) return '远程 MCP 需要 URL'
    const next: McpServer = {
      id: item.id || nid(),
      name,
      transport,
      command: item.command.trim(),
      args: item.args.trim(),
      env: item.env.trim(),
      url: item.url.trim(),
      headers: item.headers.trim(),
      enabled: item.enabled,
    }
    const i = mcp.value.findIndex((x) => x.id === next.id || x.name === next.name)
    return guarded(
      () => {
        const before = [...mcp.value]
        if (i >= 0) mcp.value[i] = { ...next, id: mcp.value[i].id }
        else mcp.value.push(next)
        return before
      },
      (before) => {
        mcp.value = before
      },
    )
  }

  async function importMcp(drafts: McpDraft[]): Promise<ImportResult> {
    let imported = 0
    for (const d of drafts) {
      const msg = await upsertMcp(d)
      if (msg) return { imported, error: msg }
      imported++
    }
    return { imported, error: '' }
  }

  async function removeMcp(id: string): Promise<string> {
    const before = [...mcp.value]
    return guarded(
      () => {
        mcp.value = mcp.value.filter((x) => x.id !== id)
      },
      () => {
        mcp.value = before
      },
    )
  }

  // 乐观翻转：失败回滚（否则界面说"开"、盘里是"关"）
  async function toggleMcp(id: string): Promise<string> {
    const row = mcp.value.find((x) => x.id === id)
    if (!row) return ''
    const prev = row.enabled
    return guarded(
      () => {
        row.enabled = !row.enabled
      },
      () => {
        row.enabled = prev
      },
    )
  }

  async function upsertSkill(item: Omit<SkillItem, 'id'> & { id?: string }): Promise<string> {
    const name = item.name.trim()
    if (!name) return '名称不能空'
    const next: SkillItem = {
      id: item.id || nid(),
      name,
      description: item.description.trim(),
      body: item.body,
      enabled: item.enabled,
    }
    const i = skills.value.findIndex((x) => x.id === next.id || x.name === next.name)
    return guarded(
      () => {
        const before = [...skills.value]
        if (i >= 0) skills.value[i] = { ...next, id: skills.value[i].id }
        else skills.value.push(next)
        return before
      },
      (before) => {
        skills.value = before
      },
    )
  }

  async function importSkills(drafts: SkillDraft[]): Promise<ImportResult> {
    let imported = 0
    for (const d of drafts) {
      const msg = await upsertSkill(d)
      if (msg) return { imported, error: msg }
      imported++
    }
    return { imported, error: '' }
  }

  async function removeSkill(id: string): Promise<string> {
    const before = [...skills.value]
    return guarded(
      () => {
        skills.value = skills.value.filter((x) => x.id !== id)
      },
      () => {
        skills.value = before
      },
    )
  }

  async function toggleSkill(id: string): Promise<string> {
    const row = skills.value.find((x) => x.id === id)
    if (!row) return ''
    const prev = row.enabled
    return guarded(
      () => {
        row.enabled = !row.enabled
      },
      () => {
        row.enabled = prev
      },
    )
  }

  // 重名冲突检测：导入前由组件提示"将覆盖 N 个同名项"（同名即覆盖是既有语义，
  // 但静默覆盖危险操作——必须让用户先知道）
  function mcpNameConflicts(names: string[]): string[] {
    return names.map((n) => n.trim()).filter((n) => n && mcp.value.some((x) => x.name === n))
  }

  function skillNameConflicts(names: string[]): string[] {
    return names.map((n) => n.trim()).filter((n) => n && skills.value.some((x) => x.name === n))
  }

  return {
    mcp,
    skills,
    busy,
    load: reload,
    upsertMcp,
    importMcp,
    importSkills,
    removeMcp,
    toggleMcp,
    upsertSkill,
    removeSkill,
    toggleSkill,
    mcpNameConflicts,
    skillNameConflicts,
  }
})
