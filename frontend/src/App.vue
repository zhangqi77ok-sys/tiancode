<template>
  <div class="h-full w-full bg-[#FAF8F5] text-[#18181B] flex flex-col font-sans select-none overflow-hidden antialiased tian-root">
    <header style="--wails-draggable:drag" class="h-[38px] min-h-[38px] bg-[#FAF8F5] border-b border-black/[0.08] flex items-center justify-between px-3 z-30 select-none">
      <div style="--wails-draggable:no-drag" class="flex items-center gap-2">
        <div class="w-5 h-5 rounded-md bg-[#18181B] text-white flex items-center justify-center font-bold text-xs shadow-xs">湉</div>
        <span class="text-xs font-semibold tracking-tight text-[#18181B]">湉码</span>
        <span class="text-[#A1A1AA] text-xs">/</span>
        <button
          @click="s.chooseWorkspace"
          style="--wails-draggable:no-drag"
          class="flex items-center gap-1.5 px-2 py-0.5 rounded-md hover:bg-black/[0.05] transition-all cursor-pointer group text-xs font-medium text-[#27272A]"
          title="点击切换工作区文件夹 (系统原生文件夹对话框)"
        >
          <span class="text-[#D96B27]">📁</span>
          <span class="group-hover:text-[#D96B27] max-w-[160px] truncate">{{ s.workspaceName }}</span>
          <span class="text-[10px] text-[#71717A] bg-black/[0.04] px-1.5 py-0.2 rounded-full font-mono">{{ s.gitBranchLabel }}</span>
        </button>
        <div class="h-3 w-[1px] bg-black/[0.08] mx-1"></div>
        <div
          :class="['flex items-center gap-1.5 text-[11px] font-medium px-2 py-0.5 rounded-full transition-colors', s.modelHealthStatus.badgeClass]"
          :title="s.primaryChannel ? `${s.primaryChannel.name} (${s.primaryChannel.endpoint}) · 延迟: ${s.primaryChannel.latency || '未测速'}` : '未配置主模型渠道，请前往设置配置'"
        >
          <span :class="['w-1.5 h-1.5 rounded-full', s.modelHealthStatus.dotClass]"></span>
          <span>{{ s.selectedModel || '未选择模型' }} · {{ s.modelHealthStatus.text }}</span>
        </div>
      </div>

      <button
        style="--wails-draggable:no-drag"
        @click="s.openCommandPalette()"
        class="hidden md:flex items-center gap-1.5 px-3 py-1 rounded-xl bg-black/[0.04] hover:bg-black/[0.07] border border-black/[0.06] text-xs text-[#71717A] cursor-pointer shadow-2xs"
        title="全局快速命令 (Ctrl+K)"
      >
        <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
        <span class="text-[11px] font-medium">跳转：设置 / 会话 / 已打开文件</span>
        <kbd class="text-[10px] font-mono px-1.5 py-0.2 rounded bg-white text-[#71717A] border border-black/[0.08]">Ctrl+K</kbd>
      </button>

      <div style="--wails-draggable:no-drag" class="flex items-center p-0.5 bg-black/[0.05] rounded-xl text-xs font-medium">
        <button @click="s.setWorkspaceView('chat')" :class="['px-2.5 py-1 rounded-lg flex items-center gap-1.5 cursor-pointer', s.workspaceView === 'chat' ? 'bg-white text-[#D96B27] shadow-2xs font-semibold' : 'text-[#71717A]']">智能对话</button>
        <button @click="s.setWorkspaceView('split')" :class="['px-2.5 py-1 rounded-lg flex items-center gap-1.5 cursor-pointer', s.workspaceView === 'split' ? 'bg-white text-[#D96B27] shadow-2xs font-semibold' : 'text-[#71717A]']">
          <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="12" y1="3" x2="12" y2="21"/></svg>
          双栏协同
        </button>
        <button @click="s.setWorkspaceView('editor')" :class="['px-2.5 py-1 rounded-lg flex items-center gap-1.5 cursor-pointer', s.workspaceView === 'editor' ? 'bg-white text-[#D96B27] shadow-2xs font-semibold' : 'text-[#71717A]']">文件与编辑器</button>
      </div>

      <div style="--wails-draggable:no-drag" class="flex items-center gap-2">
        <button
          @click="s.toggleTerminalDrawer()"
          class="flex items-center gap-1.5 px-2.5 py-1 rounded-md text-xs font-medium text-[#52525B] bg-white border border-black/[0.08] shadow-2xs hover:bg-black/[0.03] cursor-pointer"
        >
          <span class="font-mono font-bold text-[#18181B]">$_</span><span>终端抽屉</span>
        </button>
        <button
          @click="s.isSettingsOpen = true"
          class="flex items-center gap-1.5 px-2.5 py-1 rounded-md text-xs font-medium text-[#18181B] bg-white border border-black/[0.08] shadow-2xs hover:bg-black/[0.03] cursor-pointer"
        >
          <svg class="w-3.5 h-3.5 text-[#D96B27]" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0-2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>
          <span>模型与设置</span>
        </button>
        <div class="h-3 w-[1px] bg-black/[0.08]"></div>
        <div class="flex items-center gap-1">
          <button @click="wailsBridge.windowMinimise()" class="w-6 h-6 rounded flex items-center justify-center hover:bg-black/[0.05] text-[#71717A] cursor-pointer" title="最小化">
            <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="5" y1="12" x2="19" y2="12"/></svg>
          </button>
          <button @click="wailsBridge.windowToggleMaximise()" class="w-6 h-6 rounded flex items-center justify-center hover:bg-black/[0.05] text-[#71717A] cursor-pointer" title="最大化/还原">
            <svg class="w-3 h-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="18" height="18" rx="2"/></svg>
          </button>
          <button @click="wailsBridge.windowClose()" class="w-6 h-6 rounded flex items-center justify-center hover:bg-red-500 hover:text-white text-[#71717A] cursor-pointer" title="关闭">
            <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
          </button>
        </div>
      </div>
    </header>
    <div class="flex-1 flex overflow-hidden relative">
      <ActivityBar />
      <LeftDrawer />
      <div class="flex-1 flex flex-col overflow-hidden relative">
        <div class="flex-1 flex overflow-hidden relative">
          <ChatCockpit v-show="s.workspaceView !== 'editor'" />
          <DiffWorkspace />
        </div>
        <TerminalDrawer />
      </div>
    </div>
    <div
      v-if="s.isSettingsOpen"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/45 backdrop-blur-xs animate-in fade-in duration-150 font-sans"
    >
      <div class="w-[90vw] max-w-[1050px] h-[82vh] bg-white rounded-2xl shadow-2xl border border-black/[0.1] flex flex-col overflow-hidden relative">
        <header class="h-12 bg-[#FAF8F5] border-b border-black/[0.08] flex items-center justify-between px-5 select-none shrink-0">
          <div class="flex items-center gap-2">
            <span class="text-base">⚙️</span>
            <span class="font-bold text-sm text-[#18181B]">系统设置中枢 (Settings Hub)</span>
          </div>
          <button @click="s.isSettingsOpen = false" class="p-1.5 rounded-lg text-[#71717A] hover:bg-black/[0.05] cursor-pointer">✕</button>
        </header>

        <div class="flex-1 flex overflow-hidden">
          <!-- 左侧菜单 -->
          <aside class="w-48 bg-[#F4EFEA] border-r border-black/[0.08] p-3 space-y-1 select-none shrink-0">
            <button
              v-for="m in [
                { id: 'models', label: '🌐 模型与网关渠道' },
                { id: 'mcp', label: '🧩 MCP 服务协议' },
                { id: 'skills', label: '🛠️ Agent 技能库' },
                { id: 'rules', label: '📜 软件规则与提示词' },
                { id: 'theme', label: '🎨 外观与字号' },
                { id: 'sandbox', label: '🛡️ 安全隔离机制' },
                { id: 'about', label: 'ℹ️ 关于系统' },
                { id: 'lab', label: '🧪 实验特性' }
              ]"
              :key="m.id"
              @click="s.activeSettingsTab = m.id"
              :class="[
                'w-full text-left px-3 py-2 rounded-xl text-xs transition-all cursor-pointer',
                s.activeSettingsTab === m.id ? 'font-bold bg-white text-[#D96B27] shadow-xs' : 'text-[#71717A] hover:bg-black/[0.04]'
              ]"
            >
              {{ m.label }}
            </button>
          </aside>

          <!-- 右侧选项卡主体 -->
          <main class="flex-1 p-5 overflow-y-auto bg-white space-y-4">
            <!-- 选项卡 1: 模型与网关 -->
            <div v-if="s.activeSettingsTab === 'models'" class="space-y-4">
              <div class="flex items-center justify-between">
                <div>
                  <h3 class="text-xs font-bold text-[#18181B]">活跃网关与模型渠道</h3>
                  <p class="text-[11px] text-[#71717A]">已读写 ~/.tiancode/channels.json · 支持实时在线探活测速</p>
                </div>
                <div class="flex items-center gap-2">
                  <button @click="s.pingAllChannels" class="px-3 py-1.5 rounded-xl border border-black/[0.1] text-xs font-medium hover:bg-black/[0.02] cursor-pointer">
                    ⚡ 探测全部通道
                  </button>
                  <button @click="s.openAddChannelModal" class="px-3 py-1.5 rounded-xl bg-[#D96B27] text-white text-xs font-bold shadow-xs hover:bg-[#B8551B] cursor-pointer">
                    ➕ 新增渠道
                  </button>
                </div>
              </div>

              <!-- 渠道卡片列表 -->
              <div class="space-y-2">
                <div v-if="s.channels.length === 0" class="p-8 text-center bg-[#FAF8F5] rounded-xl border border-black/[0.06] text-[#71717A] text-xs">
                  <span class="text-2xl block mb-2">🌐</span>
                  <span class="font-bold text-[#18181B] block mb-1">当前未配置任何模型渠道</span>
                  <p class="text-[11px] text-[#A1A1AA] mb-3">支持配置 AgentRouter、OpenAI、Claude、DeepSeek 等兼容端点</p>
                  <button @click="s.openAddChannelModal" class="px-3 py-1.5 rounded-lg bg-[#D96B27] text-white text-xs font-semibold shadow-xs hover:bg-[#B8551B] cursor-pointer">➕ 新增渠道</button>
                </div>
                <div
                  v-for="ch in s.channels"
                  :key="ch.id"
                  class="p-3 rounded-xl border border-black/[0.08] bg-[#FAF8F5] flex items-center justify-between shadow-2xs"
                >
                  <div class="flex items-center gap-3">
                    <input type="radio" :checked="ch.primary" @change="s.setPrimaryChannel(ch.id)" class="text-[#D96B27] focus:ring-[#D96B27] cursor-pointer">
                    <div>
                      <div class="flex items-center gap-2">
                        <span class="text-xs font-bold text-[#18181B]">{{ ch.name }}</span>
                        <span
                          :class="[
                            'text-[9px] px-1.5 py-0.2 rounded font-mono font-bold',
                            ch.status === 'online' ? 'bg-emerald-50 text-emerald-700' :
                            ch.status === 'offline' ? 'bg-red-50 text-red-600' :
                            'bg-amber-50 text-amber-700'
                          ]"
                        >
                          {{ ch.status === 'online' ? '在线' : ch.status === 'offline' ? '离线' : '未测速' }}
                        </span>
                        <span class="text-[9px] bg-[#D96B27]/10 text-[#D96B27] px-1.5 py-0.2 rounded font-mono font-bold">{{ ch.protocol || 'openai' }}</span>
                        <span class="text-[9px] bg-black/[0.04] text-[#52525B] px-1.5 py-0.2 rounded font-mono">{{ ch.auth_type === 'refresh_token' ? 'RT / OAuth' : (ch.auth_type === 'azure' ? 'Azure Key' : (ch.auth_type === 'none' ? '免鉴权' : 'API Key')) }}</span>
                      </div>
                      <div class="text-[11px] text-[#71717A] mt-0.5 font-mono">
                        {{ ch.endpoint }} · 延迟: <strong :class="s.pingLoadingMap[ch.id] ? 'text-amber-500 animate-pulse' : 'text-[#10A37F]'">{{ s.pingLoadingMap[ch.id] ? '测速中...' : ch.latency }}</strong>
                      </div>
                    </div>
                  </div>

                  <div class="flex items-center gap-2">
                    <button @click="s.executePing(ch.id)" class="px-2.5 py-1 rounded-lg bg-white border border-black/[0.08] text-xs font-medium hover:bg-black/[0.02] cursor-pointer">⚡ 测速</button>
                    <button @click="s.editChannel(ch)" class="px-2.5 py-1 rounded-lg bg-white border border-black/[0.08] text-xs font-medium hover:bg-black/[0.02] cursor-pointer">✏️ 配置</button>
                    <button @click="s.deleteChannel(ch.id)" class="px-2.5 py-1 rounded-lg bg-white border border-red-200 text-xs font-medium text-red-600 hover:bg-red-50 cursor-pointer">🗑️</button>
                  </div>
                </div>
              </div>
            </div>

            <!-- 选项卡 2: MCP 服务 -->
            <div v-else-if="s.activeSettingsTab === 'mcp'" class="space-y-4">
              <div class="flex items-center justify-between">
                <div>
                  <h3 class="text-xs font-bold text-[#18181B]">Model Context Protocol (MCP) 本地服务</h3>
                  <p class="text-[11px] text-[#71717A]">已读写 ~/.tiancode/mcp_servers.json</p>
                </div>
                <button @click="s.isMcpModalOpen = true" class="px-3 py-1.5 rounded-xl bg-[#D96B27] text-white text-xs font-bold shadow-xs hover:bg-[#B8551B] cursor-pointer">
                  ➕ 导入 MCP 服务
                </button>
              </div>

              <div class="space-y-2">
                <div v-if="s.mcps.length === 0" class="p-8 text-center bg-[#FAF8F5] rounded-xl border border-black/[0.06] text-[#71717A] text-xs">
                  <span class="text-2xl block mb-2">🧩</span>
                  <span class="font-bold text-[#18181B] block mb-1">当前暂无挂载的 MCP 本地服务</span>
                  <p class="text-[11px] text-[#A1A1AA]">可导入并管理基于 Model Context Protocol 的工具算子服务</p>
                </div>
                <div v-for="mcp in s.mcps" :key="mcp.id" class="p-3 rounded-xl border border-black/[0.08] bg-[#FAF8F5] flex flex-col shadow-2xs gap-2">
                  <div class="flex items-center justify-between">
                    <div>
                      <div class="flex items-center gap-2">
                        <span class="text-xs font-bold text-[#18181B]">{{ mcp.name }}</span>
                        <span class="text-[9px] bg-black/[0.04] text-[#52525B] px-1.5 py-0.2 rounded font-mono">{{ mcp.type }}</span>
                      </div>
                      <div class="text-[11px] text-[#71717A] mt-0.5 font-mono">{{ mcp.command }} {{ (mcp.args || []).join(' ') }}</div>
                    </div>
                    <div class="flex items-center gap-2">
                      <button class="px-2 py-1 rounded-lg bg-white border border-black/[0.08] text-[11px] cursor-pointer" @click="s.testMcpAction(mcp.id)">探活</button>
                      <button class="px-2 py-1 rounded-lg bg-white border border-red-200 text-[11px] text-red-600 cursor-pointer" @click="s.deleteMcpAction(mcp.id)">删除</button>
                      <label class="relative inline-flex items-center cursor-pointer">
                        <input type="checkbox" v-model="mcp.enabled" @change="s.toggleMcp(mcp)" class="sr-only peer">
                        <div class="w-9 h-5 bg-gray-200 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-[#10A37F]"></div>
                      </label>
                    </div>
                  </div>
                  <div v-if="mcp.last_error" class="p-2 rounded-lg bg-red-50 text-red-600 text-[10px] font-mono whitespace-pre-wrap border border-red-100">
                    {{ mcp.last_error }}
                  </div>
                </div>
              </div>
            </div>

            <!-- 选项卡 3: Skill 技能库 -->
            <div v-else-if="s.activeSettingsTab === 'skills'" class="space-y-4">
              <div class="flex items-center justify-between">
                <div>
                  <h3 class="text-xs font-bold text-[#18181B]">Agent 技能库 (Skills)</h3>
                  <p class="text-[11px] text-[#71717A]">已读写 ~/.tiancode/skills.json</p>
                </div>
                <div class="flex gap-2">
                  <button @click="s.importSkillFileAction" class="px-3 py-1.5 rounded-xl bg-white border border-black/[0.1] text-[#18181B] text-xs font-semibold hover:bg-black/[0.03] cursor-pointer" title="选择本地 SKILL.md 或 Markdown 文件导入">
                    📥 导入 SKILL.md
                  </button>
                  <button @click="s.isSkillModalOpen = true" class="px-3 py-1.5 rounded-xl bg-[#D96B27] text-white text-xs font-bold shadow-xs hover:bg-[#B8551B] cursor-pointer">
                    ➕ 创建新技能
                  </button>
                </div>
              </div>

              <div class="space-y-2">
                <div class="grid grid-cols-1 gap-2">
                  <div v-for="tpl in s.skillTemplates" :key="tpl.id" class="p-3 rounded-xl border border-black/[0.08] bg-white flex items-center justify-between">
                    <div>
                      <span class="text-xs font-bold text-[#18181B]">{{ tpl.name }}</span>
                      <div class="text-[11px] text-[#71717A] mt-0.5">{{ tpl.description }}</div>
                    </div>
                    <button class="px-2 py-1 rounded-lg bg-[#FAF8F5] border border-black/[0.08] text-[11px] cursor-pointer" @click="s.installSkillTemplateAction(tpl.id)">安装到技能库</button>
                  </div>
                </div>
                <div v-if="s.skills.length === 0" class="p-8 text-center bg-[#FAF8F5] rounded-xl border border-black/[0.06] text-[#71717A] text-xs">
                  <span class="text-2xl block mb-2">🛠️</span>
                  <span class="font-bold text-[#18181B] block mb-1">当前暂无自定义技能</span>
                  <p class="text-[11px] text-[#A1A1AA]">可安装上方模板，或创建自定义技能写入 ~/.tiancode/skills.json</p>
                </div>
                <div v-for="skill in s.skills" :key="skill.id" class="p-3 rounded-xl border border-black/[0.08] bg-[#FAF8F5] flex items-center justify-between shadow-2xs">
                  <div>
                    <span class="text-xs font-bold text-[#18181B]">{{ skill.name }}</span>
                    <div class="text-[11px] text-[#71717A] mt-0.5">{{ skill.description }}</div>
                  </div>
                  <div class="flex items-center gap-2">
                    <label class="relative inline-flex items-center cursor-pointer">
                      <input type="checkbox" v-model="skill.enabled" @change="s.toggleSkill(skill)" class="sr-only peer">
                      <div class="w-9 h-5 bg-gray-200 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-[#10A37F]"></div>
                    </label>
                    <button @click="s.deleteSkillAction(skill.id)" class="p-1 rounded text-red-500 hover:bg-red-50 cursor-pointer text-xs" title="删除技能">🗑️</button>
                  </div>
                </div>
              </div>
            </div>

            <!-- 选项卡 4: 软件规则 -->
            <div v-else-if="s.activeSettingsTab === 'rules'" class="space-y-4">
              <div class="flex items-center justify-between">
                <div>
                  <h3 class="text-xs font-bold text-[#18181B]">软件工程规则与提示词规约</h3>
                  <p class="text-[11px] text-[#71717A]">已读写 ~/.tiancode/rules.json · 自动注入大模型 System Prompt</p>
                </div>
                <div class="flex gap-2">
                  <button @click="s.importWorkspaceRulesAction" class="px-3 py-1.5 rounded-xl border border-black/[0.1] text-xs font-medium hover:bg-black/[0.02] cursor-pointer">
                    从工作区导入 .cursorrules / AGENTS.md / CLAUDE.md
                  </button>
                  <button @click="s.isRuleModalOpen = true" class="px-3 py-1.5 rounded-xl bg-[#D96B27] text-white text-xs font-bold shadow-xs hover:bg-[#B8551B] cursor-pointer">
                    ➕ 添加规则
                  </button>
                </div>
              </div>

              <div class="space-y-2">
                <div v-if="s.rules.length === 0" class="p-8 text-center bg-[#FAF8F5] rounded-xl border border-black/[0.06] text-[#71717A] text-xs">
                  <span class="text-2xl block mb-2">📜</span>
                  <span class="font-bold text-[#18181B] block mb-1">当前暂无自定义工程规则</span>
                  <p class="text-[11px] text-[#A1A1AA]">点击右上角可配置规范守卫，自动在推理时注入智能体 System Prompt</p>
                </div>
                <div v-for="rule in s.rules" :key="rule.id" class="p-3 rounded-xl border border-black/[0.08] bg-[#FAF8F5] space-y-1 shadow-2xs">
                  <div class="flex items-center justify-between">
                    <span class="text-xs font-bold text-[#18181B]">{{ rule.title }}</span>
                    <div class="flex items-center gap-2">
                      <label class="relative inline-flex items-center cursor-pointer">
                        <input type="checkbox" v-model="rule.enabled" @change="s.toggleRule(rule)" class="sr-only peer">
                        <div class="w-9 h-5 bg-gray-200 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-[#10A37F]"></div>
                      </label>
                      <button @click="s.deleteRuleAction(rule.id)" class="p-1 rounded text-red-500 hover:bg-red-50 cursor-pointer text-xs" title="删除规则">🗑️</button>
                    </div>
                  </div>
                  <div class="text-[11px] text-[#52525B] font-mono bg-white p-2 rounded border border-black/[0.04]">{{ rule.content }}</div>
                </div>
              </div>
            </div>

            <div v-else-if="s.activeSettingsTab === 'theme'" class="space-y-4">
              <h3 class="text-sm font-bold text-[#18181B]">🎨 外观主题与 Monaco 编辑器偏好</h3>
              <p class="text-[11px] text-[#71717A]">写入 ~/.tiancode/ui_prefs.json，立即作用于桌面与编辑器</p>
              <div class="grid grid-cols-3 gap-3 text-xs">
                <button
                  v-for="t in [{id:'warm',name:'Warm Neutral',desc:'陶土暖橙与米白'},{id:'dark',name:'Pure Dark',desc:'暗夜高对比'},{id:'system',name:'System Auto',desc:'跟随系统'}]"
                  :key="t.id"
                  class="p-3 rounded-xl text-left cursor-pointer border"
                  :class="s.uiPrefs.theme === t.id ? 'border-2 border-[#D96B27] bg-[#FAF8F5]' : 'border-black/[0.08] bg-white'"
                  @click="s.uiPrefs.theme = t.id; s.persistUIPrefs()"
                >
                  <div class="font-bold">{{ t.name }}</div>
                  <div class="text-[11px] text-[#71717A]">{{ t.desc }}</div>
                </button>
              </div>
              <div class="grid grid-cols-2 gap-4 text-xs">
                <div>
                  <label class="font-semibold">Monaco 代码字体</label>
                  <select v-model="s.uiPrefs.monaco_font" class="w-full h-8 mt-1 px-2 rounded-lg bg-white border border-black/[0.08] font-mono" @change="s.persistUIPrefs()">
                    <option>JetBrains Mono</option>
                    <option>Fira Code</option>
                    <option>Consolas</option>
                  </select>
                </div>
                <div>
                  <label class="font-semibold">编辑器字号</label>
                  <select v-model.number="s.uiPrefs.monaco_size" class="w-full h-8 mt-1 px-2 rounded-lg bg-white border border-black/[0.08] font-mono" @change="s.persistUIPrefs()">
                    <option :value="13">13px</option>
                    <option :value="14">14px</option>
                    <option :value="16">16px</option>
                  </select>
                </div>
              </div>
              <p class="text-[11px] text-[#71717A] font-mono">工作区 {{ s.workspacePath || '尚未打开' }}</p>
            </div>
            <!-- 选项卡 6: 安全隔离机制 (真实防线说明，无假开关) -->
            <div v-else-if="s.activeSettingsTab === 'sandbox'" class="space-y-4">
              <div>
                <h3 class="text-sm font-bold text-[#18181B]">🛡️ 内核级受控安全防线</h3>
                <p class="text-[11px] text-[#71717A] mt-0.5">
                  以下安全防线由 Go 微内核 SafetyRail 与 Sandbox 底层强行拦截，所有大模型工具调用与终端执行物理受限，不设可降级软开关。
                </p>
              </div>

              <div class="p-4 rounded-xl bg-[#FAF8F5] border border-black/[0.08] space-y-4 text-xs">
                <div class="space-y-1">
                  <div class="font-bold text-[#18181B] flex items-center gap-1.5">
                    <span>📁</span><span>工作区路径沙箱硬隔离</span>
                  </div>
                  <div class="text-[11px] text-[#71717A] leading-relaxed">
                    所有文件读写、检索与目录遍历算子强制在当前工作区内执行（当前工作区: <code class="font-mono text-[#18181B] bg-black/[0.04] px-1 py-0.2 rounded">{{ s.workspacePath || '未打开工作区' }}</code>）。内核自动执行路径规范化（Canonicalize）并物理阻断跨盘符与 <code>..</code> 越界访问。
                  </div>
                </div>

                <div class="space-y-1 pt-3 border-t border-black/[0.06]">
                  <div class="font-bold text-[#18181B] flex items-center gap-1.5">
                    <span>⚡</span><span>高危破坏指令物理熔断 (SafetyRail)</span>
                  </div>
                  <div class="text-[11px] text-[#71717A] leading-relaxed">
                    在终端算子与受控执行层，拦截链在执行前物理拦截 <code>rm -rf /</code>、系统关机、格式化磁盘等破坏性指令，杜绝失控脚本破坏本地开发环境。
                  </div>
                </div>

                <div class="space-y-1 pt-3 border-t border-black/[0.06]">
                  <div class="font-bold text-[#18181B] flex items-center gap-1.5">
                    <span>🔑</span><span>网络请求敏感凭据剥离</span>
                  </div>
                  <div class="text-[11px] text-[#71717A] leading-relaxed">
                    向外部 LLM 发送请求前，自动抹除并脱敏包含 <code>sk-</code>、<code>ghp_</code>、<code>password=</code> 的密钥凭据，防止工程内私密凭据意外泄露。
                  </div>
                </div>
              </div>
            </div>

            <!-- 选项卡 7: 关于系统 (保留版本与诊断导出) -->
            <div v-else-if="s.activeSettingsTab === 'about'" class="space-y-4">
              <div>
                <h3 class="text-sm font-bold text-[#18181B]">ℹ️ 关于 湉码 (About Tiancode)</h3>
                <p class="text-[11px] text-[#71717A] mt-0.5">热插拔插件化 AI Coding 工作台</p>
              </div>

              <div class="p-4 rounded-xl bg-[#FAF8F5] border border-black/[0.08] space-y-3 text-xs">
                <div class="font-bold text-sm text-[#18181B]">{{ s.runtimeInfo.product }} v{{ s.runtimeInfo.version }}</div>
                <div class="grid grid-cols-2 gap-2 text-[11px] text-[#71717A] font-mono">
                  <div>操作系统：{{ s.runtimeInfo.os }} {{ s.runtimeInfo.arch }}</div>
                  <div>WebView：{{ s.runtimeInfo.webview }}</div>
                  <div>Go 微内核：{{ s.runtimeInfo.go_version }}</div>
                  <div>用户配置目录：{{ s.runtimeInfo.data_dir }}</div>
                </div>
              </div>

              <div>
                <button
                  class="px-3.5 py-1.5 rounded-lg bg-[#FAF8F5] border border-black/[0.1] text-xs font-medium text-[#18181B] hover:bg-black/[0.04] cursor-pointer shadow-2xs"
                  @click="s.exportDiagnosticsAction"
                >
                  📋 导出系统诊断包 (JSON)
                </button>
              </div>
            </div>

            <!-- 选项卡 8: 实验特性 (收纳非主路径辅助能力) -->
            <div v-else-if="s.activeSettingsTab === 'lab'" class="space-y-4">
              <div>
                <h3 class="text-sm font-bold text-[#18181B]">🧪 实验特性与辅助工具</h3>
                <p class="text-[11px] text-[#71717A] mt-0.5">以下特性处于实验期或仅适用于特定语言，已移出主操作区以保持主路径干净透明。</p>
              </div>

              <div class="p-4 rounded-xl bg-[#FAF8F5] border border-black/[0.08] space-y-4 text-xs">
                <div class="flex items-center justify-between gap-4">
                  <div>
                    <div class="font-bold text-[#18181B]">Go AST 代码架构拓扑</div>
                    <div class="text-[11px] text-[#71717A] mt-0.5">解析工作区内 Go 源码语法树并渲染包调用关系拓扑图（仅限 Go 工程，非通用代码地图）。</div>
                  </div>
                  <button
                    @click="s.openKnowledgeGraphModal"
                    class="px-3 py-1.5 rounded-lg bg-white border border-black/[0.1] text-xs font-medium hover:bg-black/[0.02] cursor-pointer shrink-0 shadow-2xs"
                  >
                    打开 AST 拓扑
                  </button>
                </div>

                <div class="pt-3 border-t border-black/[0.06] flex items-center justify-between gap-4">
                  <div>
                    <div class="font-bold text-[#18181B]">本地 Token 用量估算</div>
                    <div class="text-[11px] text-[#71717A] mt-0.5">基于当前进程字符吞吐的本地粗略估算（进程重启后清零，非服务商真实结算账单）。</div>
                  </div>
                  <div class="text-right font-mono text-[11px] text-[#52525B] shrink-0">
                    <div>调用: {{ s.usageMetrics.total_calls }} 次</div>
                    <div>Token: ~{{ s.usageMetrics.total_tokens }}</div>
                  </div>
                </div>
              </div>
            </div>
          </main>
        </div>
      </div>
    </div>

    <!-- ========================================================================= -->
    <!-- 4. 项目知识图谱模态窗 (Knowledge Graph Modal) -->
    <!-- ========================================================================= -->
    <div
      v-if="s.isKnowledgeGraphOpen"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/45 backdrop-blur-xs animate-in fade-in duration-150 font-sans"
    >
      <div class="w-[92vw] max-w-[1200px] h-[86vh] bg-white rounded-2xl shadow-2xl border border-black/[0.1] flex flex-col overflow-hidden relative">
        <header class="h-12 bg-[#FAF8F5] border-b border-black/[0.08] flex items-center justify-between px-5 select-none shrink-0">
          <div class="flex items-center gap-3">
            <span class="text-base">🕸️</span>
            <span class="font-bold text-sm text-[#18181B]">工作区 Go AST 拓扑</span>
          </div>

          <div class="flex items-center gap-2">
            <button
              @click="s.scanASTGraph"
              class="flex items-center gap-1.5 px-3 py-1.5 rounded-xl bg-[#D96B27] text-white text-xs font-bold shadow-xs hover:bg-[#B8551B] cursor-pointer"
            >
              <span>🔄</span><span>代码扫描与图谱重建</span>
            </button>
            <button @click="s.isKnowledgeGraphOpen = false" class="p-1.5 rounded-lg text-[#71717A] hover:bg-black/[0.05] cursor-pointer">✕</button>
          </div>
        </header>

        <div class="flex-1 flex overflow-hidden">
          <!-- 拓扑节点列表 -->
          <div class="flex-1 p-5 overflow-y-auto bg-[#FAF8F5] space-y-3">
            <div v-if="s.isGraphLoading" class="p-12 text-center text-[#71717A] text-xs flex flex-col items-center justify-center gap-3 mt-12">
              <span class="animate-spin text-3xl">⏳</span>
              <span class="font-bold text-[#18181B] text-sm">正在深度解析工作区 Go AST 语法拓扑树...</span>
              <p class="text-[11px] text-[#A1A1AA]">提取代码包、结构体、接口与依赖实体，请稍候</p>
            </div>
            <div v-else-if="s.astNodes.length === 0" class="p-12 text-center text-[#71717A] text-xs flex flex-col items-center justify-center gap-2 mt-12">
              <span class="text-3xl">🕸️</span>
              <span class="font-bold text-[#18181B]">暂无代码拓扑节点</span>
              <p class="text-[11px] text-[#A1A1AA]">点击右上角【代码扫描与图谱重建】即可扫描当前工作区</p>
            </div>
            <div v-else>
              <div class="text-xs font-bold text-[#71717A] uppercase mb-2">AST 拓扑 ({{ s.astNodes.length }} 节点，图中最多 80)</div>
              <div class="mb-3 overflow-auto rounded-xl border border-black/[0.08] bg-[#18181B] max-h-[48vh]">
                <svg :width="s.astGraph.maxX" :height="s.astGraph.maxY">
                  <line
                    v-for="(e, i) in s.astGraph.edges"
                    :key="'e'+i"
                    :x1="e.x1" :y1="e.y1" :x2="e.x2" :y2="e.y2"
                    stroke="#D96B27" stroke-opacity="0.45"
                  />
                  <g
                    v-for="p in s.astGraph.pos"
                    :key="p.id"
                    @click="s.selectedAstNode = s.astNodes.find(n => n.id === p.id) || s.selectedAstNode"
                    class="cursor-pointer"
                  >
                    <circle :cx="p.x" :cy="p.y" r="10" :fill="s.selectedAstNode?.id === p.id ? '#D96B27' : '#FAF8F5'" />
                    <text :x="p.x + 14" :y="p.y + 4" fill="#F4F4F5" font-size="10">{{ p.name }}</text>
                  </g>
                </svg>
              </div>
              <div class="grid grid-cols-2 gap-3">
                <div
                  v-for="node in s.astNodes"
                  :key="node.id"
                  @click="s.selectedAstNode = node"
                  :class="[
                    'p-3.5 rounded-2xl border bg-white shadow-2xs flex items-center justify-between cursor-pointer transition-all',
                    s.selectedAstNode?.id === node.id ? 'border-2 border-[#D96B27] ring-2 ring-[#D96B27]/20' : 'border-black/[0.08] hover:border-[#D96B27]/40'
                  ]"
                >
                  <div>
                    <div class="flex items-center gap-2">
                      <span class="text-sm">{{ node.type === 'package' ? '📦' : (node.type === 'struct' ? '🏛️' : '📄') }}</span>
                      <span class="text-xs font-bold text-[#18181B] font-mono">{{ node.name }}</span>
                      <span class="text-[9px] bg-[#D96B27]/10 text-[#D96B27] px-1.5 py-0.2 rounded font-mono font-bold">{{ node.type }}</span>
                    </div>
                    <div class="text-[11px] text-[#71717A] mt-1 font-mono">{{ node.file }}</div>
                  </div>
                </div>
              </div>
            </div>
          </div>


          <!-- 实体详情侧板 -->
          <aside class="w-80 border-l border-black/[0.08] bg-white p-5 flex flex-col justify-between overflow-y-auto">
            <div v-if="s.selectedAstNode" class="space-y-4 text-xs">
              <div class="flex items-center gap-2 pb-3 border-b border-black/[0.06]">
                <span class="text-xl">🏛️</span>
                <div>
                  <h4 class="font-bold text-sm text-[#18181B]">{{ s.selectedAstNode.name }}</h4>
                  <span class="text-[10px] text-[#D96B27] bg-[#D96B27]/10 px-1.5 py-0.2 rounded font-mono">{{ s.selectedAstNode.type }}</span>
                </div>
              </div>
              <div>
                <span class="font-bold text-[#71717A]">源文件位置</span>
                <p class="font-mono text-[11px] text-[#18181B] mt-1 bg-[#FAF8F5] p-2 rounded border border-black/[0.04]">{{ s.selectedAstNode.file }}</p>
              </div>
              <div>
                <span class="font-bold text-[#71717A]">拓扑摘要</span>
                <p class="text-[11px] text-[#52525B] leading-relaxed mt-1">{{ s.selectedAstNode.details }}</p>
              </div>
              <div>
                <span class="font-bold text-[#71717A]">架构决策 (写入 ~/.tiancode/adr.json)</span>
                <textarea v-model="s.adrNote" rows="4" class="w-full mt-1 px-2 py-1.5 rounded-lg border border-black/[0.08] text-[11px] font-mono" placeholder="这条约束会随节点引用进对话"></textarea>
                <button class="mt-1 px-2 py-1 rounded-lg bg-white border border-black/[0.08] text-[11px] cursor-pointer" @click="s.saveAdrNote">保存 ADR</button>
              </div>
            </div>

            <button
              v-if="s.selectedAstNode"
              @click="s.injectNodeToPrompt"
              class="w-full py-2 rounded-xl bg-[#D96B27] text-white text-xs font-bold shadow-xs hover:bg-[#B8551B] cursor-pointer flex items-center justify-center gap-1.5 mt-4"
            >
              <span>📌</span><span>引用该节点架构约束至对话</span>
            </button>
          </aside>
        </div>
      </div>
    </div>

    <!-- 渠道编辑弹窗 -->
    <div
      v-if="s.isChannelModalOpen"
      @keydown.esc="s.isChannelModalOpen = false"
      tabindex="-1"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/45 backdrop-blur-xs font-sans"
    >
      <div class="w-full max-w-lg bg-white rounded-2xl shadow-2xl border border-black/[0.1] p-5 space-y-4 max-h-[90vh] overflow-y-auto">
        <div class="flex items-center justify-between pb-2 border-b border-black/[0.06]">
          <h4 class="text-sm font-bold text-[#18181B] flex items-center gap-1.5">
            <span>🌐</span><span>模型渠道与网关接入配置</span>
          </h4>
          <button @click="s.isChannelModalOpen = false" class="text-[#71717A] hover:text-[#18181B] p-1 rounded-md cursor-pointer" title="关闭弹窗 (Esc)">✕</button>
        </div>
        <div class="space-y-3 text-xs">
          <div>
            <label class="block font-medium text-[#71717A] mb-1">渠道名称</label>
            <input v-model="s.channelForm.name" placeholder="如 Claude-3.5-Sonnet 或 本地Qwen" type="text" class="w-full px-2.5 py-1.5 rounded-lg border border-black/[0.1] focus:outline-none focus:border-[#D96B27]">
          </div>

          <div class="grid grid-cols-2 gap-3">
            <div>
              <label class="block font-medium text-[#71717A] mb-1">接入协议 (Protocol)</label>
              <select
                v-model="s.channelForm.protocol"
                @change="() => {
                  if (s.channelForm.protocol === 'ollama') s.channelForm.auth_type = 'none'
                  else if (s.channelForm.protocol === 'azure') s.channelForm.auth_type = 'azure'
                  else if (s.channelForm.auth_type === 'none' || s.channelForm.auth_type === 'azure') s.channelForm.auth_type = 'api_key'
                  if (s.channelForm.protocol === 'grok' && !s.channelForm.extra_models) s.channelForm.extra_models = 'grok-4.6'
                }"
                class="w-full px-2.5 py-1.5 rounded-lg border border-black/[0.1] focus:outline-none focus:border-[#D96B27]"
              >
                <option value="openai">OpenAI (含兼容模型/中转)</option>
                <option value="anthropic">Anthropic Claude (原生)</option>
                <option value="gemini">Google Gemini (原生兼容)</option>
                <option value="grok">xAI Grok (4.6 / Grok 系列)</option>
                <option value="azure">Azure OpenAI (微软云)</option>
                <option value="ollama">Ollama (本地私有化)</option>
              </select>
            </div>
            <div>
              <label class="block font-medium text-[#71717A] mb-1">认证模式 (Auth Type)</label>
              <select v-model="s.channelForm.auth_type" class="w-full px-2.5 py-1.5 rounded-lg border border-black/[0.1] focus:outline-none focus:border-[#D96B27]">
                <option value="api_key">API Key (静态密钥 / 轮询)</option>
                <option value="refresh_token">Refresh Token (RT / OAuth 保活)</option>
                <option value="azure" v-if="s.channelForm.protocol === 'azure'">Azure 专有密钥</option>
                <option value="none">免鉴权 / 本地直连</option>
              </select>
            </div>
          </div>

          <div>
            <label class="block font-medium text-[#71717A] mb-1">API Base URL</label>
            <input
              v-model="s.channelForm.endpoint"
              type="text"
              :placeholder="
                s.channelForm.protocol === 'azure' ? 'https://your-resource.openai.azure.com' :
                s.channelForm.protocol === 'anthropic' ? '默认: https://api.anthropic.com' :
                s.channelForm.protocol === 'gemini' ? '默认: https://generativelanguage.googleapis.com/v1beta/openai' :
                s.channelForm.protocol === 'grok' ? '默认: https://api.x.ai/v1 (或中转站如 https://ss2a.top/v1)' :
                s.channelForm.protocol === 'ollama' ? '默认: http://localhost:11434' : 'https://api.openai.com/v1'
              "
              class="w-full px-2.5 py-1.5 rounded-lg border border-black/[0.1] focus:outline-none focus:border-[#D96B27] font-mono text-[11px]"
            >
          </div>

          <!-- 动态鉴权字段 1: 普通 API Key 模式 -->
          <div v-if="s.channelForm.auth_type === 'api_key'">
            <div class="flex items-center justify-between mb-1">
              <label class="font-medium text-[#71717A]">API Key / 凭据 (支持多 Key 轮询或直粘凭据 JSON)</label>
              <span class="text-[10px] text-[#A1A1AA]">单 Key 直接粘贴，多 Key 换行</span>
            </div>
            <textarea
              v-model="s.channelForm.api_key"
              rows="2"
              :placeholder="
                s.channelForm.protocol === 'anthropic' ? 'sk-ant-api03-...' :
                s.channelForm.protocol === 'gemini' ? 'AIzaSy... (Google API Key) 或粘贴完整凭据 JSON' :
                s.channelForm.protocol === 'grok' ? 'xai-... 或中转站 Key 如 sk-...' : 'sk-... 或粘贴整段凭据 JSON'
              "
              class="w-full px-2.5 py-1.5 rounded-lg border border-black/[0.1] focus:outline-none focus:border-[#D96B27] font-mono text-[11px] resize-none"
            ></textarea>
            <div class="text-[10px] text-[#A1A1AA] mt-1">
              💡 对齐 new-api：可直接粘贴 Google ADC JSON、GCP Service Account JSON 或 OpenAI Codex JSON，系统自动提取鉴权。
            </div>
          </div>

          <!-- 动态鉴权字段 2: Refresh Token (RT) / OAuth 模式 -->
          <div v-else-if="s.channelForm.auth_type === 'refresh_token'" class="space-y-2 bg-[#FAF8F5] p-3 rounded-xl border border-black/[0.06]">
            <div>
              <div class="flex items-center justify-between mb-1">
                <label class="font-bold text-[#18181B]">Refresh Token (RT) / 凭据 JSON</label>
                <span class="text-[10px] text-[#D96B27] font-semibold">自动刷新并缓存 Access Token</span>
              </div>
              <textarea
                v-model="s.channelForm.api_key"
                rows="2"
                placeholder="直接粘贴 Refresh Token (如 rt_...) 或 Google / OpenAI OAuth 完整凭据 JSON"
                class="w-full px-2.5 py-1.5 rounded-lg border border-black/[0.1] bg-white focus:outline-none focus:border-[#D96B27] font-mono text-[11px] resize-none"
              ></textarea>
            </div>

            <!-- 可选折叠高级参数 (默认隐藏，与 new-api 体验对齐，无需手动填写) -->
            <details class="text-[11px] text-[#71717A] pt-1">
              <summary class="cursor-pointer font-medium hover:text-[#D96B27] select-none flex items-center gap-1">
                <span>⚙️ 高级 OAuth 覆盖参数 (可选，默认自动识别)</span>
              </summary>
              <div class="grid grid-cols-3 gap-2 mt-2 pt-2 border-t border-black/[0.06]">
                <div>
                  <label class="block text-[10px] text-[#71717A] mb-0.5">Token 端点</label>
                  <input
                    v-model="s.channelForm.token_endpoint"
                    placeholder="自动识别"
                    type="text"
                    class="w-full px-2 py-1 rounded-lg border border-black/[0.1] bg-white font-mono text-[10px]"
                  >
                </div>
                <div>
                  <label class="block text-[10px] text-[#71717A] mb-0.5">Client ID</label>
                  <input
                    v-model="s.channelForm.client_id"
                    placeholder="选填"
                    type="text"
                    class="w-full px-2 py-1 rounded-lg border border-black/[0.1] bg-white font-mono text-[10px]"
                  >
                </div>
                <div>
                  <label class="block text-[10px] text-[#71717A] mb-0.5">Client Secret</label>
                  <input
                    v-model="s.channelForm.client_secret"
                    placeholder="选填"
                    type="password"
                    class="w-full px-2 py-1 rounded-lg border border-black/[0.1] bg-white font-mono text-[10px]"
                  >
                </div>
              </div>
            </details>

            <div class="text-[10px] text-[#71717A] bg-black/[0.02] p-1.5 rounded-lg border border-black/[0.04]">
              💡 <strong>极速模式</strong>：直接将 Google 凭据 JSON (如 <code>application_default_credentials.json</code>) 完整粘贴于上方主输入框，全自动解析，免填其他项。
            </div>
          </div>

          <!-- 动态鉴权字段 3: Azure 模式 -->
          <div v-else-if="s.channelForm.auth_type === 'azure'" class="space-y-2 bg-[#FAF8F5] p-3 rounded-xl border border-black/[0.06]">
            <div>
              <label class="block font-medium text-[#71717A] mb-1">Azure API Key</label>
              <input
                v-model="s.channelForm.api_key"
                type="password"
                placeholder="Azure 门户中的 32 位 Key"
                class="w-full px-2.5 py-1.5 rounded-lg border border-black/[0.1] bg-white focus:outline-none focus:border-[#D96B27] font-mono text-[11px]"
              >
            </div>
            <div>
              <label class="block text-[11px] text-[#71717A] mb-0.5">API Version (版本号)</label>
              <input
                v-model="s.channelForm.api_version"
                type="text"
                placeholder="例如: 2024-02-15-preview"
                class="w-full px-2.5 py-1.5 rounded-lg border border-black/[0.1] bg-white font-mono text-[11px]"
              >
            </div>
          </div>

          <!-- 动态鉴权字段 4: 免鉴权模式 -->
          <div v-else-if="s.channelForm.auth_type === 'none'" class="p-2.5 rounded-lg bg-emerald-50 border border-emerald-200 text-emerald-800 text-[11px] flex items-center gap-2">
            <span>🟢</span>
            <span>已启用免鉴权模式，请求将直接透传至 Base URL，无需验证 API Key。</span>
          </div>

          <div>
            <label class="block font-medium text-[#71717A] mb-1">额外模型标签（逗号分隔，写入渠道 extra_models）</label>
            <input v-model="s.channelForm.extra_models" type="text" class="w-full px-2.5 py-1.5 rounded-lg border border-black/[0.1] focus:outline-none focus:border-[#D96B27]" placeholder="deepseek-v4-flash, glm-5.3">
          </div>

          <button @click="s.fetchModelsAction" class="w-full py-1.5 rounded-lg border border-[#D96B27] text-[#D96B27] text-xs font-bold hover:bg-[#D96B27]/10 cursor-pointer">
            🔄 真实自动获取上游模型 (/v1/models)
          </button>
        </div>

        <div class="flex justify-end gap-2 pt-2 border-t border-black/[0.06]">
          <button @click="s.isChannelModalOpen = false" class="px-3 py-1 rounded-lg border border-black/[0.1] text-xs cursor-pointer">取消</button>
          <button @click="s.saveChannelAction" class="px-4 py-1 rounded-lg bg-[#D96B27] text-white text-xs font-semibold hover:bg-[#B8551B] cursor-pointer">保存至磁盘</button>
        </div>
      </div>
    </div>

    <!-- MCP 导入配置弹窗 -->
    <div
      v-if="s.isMcpModalOpen"
      @keydown.esc="s.isMcpModalOpen = false"
      tabindex="-1"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/45 backdrop-blur-xs font-sans"
    >
      <div class="w-full max-w-md bg-white rounded-2xl shadow-2xl border border-black/[0.1] p-5 space-y-4">
        <div class="flex items-center justify-between pb-2 border-b border-black/[0.06]">
          <h4 class="text-sm font-bold text-[#18181B] flex items-center gap-1.5">
            <span>🧩</span><span>导入 MCP 服务配置</span>
          </h4>
          <button @click="s.isMcpModalOpen = false" class="text-[#71717A] hover:text-[#18181B] p-1 rounded-md cursor-pointer" title="关闭弹窗 (Esc)">✕</button>
        </div>
        <div class="space-y-3 text-xs">
          <div>
            <label class="block font-medium text-[#71717A] mb-1">服务名称</label>
            <input v-model="s.mcpForm.name" placeholder="如 fetch-mcp 或 filesystem" type="text" class="w-full px-2.5 py-1.5 rounded-lg border border-black/[0.1] focus:outline-none focus:border-[#D96B27]">
          </div>
          <div>
            <label class="block font-medium text-[#71717A] mb-1">通信类型</label>
            <select v-model="s.mcpForm.type" class="w-full px-2.5 py-1.5 rounded-lg border border-black/[0.1] focus:outline-none focus:border-[#D96B27]">
              <option value="stdio">stdio（当前内核已实现）</option>
            </select>
          </div>
          <div>
            <label class="block font-medium text-[#71717A] mb-1">启动命令 (Command)</label>
            <input v-model="s.mcpForm.command" placeholder="如 npx 或 python" type="text" class="w-full px-2.5 py-1.5 rounded-lg border border-black/[0.1] focus:outline-none focus:border-[#D96B27]">
          </div>
          <div>
            <label class="block font-medium text-[#71717A] mb-1">启动参数 (以空格隔开)</label>
            <input v-model="s.mcpArgsInput" placeholder="-y @modelcontextprotocol/server-filesystem D:/workspace" type="text" class="w-full px-2.5 py-1.5 rounded-lg border border-black/[0.1] focus:outline-none focus:border-[#D96B27]">
          </div>
          <div>
            <label class="block font-medium text-[#71717A] mb-1">环境变量 (每行 KEY=VALUE)</label>
            <textarea v-model="s.mcpEnvInput" placeholder="API_KEY=xxx\nDB_PASS=yyy" rows="3" class="w-full px-2.5 py-1.5 rounded-lg border border-black/[0.1] focus:outline-none focus:border-[#D96B27] resize-none"></textarea>
          </div>
        </div>
        <div class="flex justify-end gap-2 pt-2 border-t border-black/[0.06]">
          <button @click="s.isMcpModalOpen = false" class="px-3 py-1 rounded-lg border border-black/[0.1] text-xs cursor-pointer">取消</button>
          <button @click="s.saveMcpAction" class="px-4 py-1 rounded-lg bg-[#D96B27] text-white text-xs font-semibold hover:bg-[#B8551B] cursor-pointer">保存 MCP 服务</button>
        </div>
      </div>
    </div>

    <!-- Skill 新增弹窗 -->
    <div
      v-if="s.isSkillModalOpen"
      @keydown.esc="s.isSkillModalOpen = false"
      tabindex="-1"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/45 backdrop-blur-xs font-sans"
    >
      <div class="w-full max-w-md bg-white rounded-2xl shadow-2xl border border-black/[0.1] p-5 space-y-4">
        <div class="flex items-center justify-between pb-2 border-b border-black/[0.06]">
          <h4 class="text-sm font-bold text-[#18181B] flex items-center gap-1.5">
            <span>🛠️</span><span>创建 Agent 技能 (Skill)</span>
          </h4>
          <button @click="s.isSkillModalOpen = false" class="text-[#71717A] hover:text-[#18181B] p-1 rounded-md cursor-pointer" title="关闭弹窗 (Esc)">✕</button>
        </div>
        <div class="space-y-3 text-xs">
          <div>
            <label class="block font-medium text-[#71717A] mb-1">技能名称</label>
            <input v-model="s.skillForm.name" placeholder="如 vue3-expert 或 rust-analyzer" type="text" class="w-full px-2.5 py-1.5 rounded-lg border border-black/[0.1] focus:outline-none focus:border-[#D96B27]">
          </div>
          <div>
            <label class="block font-medium text-[#71717A] mb-1">描述与职责说明</label>
            <input v-model="s.skillForm.description" placeholder="专有技术栈模式、规约与实现导向" type="text" class="w-full px-2.5 py-1.5 rounded-lg border border-black/[0.1] focus:outline-none focus:border-[#D96B27]">
          </div>
          <div>
            <label class="block font-medium text-[#71717A] mb-1">提示词与技能正文</label>
            <textarea v-model="s.skillForm.content" rows="4" placeholder="在此输入注入大模型系统指令的专业技能提示词..." class="w-full px-2.5 py-1.5 rounded-lg border border-black/[0.1] focus:outline-none focus:border-[#D96B27] resize-none"></textarea>
          </div>
        </div>
        <div class="flex justify-end gap-2 pt-2 border-t border-black/[0.06]">
          <button @click="s.isSkillModalOpen = false" class="px-3 py-1 rounded-lg border border-black/[0.1] text-xs cursor-pointer">取消</button>
          <button @click="s.saveSkillAction" class="px-4 py-1 rounded-lg bg-[#D96B27] text-white text-xs font-semibold hover:bg-[#B8551B] cursor-pointer">保存技能</button>
        </div>
      </div>
    </div>

    <!-- Rule 新增弹窗 -->
    <div
      v-if="s.isRuleModalOpen"
      @keydown.esc="s.isRuleModalOpen = false"
      tabindex="-1"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/45 backdrop-blur-xs font-sans"
    >
      <div class="w-full max-w-md bg-white rounded-2xl shadow-2xl border border-black/[0.1] p-5 space-y-4">
        <div class="flex items-center justify-between pb-2 border-b border-black/[0.06]">
          <h4 class="text-sm font-bold text-[#18181B] flex items-center gap-1.5">
            <span>📜</span><span>添加工程规约与规则 (Rule)</span>
          </h4>
          <button @click="s.isRuleModalOpen = false" class="text-[#71717A] hover:text-[#18181B] p-1 rounded-md cursor-pointer" title="关闭弹窗 (Esc)">✕</button>
        </div>
        <div class="space-y-3 text-xs">
          <div>
            <label class="block font-medium text-[#71717A] mb-1">规则名称</label>
            <input v-model="s.ruleForm.title" placeholder="如 铁律 0.5 严禁假数据" type="text" class="w-full px-2.5 py-1.5 rounded-lg border border-black/[0.1] focus:outline-none focus:border-[#D96B27]">
          </div>
          <div>
            <label class="block font-medium text-[#71717A] mb-1">规则内容</label>
            <textarea v-model="s.ruleForm.content" rows="4" placeholder="在此输入强制约束与守卫提示词..." class="w-full px-2.5 py-1.5 rounded-lg border border-black/[0.1] focus:outline-none focus:border-[#D96B27] resize-none"></textarea>
          </div>
        </div>
        <div class="flex justify-end gap-2 pt-2 border-t border-black/[0.06]">
          <button @click="s.isRuleModalOpen = false" class="px-3 py-1 rounded-lg border border-black/[0.1] text-xs cursor-pointer">取消</button>
          <button @click="s.saveRuleAction" class="px-4 py-1 rounded-lg bg-[#D96B27] text-white text-xs font-semibold hover:bg-[#B8551B] cursor-pointer">保存规则</button>
        </div>
      </div>
    </div>

    <div
      v-if="s.isCommandPaletteOpen"
      class="fixed inset-0 z-[60] flex items-start justify-center bg-black/40 pt-[12vh]"
      @click.self="s.isCommandPaletteOpen = false"
    >
      <div class="w-[min(640px,90vw)] bg-white rounded-2xl shadow-2xl border border-black/[0.1] overflow-hidden">
        <input
          v-model="s.commandPaletteQuery"
          type="text"
          autofocus
          placeholder="跳转：设置 / 会话 / 已打开文件…"
          class="w-full px-4 py-3 text-sm border-b border-black/[0.08] focus:outline-none"
          @keydown.down.prevent="s.moveCommandPalette(1)"
          @keydown.up.prevent="s.moveCommandPalette(-1)"
          @keydown.enter.prevent="s.confirmCommandPalette()"
        />
        <div class="max-h-[50vh] overflow-y-auto py-1">
          <div v-if="s.commandPaletteItems.length === 0" class="px-4 py-6 text-xs text-[#71717A]">没有匹配项</div>
          <button
            v-for="(item, idx) in s.commandPaletteItems"
            :key="item.id"
            class="w-full text-left px-4 py-2 text-xs flex items-center justify-between cursor-pointer"
            :class="idx === s.commandPaletteIndex ? 'bg-[#D96B27]/10 text-[#18181B]' : 'hover:bg-black/[0.03]'"
            @click="s.runCommandPaletteItem(item)"
          >
            <span class="font-medium truncate">{{ item.label }}</span>
            <span class="text-[10px] text-[#A1A1AA] ml-3 shrink-0">{{ item.kind }}{{ item.hint ? ' · ' + item.hint : '' }}</span>
          </button>
        </div>
      </div>
    </div>

    <div
      v-if="s.isStrategyPickerOpen"
      class="fixed inset-0 z-[65] flex items-center justify-center bg-black/45 backdrop-blur-xs font-sans"
      @keydown.esc="s.closeStrategyPicker"
      @click.self="s.closeStrategyPicker"
      tabindex="-1"
    >
      <div class="w-[min(720px,92vw)] bg-white rounded-2xl border border-black/[0.1] shadow-2xl p-4 space-y-3">
        <div class="flex items-center justify-between">
          <div>
            <h4 class="text-sm font-bold text-[#18181B]">架构执行策略确认</h4>
            <p class="text-[11px] text-[#71717A]">选择会改内核工具权限：只读拦写盘；TDD 写后跑测试；直接改代码会写磁盘。</p>
          </div>
          <div class="flex items-center gap-2">
            <span class="text-[9px] text-[#D96B27] bg-[#D96B27]/10 px-1.5 py-0.5 rounded font-mono font-bold">待用户决策</span>
            <button @click="s.closeStrategyPicker" class="p-1 rounded-md text-[#71717A] hover:bg-black/[0.05] cursor-pointer" title="关闭 (Esc)">✕</button>
          </div>
        </div>
        <div class="grid grid-cols-1 md:grid-cols-3 gap-2.5">
          <button
            v-for="opt in s.executionStrategies"
            :key="opt.id"
            type="button"
            class="text-left p-3 rounded-xl cursor-pointer transition-all"
            :class="s.selectedStrategyDraft === opt.id ? 'border-2 border-[#D96B27] bg-white shadow-xs ring-2 ring-[#D96B27]/15' : 'border border-black/[0.08] opacity-85 hover:opacity-100'"
            @click="s.selectedStrategyDraft = opt.id"
          >
            <div class="flex items-center justify-between gap-1">
              <div class="flex items-center gap-1.5 font-bold text-xs text-[#18181B]">
                <span class="w-3.5 h-3.5 rounded-full bg-[#D96B27] text-white flex items-center justify-center text-[9px]">{{ opt.letter }}</span>
                <span>{{ opt.title }}</span>
              </div>
              <span class="text-[9px] font-mono font-bold text-[#10A37F] bg-emerald-50 px-1.5 py-0.2 rounded">{{ opt.badge }}</span>
            </div>
            <p class="text-[11px] text-[#71717A] mt-1.5 leading-relaxed">{{ opt.desc }}</p>
          </button>
        </div>
        <input
          v-model="s.strategyNote"
          type="text"
          placeholder="补充约束（可选，会写入系统提示并随策略一起生效）"
          class="w-full h-8 px-2.5 rounded-lg border border-black/[0.08] text-xs"
        />
        <div class="flex justify-end gap-2 pt-1 border-t border-black/[0.06]">
          <button class="px-3 py-1 rounded-lg text-xs text-[#71717A] hover:bg-black/[0.04] cursor-pointer" @click="s.skipStrategyChoice">保持默认只读审查 (analyze)</button>
          <button class="px-4 py-1 rounded-lg bg-[#D96B27] text-white text-xs font-semibold cursor-pointer shadow-2xs hover:bg-[#B8551B]" @click="s.confirmStrategyAndSend">确定提交选择</button>
        </div>
      </div>
    </div>

    <div
      v-if="s.tabContextMenu"
      class="fixed z-[70] bg-white border border-black/[0.1] rounded-lg shadow-lg text-xs py-1 min-w-[140px]"
      :style="{ left: s.tabContextMenu.x + 'px', top: s.tabContextMenu.y + 'px' }"
      @click.self="s.tabContextMenu = null"
    >
      <button class="w-full text-left px-3 py-1.5 hover:bg-black/[0.04] cursor-pointer" @click="s.closeSessionTab(s.tabContextMenu.id)">关闭</button>
      <button class="w-full text-left px-3 py-1.5 hover:bg-black/[0.04] cursor-pointer" @click="s.closeOtherTabs(s.tabContextMenu.id)">关闭其他</button>
      <button class="w-full text-left px-3 py-1.5 hover:bg-black/[0.04] cursor-pointer" @click="s.closeAllTabs()">关闭全部</button>
    </div>

    <!-- 全局 Toast 提示 -->
    <div
      v-if="s.toastMessage"
      class="fixed bottom-5 right-5 z-50 px-3.5 py-2 rounded-xl bg-[#18181B] text-white text-xs font-medium shadow-2xl flex items-center gap-2 border border-white/[0.1] animate-in slide-in-from-bottom-3 duration-200"
    >
      <span>{{ s.toastMessage }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'
import { wailsBridge } from './core/wailsBridge'
import { useWorkbenchStore } from './stores/workbench'
import ActivityBar from './components/ActivityBar.vue'
import LeftDrawer from './components/LeftDrawer.vue'
import ChatCockpit from './components/ChatCockpit.vue'
import DiffWorkspace from './components/DiffWorkspace.vue'
import TerminalDrawer from './components/TerminalDrawer.vue'

const s = useWorkbenchStore()
let stop: (() => void) | undefined
onMounted(() => { stop = s.initWorkbench() })
onUnmounted(() => { stop?.() })
</script>

<style>
button {
  transition: transform 0.08s ease, background-color 0.15s ease, opacity 0.15s ease;
}
button:active {
  transform: scale(0.96);
}

.markdown-body pre {
  background-color: #18181B;
  color: #F4F4F5;
  padding: 0.75rem;
  border-radius: 0.5rem;
  overflow-x: auto;
  font-family: 'Fira Code', monospace;
  margin: 0.5rem 0;
  border: 1px solid rgba(255, 255, 255, 0.1);
}
.markdown-body code {
  font-family: 'Fira Code', monospace;
  background-color: rgba(0, 0, 0, 0.05);
  padding: 0.1rem 0.3rem;
  border-radius: 0.25rem;
}
.markdown-body pre code {
  background-color: transparent;
  padding: 0;
}
.markdown-body p {
  margin-bottom: 0.5rem;
}
.markdown-body ul, .markdown-body ol {
  padding-left: 1.25rem;
  margin-bottom: 0.5rem;
}
.markdown-body ul {
  list-style-type: disc;
}
.markdown-body ol {
  list-style-type: decimal;
}
html[data-theme='dark'] .tian-root {
  background: #18181B !important;
  color: #FAFAFA !important;
}
html[data-theme='dark'] .tian-root header,
html[data-theme='dark'] .tian-root .bg-\[\#FAF8F5\] {
  background: #27272A !important;
}
</style>

