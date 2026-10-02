<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useChatStore } from '../stores/chat'
import { useCatalogStore } from '../stores/catalog'
import { useChannelStore } from '../stores/channels'
import { useTonesStore } from '../stores/tones'
import { useWorkspaceStore } from '../stores/workspace'
import { buildSidebar, filterSidebar } from '../composables/sessionGrouping'
import { useDialogs } from '../composables/useDialogs'
import { useEscClose } from '../composables/useEsc'
import AppIcon from './AppIcon.vue'
import BaseModal from './BaseModal.vue'
import SessionRow from './SessionRow.vue'

// 会话侧栏（三段式，对齐商用 AI 工具）：置顶 / 会话（未归属空间）/ 空间（按工作区分组）。
// 双动作入口：新建对话（当前工作区）+ 打开工作区（切换后新对话归属该空间）。
// 每个列表默认显示 5 条，超出折叠为"查看更多 (N)"。
// 底部导航：模型/渠道/技能/MCP 的**统一**设置入口（0.0.06——此前拆在汉堡导航与
// 侧栏底部两处，现已收敛到这一处；汉堡导航整体下线）。
// 折叠成窄轨（第 8 批）：Ctrl+B 收起后只剩图标（新建 / 设置入口 / 展开），
// 文件详情面板占右侧时对话列不再被挤扁。状态由 App 持有（localStorage 持久化），
// 这里只负责渲染与请求切换。
const props = defineProps<{ collapsed?: boolean }>()
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'open-channels'): void
  (e: 'open-mcp'): void
  (e: 'open-skills'): void
  (e: 'open-tones'): void
  (e: 'open-memory'): void
  (e: 'open-workspace-settings'): void
  (e: 'toggle-collapsed'): void
}>()

const store = useChatStore()
const channels = useChannelStore()
const ws = useWorkspaceStore()
const dialogs = useDialogs()

// ---- 统一设置入口（0.0.06）：模型 / 渠道 / 技能 / MCP 收成侧栏底部一处——
// 此前入口拆在汉堡导航与侧栏底部两处，用户不知道去哪找。模型名在这里是
// 主信息（正文字号、正常色），不再是 11px 浅色次要字。
const catalog = useCatalogStore()
const tones = useTonesStore()
const settingsMenuOpen = ref(false)
const settingsMenuRef = ref<HTMLElement | null>(null)

// Esc 关闭设置菜单（第 3 批）：经消费栈注册（原先只有点外部关闭）
useEscClose(settingsMenuOpen, () => {
  settingsMenuOpen.value = false
})
const mcpOn = computed(() => catalog.mcp.filter((s) => s.enabled).length)
const skillOn = computed(() => catalog.skills.filter((s) => s.enabled).length)
// 按钮 hover 提示：一句话说明当前模型（版面不显示，信息不丢）
const settingsTitle = computed(() =>
  channels.activeModel
    ? `设置 · 当前模型 ${channels.activeModel}${channels.activeChannel ? `（${channels.activeChannel.name}）` : ''}`
    : '设置 · 未配置模型，点击进入模型与渠道管理',
)

function onSettingsMousedown(e: MouseEvent) {
  if (!settingsMenuOpen.value) return
  const el = settingsMenuRef.value
  if (el && !el.contains(e.target as Node)) settingsMenuOpen.value = false
}
onMounted(() => {
  document.addEventListener('mousedown', onSettingsMousedown)
  // 语气设置（第 8 批）：底部入口要显示当前语气。这里失败不拦界面（只影响那行后缀，
  // 不显示即"不知道"），真正的错误在面板打开时再读一次并原样显示。
  if (!tones.loaded) void tones.load()
})
onBeforeUnmount(() => document.removeEventListener('mousedown', onSettingsMousedown))

