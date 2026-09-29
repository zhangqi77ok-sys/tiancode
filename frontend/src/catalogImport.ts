// 主流导入格式：
// MCP — Claude Desktop / Claude Code / Cursor 的 mcp.json（mcpServers），
//       以及 VS Code Copilot 的 servers / mcp.servers；stdio（command+args）或远程（url）。
// Skill — agentskills / Claude Code / Codex 的 SKILL.md（YAML frontmatter + 正文）。

export interface McpDraft {
  name: string
  transport: 'stdio' | 'http'
  command: string
  args: string
  env: string
  url: string
  headers: string
  enabled: boolean
}

export interface SkillDraft {
  name: string
  description: string
  body: string
  enabled: boolean
}

export function parseMcpConfig(raw: string): { servers: McpDraft[]; error: string } {
  const text = raw.trim()
  if (!text) return { servers: [], error: '请粘贴 mcp.json，或选择配置文件' }
  let data: unknown
  try {
    data = JSON.parse(text)
  } catch {
    return { servers: [], error: '不是合法 JSON。请粘贴 Claude / Cursor 的 mcp.json（含 mcpServers）' }
  }
  const map = serverMap(data)
  if (!map) return { servers: [], error: '没找到 MCP 服务器。需要 mcpServers、servers，或带 command/url 的对象' }
  const servers: McpDraft[] = []
  for (const [name, spec] of Object.entries(map)) {
    const draft = toDraft(name, spec)
    if (draft) servers.push(draft)
  }
  if (!servers.length) return { servers: [], error: '配置里没有可导入的 command 或 url' }
  return { servers, error: '' }
}

export function parseSkillMarkdown(raw: string, fallbackName: string): SkillDraft | null {
  const text = raw.replace(/^\uFEFF/, '').trim()
  if (!text) return null
  let name = fallbackName.trim() || '未命名技能'
  let description = ''
  let body = text
  const fm = text.match(/^---\r?\n([\s\S]*?)\r?\n---\r?\n?([\s\S]*)$/)
  if (fm) {
    const meta = fm[1]
    body = fm[2].trim()
    const n = frontValue(meta, 'name')
    const d = frontValue(meta, 'description')
    if (n) name = n
    if (d) description = d
  }
  if (!body && !description) return null
  return { name, description, body, enabled: true }
}

function frontValue(block: string, key: string): string {
  const re = new RegExp('^' + key + '\\s*:\\s*(.*)$', 'im')
  const m = block.match(re)
  if (!m) return ''
  return m[1].trim().replace(/^['"]|['"]$/g, '')
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return !!v && typeof v === 'object' && !Array.isArray(v)
}

function serverMap(data: unknown): Record<string, unknown> | null {
  if (!isRecord(data)) return null
  const nested =
    data.mcpServers ??
    data.mcp_servers ??
    (isRecord(data.mcp) ? data.mcp.servers : undefined) ??
    data.servers
  if (isRecord(nested)) return nested
  if (looksLikeServer(data)) return { imported: data }
  const values = Object.values(data)
  if (values.length && values.every(looksLikeServer)) return data
  return null
}

function looksLikeServer(v: unknown): boolean {
  if (!isRecord(v)) return false
  return typeof v.command === 'string' || typeof v.url === 'string' || typeof v.serverUrl === 'string'
}

function toDraft(name: string, spec: unknown): McpDraft | null {
  if (!isRecord(spec)) return null
  const command = str(spec.command)
  const url = str(spec.url) || str(spec.serverUrl)
  const typ = str(spec.type).toLowerCase()
  const http = typ === 'http' || typ === 'sse' || typ === 'streamable-http' || (!command && !!url)
  if (!command && !url) return null
  const args = spec.args
  const argText = Array.isArray(args) ? args.map((a) => String(a)).join(' ') : str(args)
  return {
    name: name.trim() || 'imported',
    transport: http ? 'http' : 'stdio',
    command,
    args: argText,
    env: kvLines(spec.env),
    url,
    headers: kvLines(spec.headers),
    enabled: spec.disabled === true ? false : true,
  }
}

function str(v: unknown): string {
  return typeof v === 'string' ? v.trim() : ''
}

function kvLines(v: unknown): string {
  if (!isRecord(v)) return ''
  return Object.entries(v)
    // 非字符串值统一字符串化：mcp.json 里 "PORT": 3000 这类数字/布尔此前被
    // 静默丢弃——导入后进程环境不完整（0.2.27 修复）。null/undefined 跳过。
    .filter(([, val]) => val !== null && val !== undefined)
    .map(([k, val]) => `${k}=${typeof val === 'string' ? val : String(val)}`)
    .join('\n')
}
