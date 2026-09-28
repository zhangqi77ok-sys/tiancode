import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { McpDraft, SkillDraft } from '../catalogImport'
import { bridge } from '../wails'

// MCP 服务器与 Skill 的本机清单。存在浏览器存储里，和会话账本分开。
// 清单同时写入本机 extensions.json。启用项会在下一轮对话里告诉模型。

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

export const useCatalogStore = defineStore('catalog', () => {
  const mcp = ref<McpServer[]>(load<McpServer>(MCP_KEY).map(normalizeMcp))
  const skills = ref<SkillItem[]>(load<SkillItem>(SKILL_KEY))

  async function persist() {
    await bridge().app.SaveExtensions({ mcp: mcp.value, skills: skills.value })
    localStorage.setItem(MCP_KEY, JSON.stringify(mcp.value))
    localStorage.setItem(SKILL_KEY, JSON.stringify(skills.value))
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

  async function upsertMcp(item: Omit<McpServer, 'id'> & { id?: string }) {
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
    if (i >= 0) mcp.value[i] = { ...next, id: mcp.value[i].id }
    else mcp.value.push(next)
    await persist()
    return ''
  }

  async function importMcp(drafts: McpDraft[]) {
    for (const d of drafts) {
      const msg = await upsertMcp(d)
      if (msg) return msg
    }
    return ''
  }

  async function removeMcp(id: string) {
    mcp.value = mcp.value.filter((x) => x.id !== id)
    await persist()
  }

  async function toggleMcp(id: string) {
    const row = mcp.value.find((x) => x.id === id)
    if (!row) return
    row.enabled = !row.enabled
    await persist()
  }

  async function importSkills(drafts: SkillDraft[]) {
    for (const d of drafts) {
      const msg = await upsertSkill(d)
      if (msg) return msg
    }
    return ''
  }

  async function upsertSkill(item: Omit<SkillItem, 'id'> & { id?: string }) {
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
    if (i >= 0) skills.value[i] = { ...next, id: skills.value[i].id }
    else skills.value.push(next)
    await persist()
    return ''
  }

  async function removeSkill(id: string) {
    skills.value = skills.value.filter((x) => x.id !== id)
    await persist()
  }

  async function toggleSkill(id: string) {
    const row = skills.value.find((x) => x.id === id)
    if (!row) return
    row.enabled = !row.enabled
    await persist()
  }

  return {
    mcp,
    skills,
    load: reload,
    upsertMcp,
    importMcp,
    importSkills,
    removeMcp,
    toggleMcp,
    upsertSkill,
    removeSkill,
    toggleSkill,
  }
})