function openSettings(kind: 'channels' | 'mcp' | 'skills' | 'tones' | 'memory') {
  settingsMenuOpen.value = false
  if (kind === 'channels') emit('open-channels')
  else if (kind === 'mcp') emit('open-mcp')
  else if (kind === 'skills') emit('open-skills')
  else if (kind === 'memory') emit('open-memory')
  else emit('open-tones')
}

const VIEW_LIMIT = 5

// 本地搜索（0.0.11）：标题 / 工作区路径子串过滤，纯前端（无新后端接口）。
const query = ref('')
const filtering = computed(() => query.value.trim().length > 0)

// 分区模型（纯函数 + 单测）：sections = 置顶 / 会话 / 空间；只镜像账本，草稿会话不可见
const sections = computed(() => {
  const all = buildSidebar(store.summaries, ws.path)
  return filtering.value ? filterSidebar(all, query.value) : all
})

// 折叠状态：分区头与空间分组头共用（当前空间默认展开，其余默认收起；点击后以手动为准）
const collapsed = ref<Record<string, boolean>>({})
function toggle(key: string) {
  collapsed.value[key] = !collapsed.value[key]
}
function isOpen(key: string, defaultOpen: boolean): boolean {
  return collapsed.value[key] ?? defaultOpen
}
// 分组是否展开：搜索中默认展开——用户正在找东西，不该再点一次才看见结果
function groupOpen(label: string, isCurrent: boolean): boolean {
  return isOpen('fold:' + label, filtering.value || isCurrent)
}

// 查看更多：每个列表独立展开态；搜索中不受 5 条限制（命中项全列）
const expanded = ref<Record<string, boolean>>({})
function visible<T extends { id: string }>(items: T[], key: string): T[] {
  if (filtering.value) return items
  return expanded.value[key] ? items : items.slice(0, VIEW_LIMIT)
}
// 查看更多点击：切换 expanded（0.2.10 曾误绑 toggle 折叠态——改的是另一张表，点击永远无效）
function toggleMore(key: string) {
  expanded.value[key] = !expanded.value[key]
}

// 打开工作区：系统目录选择框 → 切换 → 回到草稿态（等于在该空间开新对话）。
// 归属由首条消息落账本时的快照决定，天然记到新空间名下；草稿不进侧栏，反复切换不堆积空会话。
// 多会话（0.2.25）：进行中的轮次持有自己的工具集快照，切工作区不再影响它们，无需禁止。
async function openWorkspace() {
  const ok = await ws.pickAndSet()
  if (ok) {
    await store.newSession()
    emit('close') // 窄屏抽屉收起；桌面端无副作用
  }
}

// 在已有工作区新建会话（空间组头 ＋）：直接切入（无目录选择器）→ 草稿。
// 这是"灵活选择已有工作区新建会话"的最短路径。
async function newInWorkspace(dir: string) {
  await ws.setPath(dir)
  await store.newSession()
  emit('close')
}

// 重命名：对话框返回 null 视为放弃；60 字上限与后端契约一致
async function rename(id: string) {
  const next = await dialogs.prompt({
    title: '重命名会话',
    message: '会话标题（最多 60 字）',
    value: store.titleOf(id),
    maxlength: 60,
  })
  if (next === null) return
  await store.renameSession(id, next)
}

// 删除：不可逆操作，先确认（消息删除后无法从界面找回）。
// 运行中的会话由 store.removeSession 显式拒绝（错误可见），不在这里静默拦。
async function remove(id: string) {
  const ok = await dialogs.confirm({
    title: '删除会话',
    message: `删除会话「${store.titleOf(id)}」？\n该会话的全部消息将被移除，且无法恢复。`,
    confirmText: '删除',
    danger: true,
  })
  if (ok) await store.removeSession(id)
}

function select(id: string) {
  void store.selectSession(id)
  emit('close') // 窄屏选中后收起抽屉；桌面端该事件无副作用
}

// ---- 移动到空间（0.0.19）----
// 归属是账本事实（workspace_move 事件），展示与工具根同一规则——迁移后侧栏分组、
// 顶栏标签、下一轮文件操作一起切到新空间。候选 = 用户用过的全部工作区
//（summaries 归属去重 + 当前根）；也允许移出空间（纯对话归属，工具下线）。
const moveTarget = ref('') // 待迁移的会话 ID（'' = 关闭）
const moveTitle = computed(() => (moveTarget.value ? store.titleOf(moveTarget.value) : ''))
const moveOptions = computed(() => {
  const set = new Set<string>()
  if (ws.path) set.add(ws.path)
  for (const sm of store.summaries) if (sm.workspace) set.add(sm.workspace)
  return [...set]
})
function openMove(id: string) {
  moveTarget.value = id
}
async function doMove(dir: string) {
  const id = moveTarget.value
  moveTarget.value = ''
  if (id) await store.moveSession(id, dir)
}
</script>

<template>
  <aside
    aria-label="会话列表"
    class="flex shrink-0 flex-col bg-[var(--c-surface)] p-3"
    :class="props.collapsed ? 'w-14' : 'w-64'"
  >
    <!-- 窄轨（第 8 批）：只剩图标——展开 / 新建 / 设置入口；搜索与会话列表整体隐藏 -->
    <template v-if="props.collapsed">
      <button
        class="chip mb-2 justify-center px-0 py-2"
        title="展开侧栏（Ctrl+B）"
        aria-label="展开侧栏"
        @click="emit('toggle-collapsed')"
      >
        <AppIcon name="chevron-down" :size="14" class="-rotate-90" />
      </button>
      <button
        class="btn-primary mb-2 justify-center gap-0 px-0 py-2"
        :title="ws.path ? `在当前工作区（${ws.path}）新建对话` : '新建通用对话（未选择工作区）'"
        aria-label="新建对话"
        @click="store.newSession(); emit('close')"
      >
        <AppIcon name="plus" :size="16" />
      </button>
    </template>

    <!-- 双动作入口：新建对话（当前工作区）+ 打开工作区（切换归属） -->
    <div v-if="!props.collapsed" class="mb-3 flex gap-1.5">
      <button
        class="btn-primary min-w-0 flex-1 gap-1.5 py-2 text-sm"
        :title="ws.path ? `在当前工作区（${ws.path}）新建对话` : '新建通用对话（未选择工作区）'"
        @click="store.newSession(); emit('close')"
      >
        <AppIcon name="plus" :size="14" /> 新建对话
      </button>
      <button
        class="chip shrink-0 gap-1.5 px-3"
        title="打开工作区（切换并自动新建对话）"
        @click="openWorkspace"
      >
        <AppIcon name="folder" :size="14" /> 打开
      </button>
    </div>

    <!-- 本地搜索（0.0.11）：标题与工作区路径子串匹配；命中分组自动展开、不限 5 条 -->
    <input
      v-if="!props.collapsed"
      v-model="query"
      type="search"
      aria-label="搜索会话"
      placeholder="搜索会话或工作区…"
      class="mb-2 w-full rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-1.5 text-xs text-[var(--c-text)] outline-none focus:border-[var(--c-primary)]"
    />

    <div v-if="!props.collapsed" class="min-h-0 flex-1 space-y-2 overflow-y-auto">
      <p v-if="filtering && !sections.length" class="px-2 py-3 text-xs text-[var(--c-text-faint)]">
        没有匹配的会话
      </p>
      <template v-for="sec in sections" :key="sec.kind + sec.label">
        <!-- 置顶 / 会话：单列表 -->
        <div v-if="sec.kind !== 'spaces'" class="mb-1">
          <div class="flex items-center gap-1.5 px-2 py-1 text-[11px] font-medium text-[var(--c-text-faint)]">
            <AppIcon :name="sec.kind === 'pinned' ? 'star' : 'message'" :size="11" />
            {{ sec.label }} ({{ sec.items.length }})
          </div>
          <div class="space-y-0.5">
            <SessionRow
              v-for="sm in visible(sec.items, sec.kind)"
              :key="sm.id"
              :id="sm.id"
              :title="sm.title || sm.id"
              :last-active-ms="sm.lastActiveMs"
              :pinned="sm.pinned"
              :running="store.isRunning(sm.id)"
              :pending="store.pendingOf(sm.id)"
              @select="select(sm.id)"
              @pin="(p) => store.pinSession(sm.id, p)"
              @rename="rename(sm.id)"
              @remove="remove(sm.id)"
              @move="openMove(sm.id)"
            />
          </div>
          <button
            v-if="!filtering && sec.items.length > VIEW_LIMIT"
            class="w-full rounded-lg px-2 py-1.5 text-left text-[11px] text-[var(--c-primary)] transition-colors hover:bg-[var(--c-primary-soft)]"
            @click="toggleMore(sec.kind)"
          >
            {{ expanded[sec.kind] ? '收起' : `查看更多 (${sec.items.length - VIEW_LIMIT})` }}
          </button>
        </div>

        <!-- 空间：按工作区的嵌套分组 -->
        <div v-else class="mb-1">
          <div class="flex items-center gap-1.5 px-2 py-1 text-[11px] font-medium text-[var(--c-text-faint)]">
            <AppIcon name="folder" :size="11" /> {{ sec.label }} ({{ sec.groups.length }})
          </div>
          <div v-for="g in sec.groups" :key="g.label" class="mb-1">
            <!-- 组头：折叠按钮 + 在该工作区新建（hover/focus 显现，不挤占常态视觉） -->
            <div
              class="group flex items-center gap-1 rounded-lg px-2 py-1.5 text-[11px] text-[var(--c-text-dim)] transition-colors hover:bg-[var(--c-surface-soft)]"
            >
              <button
                class="flex min-w-0 flex-1 items-center gap-1.5 text-left"
                :aria-expanded="groupOpen(g.label, g.isCurrent)"
                @click="toggle('fold:' + g.label)"
              >
                <AppIcon
                  name="chevron-down"
                  :size="10"
                  class="shrink-0 transition-transform"
                  :class="groupOpen(g.label, g.isCurrent) ? '' : '-rotate-90'"
                />
                <AppIcon
                  name="folder"
                  :size="11"
                  class="shrink-0"
                  :class="g.isCurrent ? 'text-[var(--c-primary)]' : ''"
                />
                <span
                  class="min-w-0 flex-1 truncate"
                  :class="g.isCurrent ? 'font-medium text-[var(--c-primary)]' : ''"
                >
                  {{ g.label }}
                </span>
                <span class="shrink-0 text-[10px] text-[var(--c-text-faint)]">{{ g.items.length }}</span>
              </button>
              <button
                class="shrink-0 rounded p-0.5 text-[var(--c-text-faint)] opacity-0 transition-opacity hover:text-[var(--c-primary)] focus-visible:opacity-100 group-hover:opacity-100"
                :title="`在 ${g.label} 新建对话`"
                :aria-label="`在 ${g.label} 新建对话`"
                @click="newInWorkspace(g.workspace)"
              >
                <AppIcon name="plus" :size="12" />
              </button>
            </div>

            <div v-if="groupOpen(g.label, g.isCurrent)" class="mt-0.5 space-y-0.5">
              <SessionRow
                v-for="sm in visible(g.items, 'more:' + g.label)"
                :key="sm.id"
                :id="sm.id"
                :title="sm.title || sm.id"
                :last-active-ms="sm.lastActiveMs"
                :pinned="sm.pinned"
                :running="store.isRunning(sm.id)"
              :pending="store.pendingOf(sm.id)"
                @select="select(sm.id)"
                @pin="(p) => store.pinSession(sm.id, p)"
                @rename="rename(sm.id)"
                @remove="remove(sm.id)"
                @move="openMove(sm.id)"
              />
            </div>
            <!-- 查看更多与文件夹折叠用不同 key，互不打架；搜索中不折叠到 5 条 -->
            <button
              v-if="!filtering && g.items.length > VIEW_LIMIT && groupOpen(g.label, g.isCurrent)"
              class="w-full rounded-lg px-2 py-1.5 text-left text-[11px] text-[var(--c-primary)] transition-colors hover:bg-[var(--c-primary-soft)]"
              @click="toggleMore('more:' + g.label)"
            >
              {{ expanded['more:' + g.label] ? '收起' : `查看更多 (${g.items.length - VIEW_LIMIT})` }}
            </button>
          </div>
        </div>
      </template>
    </div>

    <div v-if="store.error" class="mt-2 px-1 text-xs text-[var(--c-err-text)]">{{ store.error }}</div>

    <!-- 底部设置入口（0.0.12 重排）：一行 = 图标 + "设置" + 状态点 + 展开指示；
         菜单向上弹，与条目共用同一列网格（图标列 / 文本列 / 右侧状态列），
         当前模型收进菜单顶部当"标题块"（带图标，与条目图标同列） -->
    <div ref="settingsMenuRef" class="relative mt-2 border-t border-[var(--c-border)] px-1 pt-2">
      <button
        class="flex w-full items-center gap-2 rounded-lg py-2 text-[13px] transition-colors"
        :class="[
          props.collapsed ? 'justify-center px-0' : 'px-3',
          settingsMenuOpen
            ? 'bg-[var(--c-surface-soft)] text-[var(--c-text)]'
            : 'text-[var(--c-text-dim)] hover:bg-[var(--c-surface-soft)] hover:text-[var(--c-text)]',
        ]"
        aria-haspopup="menu"
        :aria-expanded="settingsMenuOpen"
        :title="settingsTitle"
        @click="settingsMenuOpen = !settingsMenuOpen"
      >
        <AppIcon name="sliders" :size="14" class="shrink-0" />
        <span v-if="!props.collapsed" class="min-w-0 flex-1 text-left">设置</span>
        <template v-if="!props.collapsed">
          <span
            class="h-1.5 w-1.5 shrink-0 rounded-full"
            :class="channels.activeModel ? 'bg-[var(--c-ok)]' : 'bg-[var(--c-warn)]'"
            aria-hidden="true"
          ></span>
          <AppIcon
            name="chevron-down"
            :size="12"
            class="shrink-0 rotate-180 text-[var(--c-text-faint)]"
            aria-hidden="true"
          />
        </template>
      </button>
      <div
        v-if="settingsMenuOpen"
        role="menu"
        class="absolute bottom-full left-0 z-40 mb-1.5 rounded-xl border border-[var(--c-border)] bg-[var(--c-surface)] p-1.5 shadow-[var(--shadow-float)]"
        :class="props.collapsed ? 'w-56' : 'w-full'"
      >
        <!-- 当前模型：带图标的一行块——图标与条目图标同列，文本与条目文本同列 -->
        <div class="flex items-center gap-2 px-2 py-2">
          <AppIcon name="message" :size="14" class="shrink-0 text-[var(--c-text-faint)]" aria-hidden="true" />
          <div class="min-w-0 flex-1">
            <p class="truncate text-xs font-medium text-[var(--c-text)]" :title="channels.activeModel">
              {{ channels.activeModel || '未选择模型' }}
            </p>
            <p
              class="mt-0.5 truncate text-[11px]"
              :class="channels.activeChannel ? 'text-[var(--c-text-dim)]' : 'text-[var(--c-warn-text)]'"
            >
              {{ channels.activeChannel ? channels.activeChannel.name : '未配置渠道' }}
            </p>
          </div>
        </div>
        <div class="my-1 h-px bg-[var(--c-border)]"></div>
        <button role="menuitem" class="menu-item" @click="openSettings('channels')">
          <AppIcon name="sliders" :size="14" class="shrink-0 text-[var(--c-text-dim)]" />
          <span class="min-w-0 flex-1">模型与渠道管理</span>
        </button>
        <button role="menuitem" class="menu-item" @click="openSettings('mcp')">
          <AppIcon name="plug" :size="14" class="shrink-0 text-[var(--c-text-dim)]" />
          <span class="min-w-0 flex-1">MCP 管理</span>
          <span class="shrink-0 pl-2 text-[11px] text-[var(--c-text-faint)]">
            {{ catalog.mcp.length ? `已启用 ${mcpOn}/${catalog.mcp.length}` : '未添加' }}
          </span>
        </button>
        <button role="menuitem" class="menu-item" @click="openSettings('skills')">
          <AppIcon name="book" :size="14" class="shrink-0 text-[var(--c-text-dim)]" />
          <span class="min-w-0 flex-1">Skill 管理</span>
          <span class="shrink-0 pl-2 text-[11px] text-[var(--c-text-faint)]">
            {{ catalog.skills.length ? `已启用 ${skillOn}/${catalog.skills.length}` : '未添加' }}
          </span>
        </button>
        <!-- 语气（第 8 批）：固定一条 / 按每条消息自动选——与模型、渠道、MCP、技能并列，
             入口只在侧栏底部（不进顶栏）。后缀显示当前语气，读不到就不显示。 -->
        <button role="menuitem" class="menu-item" @click="openSettings('tones')">
          <AppIcon name="message" :size="14" class="shrink-0 text-[var(--c-text-dim)]" />
          <span class="min-w-0 flex-1">语气</span>
          <span v-if="tones.loaded" class="shrink-0 pl-2 text-[11px] text-[var(--c-text-faint)]">
            {{ tones.label }}
          </span>
        </button>
        <!-- 工作区设置（第 8 批）：在这一行打开 / 检查命令——按工作区存，不进顶栏 -->
        <button role="menuitem" class="menu-item" @click="emit('open-workspace-settings'); settingsMenuOpen = false">
          <AppIcon name="wrench" :size="14" class="shrink-0 text-[var(--c-text-dim)]" />
          <span class="min-w-0 flex-1">工作区设置</span>
        </button>
        <!-- 记忆（0.0.21）：模型能记的用户必须看得见、删得掉——透明度红线 -->
        <button role="menuitem" class="menu-item" @click="openSettings('memory')">
          <AppIcon name="book" :size="14" class="shrink-0 text-[var(--c-text-dim)]" />
          <span class="min-w-0 flex-1">记忆</span>
        </button>
      </div>
    </div>
  <!-- 迁移空间（0.0.19）：归属 = 账本事实，确认后落 workspace_move 事件 -->
  <BaseModal :open="!!moveTarget" :title="`移动「${moveTitle}」到空间`" @close="moveTarget = ''">
    <div class="space-y-1">
      <p class="px-1 pb-1 text-xs text-[var(--c-text-dim)]">
        归属决定侧栏分组与这场对话读写的工作区；运行中的会话不能迁移。
      </p>
      <button
        v-for="w in moveOptions"
        :key="w"
        class="menu-item"
        @click="doMove(w)"
      >
        <AppIcon name="folder" :size="13" class="shrink-0 text-[var(--c-text-faint)]" />
        <span class="min-w-0 flex-1 truncate text-left" :title="w">{{ w }}</span>
      </button>
      <div class="my-1 h-px bg-[var(--c-border)]"></div>
      <button class="menu-item" @click="doMove('')">
        <AppIcon name="message" :size="13" class="shrink-0 text-[var(--c-text-faint)]" />
        <span class="flex-1 text-left">移出空间（纯对话，文件工具下线）</span>
      </button>
    </div>
  </BaseModal>
  </aside>
</template>
