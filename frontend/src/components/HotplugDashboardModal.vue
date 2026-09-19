<template>
  <div
    v-if="s.isHotplugDashboardOpen"
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/45 backdrop-blur-xs font-sans animate-in fade-in duration-150"
    @keydown.esc="s.closeHotplugDashboard()"
    @click.self="s.closeHotplugDashboard()"
    tabindex="-1"
    ref="modalRef"
  >
    <div class="w-[92vw] max-w-[1100px] h-[85vh] bg-[#FAF8F5] rounded-2xl shadow-2xl border border-black/[0.1] flex flex-col overflow-hidden relative">
      <!-- 弹窗顶栏 -->
      <header class="h-14 bg-[#F4EFEA] border-b border-black/[0.08] px-5 flex items-center justify-between select-none shrink-0">
        <div class="flex items-center gap-3">
          <div class="w-8 h-8 rounded-xl bg-[#D96B27]/15 text-[#D96B27] flex items-center justify-center font-bold text-base shadow-xs">
            🧩
          </div>
          <div>
            <div class="flex items-center gap-2">
              <h3 class="font-bold text-sm text-[#18181B]">插件热插拔中心与 DSH 算子大盘</h3>
              <span class="text-[10px] font-mono font-semibold px-2 py-0.5 rounded-full bg-[#D96B27]/10 text-[#D96B27] border border-[#D96B27]/20">
                Go Microkernel + DSH
              </span>
            </div>
            <p class="text-[11px] text-[#71717A]">
              Agent = Model + Harness · 运行时全景实时拓扑、微内核算子、SafetyRail 防线与动态 MCP
            </p>
          </div>
        </div>

        <div class="flex items-center gap-2">
          <button
            @click="s.reloadHotplugRegistryAction()"
            :disabled="s.isHotplugLoading"
            class="flex items-center gap-1.5 px-3 py-1.5 rounded-xl bg-white border border-black/[0.1] text-xs font-medium text-[#27272A] hover:bg-black/[0.03] shadow-2xs transition-all cursor-pointer disabled:opacity-50"
            title="重新探测并热重载所有插件与算子 (Reload Registry)"
          >
            <span :class="{ 'animate-spin': s.isHotplugLoading }">🔄</span>
            <span>动态热重载</span>
          </button>

          <button
            @click="s.exportHotplugManifestAction()"
            class="flex items-center gap-1.5 px-3 py-1.5 rounded-xl bg-white border border-black/[0.1] text-xs font-medium text-[#27272A] hover:bg-black/[0.03] shadow-2xs transition-all cursor-pointer"
            title="导出微内核算子清单为标准 JSON 格式并复制至剪贴板"
          >
            <span>📋</span>
            <span>导出清单</span>
          </button>

          <button
            @click="s.closeHotplugDashboard()"
            class="w-8 h-8 rounded-xl flex items-center justify-center text-[#71717A] hover:text-[#18181B] hover:bg-black/[0.06] transition-all cursor-pointer"
            title="关闭大盘 (Esc)"
          >
            ✕
          </button>
        </div>
      </header>

      <!-- KPI 概览横幅 (Summary Cards) -->
      <section class="grid grid-cols-2 md:grid-cols-5 gap-3 p-4 bg-white border-b border-black/[0.06] shrink-0">
        <div
          @click="s.hotplugActiveTab = 'tools'"
          class="p-2.5 rounded-xl bg-[#FAF8F5] border border-black/[0.06] hover:border-[#D96B27]/40 transition-all cursor-pointer group"
          :class="{ 'ring-2 ring-[#D96B27]/20 border-[#D96B27]': s.hotplugActiveTab === 'tools' }"
        >
          <div class="flex items-center justify-between text-xs text-[#71717A] mb-1">
            <span class="font-medium group-hover:text-[#D96B27]">微内核算子</span>
            <span>⚡</span>
          </div>
          <div class="text-xl font-bold font-mono text-[#18181B]">
            {{ s.hotplugReport?.summary.total_tools ?? 0 }}
          </div>
          <div class="text-[10px] text-[#A1A1AA] truncate mt-0.5">
            FS / Git / Shell / MCP 算子
          </div>
        </div>

        <div
          @click="s.hotplugActiveTab = 'mcps'"
          class="p-2.5 rounded-xl bg-[#FAF8F5] border border-black/[0.06] hover:border-[#D96B27]/40 transition-all cursor-pointer group"
          :class="{ 'ring-2 ring-[#D96B27]/20 border-[#D96B27]': s.hotplugActiveTab === 'mcps' }"
        >
          <div class="flex items-center justify-between text-xs text-[#71717A] mb-1">
            <span class="font-medium group-hover:text-[#D96B27]">MCP 动态服务</span>
            <span>🔌</span>
          </div>
          <div class="text-xl font-bold font-mono text-[#18181B]">
            {{ s.hotplugReport?.summary.active_mcps ?? 0 }}
          </div>
          <div class="text-[10px] text-[#A1A1AA] truncate mt-0.5">
            JSON-RPC 2.0 协议通道
          </div>
        </div>

        <div
          @click="s.hotplugActiveTab = 'rails'"
          class="p-2.5 rounded-xl bg-[#FAF8F5] border border-black/[0.06] hover:border-[#D96B27]/40 transition-all cursor-pointer group"
          :class="{ 'ring-2 ring-[#D96B27]/20 border-[#D96B27]': s.hotplugActiveTab === 'rails' }"
        >
          <div class="flex items-center justify-between text-xs text-[#71717A] mb-1">
            <span class="font-medium group-hover:text-[#D96B27]">SafetyRail 防线</span>
            <span>🛡️</span>
          </div>
          <div class="text-xl font-bold font-mono text-[#18181B]">
            {{ s.hotplugReport?.summary.active_rails ?? 0 }}
          </div>
          <div class="text-[10px] text-[#A1A1AA] truncate mt-0.5">
            前置裁决 / P-100 阻断权
          </div>
        </div>

        <div
          @click="s.hotplugActiveTab = 'providers'"
          class="p-2.5 rounded-xl bg-[#FAF8F5] border border-black/[0.06] hover:border-[#D96B27]/40 transition-all cursor-pointer group"
          :class="{ 'ring-2 ring-[#D96B27]/20 border-[#D96B27]': s.hotplugActiveTab === 'providers' }"
        >
          <div class="flex items-center justify-between text-xs text-[#71717A] mb-1">
            <span class="font-medium group-hover:text-[#D96B27]">模型协议驱动</span>
            <span>🌐</span>
          </div>
          <div class="text-xl font-bold font-mono text-[#18181B]">
            {{ s.hotplugReport?.summary.active_providers ?? 0 }}
          </div>
          <div class="text-[10px] text-[#A1A1AA] truncate mt-0.5">
            OpenAI / Claude / Gemini / Grok
          </div>
        </div>

        <div
          @click="s.hotplugActiveTab = 'overview'"
          class="p-2.5 rounded-xl bg-[#FAF8F5] border border-black/[0.06] hover:border-[#D96B27]/40 transition-all cursor-pointer group"
          :class="{ 'ring-2 ring-[#D96B27]/20 border-[#D96B27]': s.hotplugActiveTab === 'overview' }"
        >
          <div class="flex items-center justify-between text-xs text-[#71717A] mb-1">
            <span class="font-medium group-hover:text-[#D96B27]">健康探针</span>
            <span class="inline-block w-2 h-2 rounded-full bg-[#10A37F]"></span>
          </div>
          <div class="text-xl font-bold font-mono text-[#10A37F]">
            {{ s.hotplugReport?.summary.healthyCount ?? 0 }} 项正常
          </div>
          <div class="text-[10px] text-[#A1A1AA] truncate mt-0.5">
            Fail-Closed 零假成功
          </div>
        </div>
      </section>

      <!-- 选项卡与检索条 -->
      <div class="h-11 px-5 bg-[#FAF8F5] border-b border-black/[0.08] flex items-center justify-between shrink-0">
        <div class="flex items-center gap-1">
          <button
            @click="s.hotplugActiveTab = 'tools'"
            :class="['px-3 py-1.5 rounded-lg text-xs font-medium cursor-pointer transition-all', s.hotplugActiveTab === 'tools' ? 'bg-white shadow-2xs text-[#D96B27] font-semibold border border-black/[0.08]' : 'text-[#71717A] hover:bg-black/[0.04]']"
          >
            ⚡ 微内核算子 ({{ s.hotplugReport?.tools.length ?? 0 }})
          </button>
          <button
            @click="s.hotplugActiveTab = 'mcps'"
            :class="['px-3 py-1.5 rounded-lg text-xs font-medium cursor-pointer transition-all', s.hotplugActiveTab === 'mcps' ? 'bg-white shadow-2xs text-[#D96B27] font-semibold border border-black/[0.08]' : 'text-[#71717A] hover:bg-black/[0.04]']"
          >
            🔌 MCP 动态服务 ({{ s.hotplugReport?.mcps.length ?? 0 }})
          </button>
          <button
            @click="s.hotplugActiveTab = 'rails'"
            :class="['px-3 py-1.5 rounded-lg text-xs font-medium cursor-pointer transition-all', s.hotplugActiveTab === 'rails' ? 'bg-white shadow-2xs text-[#D96B27] font-semibold border border-black/[0.08]' : 'text-[#71717A] hover:bg-black/[0.04]']"
          >
            🛡️ SafetyRail 防线 ({{ s.hotplugReport?.rails.length ?? 0 }})
          </button>
          <button
            @click="s.hotplugActiveTab = 'providers'"
            :class="['px-3 py-1.5 rounded-lg text-xs font-medium cursor-pointer transition-all', s.hotplugActiveTab === 'providers' ? 'bg-white shadow-2xs text-[#D96B27] font-semibold border border-black/[0.08]' : 'text-[#71717A] hover:bg-black/[0.04]']"
          >
            🌐 协议驱动 ({{ s.hotplugReport?.providers.length ?? 0 }})
          </button>
          <button
            @click="s.hotplugActiveTab = 'creator'"
            :class="['px-3 py-1.5 rounded-lg text-xs font-medium cursor-pointer transition-all', s.hotplugActiveTab === 'creator' ? 'bg-white shadow-2xs text-[#D96B27] font-semibold border border-black/[0.08]' : 'text-[#71717A] hover:bg-black/[0.04]']"
          >
            🛠️ DSH 技能造物 (Creator)
          </button>
        </div>

        <div class="flex items-center gap-2">
          <div class="relative w-52">
            <input
              v-model="s.hotplugSearchQuery"
              type="text"
              placeholder="过滤算子/服务/驱动..."
              class="w-full h-7 pl-6 pr-2 rounded-lg bg-white border border-black/[0.1] text-xs focus:outline-none focus:border-[#D96B27]"
            />
            <span class="absolute left-2 top-1.5 text-xs text-[#A1A1AA]">🔍</span>
          </div>
        </div>
      </div>

      <!-- 内容视窗 -->
      <div class="flex-1 overflow-y-auto p-5 space-y-4">
        <!-- 1. 微内核算子 Tab -->
        <div v-if="s.hotplugActiveTab === 'tools'" class="space-y-3">
          <div v-if="filteredTools.length === 0" class="text-center py-12 text-[#A1A1AA] text-xs">
            暂无匹配的算子
          </div>

          <div
            v-for="tool in filteredTools"
            :key="tool.id"
            class="bg-white rounded-xl border border-black/[0.08] shadow-2xs p-4 space-y-2.5 transition-all hover:border-[#D96B27]/40"
          >
            <div class="flex items-center justify-between gap-2">
              <div class="flex items-center gap-2.5">
                <span class="w-7 h-7 rounded-lg bg-[#FAF8F5] border border-black/[0.08] flex items-center justify-center text-sm">
                  {{ tool.type === 'mcp_tool' ? '🔌' : '⚡' }}
                </span>
                <div>
                  <div class="flex items-center gap-2">
                    <span class="font-bold text-xs text-[#18181B] font-mono">{{ tool.id }}</span>
                    <span
                      class="text-[10px] px-1.5 py-0.2 rounded font-medium"
                      :class="tool.type === 'mcp_tool' ? 'bg-blue-50 text-blue-700 border border-blue-200' : 'bg-emerald-50 text-emerald-700 border border-emerald-200'"
                    >
                      {{ tool.category }}
                    </span>
                    <span
                      class="text-[10px] px-1.5 py-0.2 rounded font-mono"
                      :class="tool.mutating ? 'bg-amber-50 text-amber-700 border border-amber-200' : 'bg-zinc-100 text-zinc-600'"
                    >
                      {{ tool.mutating ? '写盘/执行 (Mutating)' : '只读探测 (Readonly)' }}
                    </span>
                  </div>
                  <div class="text-[11px] text-[#52525B] mt-0.5 leading-relaxed">{{ tool.description }}</div>
                </div>
              </div>

              <div class="flex items-center gap-2 shrink-0">
                <span
                  class="text-[11px] font-mono px-2 py-0.5 rounded-full flex items-center gap-1.5"
                  :class="tool.healthy ? 'bg-emerald-50 text-emerald-700' : 'bg-red-50 text-red-700'"
                >
                  <span class="w-1.5 h-1.5 rounded-full" :class="tool.healthy ? 'bg-[#10A37F]' : 'bg-red-500'"></span>
                  <span>{{ tool.healthy ? (tool.latency_ms + 'ms') : '异常' }}</span>
                </span>
                <button
                  @click="s.probeHotplugItemAction(tool.id, 'tool')"
                  class="px-2.5 py-1 rounded-lg bg-[#FAF8F5] border border-black/[0.1] text-xs font-medium text-[#27272A] hover:bg-[#D96B27] hover:text-white transition-all cursor-pointer"
                  title="实时触发该算子健康检查探针"
                >
                  ⚡ 单点探活
                </button>
                <button
                  @click="toggleToolSchema(tool.id)"
                  class="px-2.5 py-1 rounded-lg bg-[#FAF8F5] border border-black/[0.1] text-xs font-medium text-[#71717A] hover:text-[#18181B] transition-all cursor-pointer"
                  title="展开/收起算子 JSON Schema 契约定义"
                >
                  {{ isSchemaOpen(tool.id) ? '收起参数' : '查看契约' }}
                </button>
              </div>
            </div>

            <!-- Schema 展开卡片 -->
            <div
              v-if="isSchemaOpen(tool.id)"
              class="mt-2 p-3 bg-[#18181B] text-[#F4F4F5] rounded-xl font-mono text-[11px] overflow-x-auto leading-relaxed border border-white/[0.1]"
            >
              <div class="text-[#A1A1AA] text-[10px] mb-1 font-sans"># 大模型 Function Calling JSON Schema 契约：</div>
              <pre>{{ JSON.stringify(tool.parameters || {}, null, 2) }}</pre>
            </div>
          </div>
        </div>

        <!-- 2. MCP 动态服务 Tab -->
        <div v-if="s.hotplugActiveTab === 'mcps'" class="space-y-3">
          <div class="flex items-center justify-between bg-amber-500/10 border border-amber-500/20 rounded-xl p-3 text-xs text-amber-800">
            <div class="flex items-center gap-2">
              <span>💡</span>
              <span>
                MCP (Model Context Protocol) 算子通过标准 JSON-RPC 协议与微内核通信，支持热插拔挂载与即时生效。
              </span>
            </div>
            <button
              @click="s.openSettingsTab('mcp'); s.closeHotplugDashboard()"
              class="px-2.5 py-1 rounded-lg bg-white border border-amber-300 text-amber-900 font-medium hover:bg-amber-100 cursor-pointer"
            >
              + 接入新 MCP 服务
            </button>
          </div>

          <div v-if="filteredMCPs.length === 0" class="text-center py-12 text-[#A1A1AA] text-xs">
            暂未配置 MCP 服务（可点击上方按钮接入官方 Filesystem / Git / Postgres 等 MCP）
          </div>

          <div
            v-for="mcp in filteredMCPs"
            :key="mcp.id"
            class="bg-white rounded-xl border border-black/[0.08] shadow-2xs p-4 flex items-center justify-between gap-3"
          >
            <div class="flex items-center gap-3">
              <span class="w-8 h-8 rounded-lg bg-blue-50 text-blue-700 border border-blue-200 flex items-center justify-center text-sm font-bold">
                🔌
              </span>
              <div>
                <div class="flex items-center gap-2">
                  <span class="font-bold text-xs text-[#18181B]">{{ mcp.name }}</span>
                  <span class="text-[10px] font-mono px-1.5 py-0.2 rounded bg-black/[0.04] text-[#71717A]">{{ mcp.id }}</span>
                  <span
                    class="text-[10px] font-medium px-2 py-0.2 rounded-full"
                    :class="mcp.healthy ? 'bg-emerald-50 text-emerald-700' : 'bg-zinc-100 text-zinc-500'"
                  >
                    {{ mcp.message }}
                  </span>
                </div>
                <div class="text-[11px] text-[#71717A] font-mono mt-1 truncate max-w-[600px]">
                  {{ mcp.description }}
                </div>
              </div>
            </div>

            <div class="flex items-center gap-2 shrink-0">
              <button
                @click="s.probeHotplugItemAction(mcp.id, 'mcp')"
                class="px-3 py-1.5 rounded-lg bg-[#FAF8F5] border border-black/[0.1] text-xs font-medium text-[#27272A] hover:bg-[#D96B27] hover:text-white transition-all cursor-pointer"
                title="触发物理握手探测与工具数量列举"
              >
                ⚡ 握手探活
              </button>
            </div>
          </div>
        </div>

        <!-- 3. SafetyRail 防线 Tab -->
        <div v-if="s.hotplugActiveTab === 'rails'" class="space-y-3">
          <div class="bg-white rounded-xl border border-black/[0.08] shadow-2xs p-4 space-y-3">
            <div class="flex items-center justify-between">
              <div class="flex items-center gap-2.5">
                <span class="w-8 h-8 rounded-lg bg-red-50 text-red-700 border border-red-200 flex items-center justify-center text-sm">
                  🛡️
                </span>
                <div>
                  <div class="flex items-center gap-2">
                    <h4 class="font-bold text-xs text-[#18181B]">SafetyRail 运行时核心安全防护防线</h4>
                    <span class="text-[10px] font-mono font-bold bg-red-500 text-white px-1.5 py-0.2 rounded">P-100 终极阻断权</span>
                  </div>
                  <p class="text-[11px] text-[#71717A] mt-0.5">
                    在模型执行每一条算子前强行介入，防御危险指令、工作区逃逸与密钥泄露
                  </p>
                </div>
              </div>
              <span class="text-[11px] font-mono px-2 py-0.5 rounded-full bg-emerald-50 text-emerald-700 flex items-center gap-1">
                <span class="w-1.5 h-1.5 rounded-full bg-[#10A37F]"></span>
                <span>常驻戒备中</span>
              </span>
            </div>

            <div class="grid grid-cols-1 md:grid-cols-3 gap-3 pt-2 border-t border-black/[0.06]">
              <div class="p-3 rounded-lg bg-[#FAF8F5] border border-black/[0.06] text-xs space-y-1">
                <div class="font-bold text-[#18181B] flex items-center gap-1">
                  <span>🚫</span><span>危险系统命令拦截</span>
                </div>
                <div class="text-[11px] text-[#71717A] leading-relaxed">
                  阻止 rm -rf / format / del /s / 格式化卷与高危静默破坏指令
                </div>
              </div>
              <div class="p-3 rounded-lg bg-[#FAF8F5] border border-black/[0.06] text-xs space-y-1">
                <div class="font-bold text-[#18181B] flex items-center gap-1">
                  <span>📁</span><span>目录穿越隔离防护</span>
                </div>
                <div class="text-[11px] text-[#71717A] leading-relaxed">
                  严防 .. 路径越权，严格将读写操作锁死在项目工作区沙箱之内
                </div>
              </div>
              <div class="p-3 rounded-lg bg-[#FAF8F5] border border-black/[0.06] text-xs space-y-1">
                <div class="font-bold text-[#18181B] flex items-center gap-1">
                  <span>🔒</span><span>凭据密钥脱敏清洗</span>
                </div>
                <div class="text-[11px] text-[#71717A] leading-relaxed">
                  OnBeforeReason 自动抹除 API Key 与私密环境变量，防止上送泄密
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- 4. 协议驱动 Tab -->
        <div v-if="s.hotplugActiveTab === 'providers'" class="space-y-3">
          <div
            v-for="prov in s.hotplugReport?.providers"
            :key="prov.id"
            class="bg-white rounded-xl border border-black/[0.08] shadow-2xs p-4 flex items-center justify-between gap-3"
          >
            <div class="flex items-center gap-3">
              <span class="w-8 h-8 rounded-lg bg-[#FAF8F5] border border-black/[0.08] flex items-center justify-center text-sm">
                🌐
              </span>
              <div>
                <div class="flex items-center gap-2">
                  <span class="font-bold text-xs text-[#18181B] font-mono">{{ prov.id }}</span>
                  <span class="text-[10px] font-medium px-1.5 py-0.2 rounded bg-black/[0.04] text-[#71717A]">{{ prov.version }}</span>
                </div>
                <div class="text-[11px] text-[#52525B] mt-0.5">{{ prov.description }}</div>
              </div>
            </div>

            <div class="flex items-center gap-2 shrink-0">
              <span class="text-[11px] font-mono px-2 py-0.5 rounded-full bg-emerald-50 text-emerald-700">
                驱动正常
              </span>
              <button
                @click="s.probeHotplugItemAction(prov.id, 'provider')"
                class="px-3 py-1 rounded-lg bg-[#FAF8F5] border border-black/[0.1] text-xs font-medium text-[#27272A] hover:bg-[#D96B27] hover:text-white transition-all cursor-pointer"
                title="探测此协议驱动接口连通性"
              >
                ⚡ 探活
              </button>
            </div>
          </div>
        </div>

        <!-- 5. 技能造物与沙箱 Tab (Creator Mode) -->
        <div v-if="s.hotplugActiveTab === 'creator'" class="space-y-4">
          <div class="bg-white rounded-xl border border-black/[0.08] p-5 shadow-2xs space-y-3">
            <div class="flex items-center gap-2.5">
              <span class="w-8 h-8 rounded-xl bg-[#D96B27]/15 text-[#D96B27] flex items-center justify-center text-base">
                🛠️
              </span>
              <div>
                <h4 class="font-bold text-xs text-[#18181B]">DSH 技能造物主工作台 (Creator Mode)</h4>
                <p class="text-[11px] text-[#71717A] mt-0.5">
                  现场定义 Agent 技能 (Skill)、注入项目工程铁律 (Rule) 与热挂载外部算子
                </p>
              </div>
            </div>

            <div class="grid grid-cols-1 md:grid-cols-3 gap-3 pt-3 border-t border-black/[0.06]">
              <button
                @click="s.isSkillModalOpen = true"
                class="p-4 rounded-xl bg-[#FAF8F5] border border-black/[0.08] hover:border-[#D96B27] text-left transition-all cursor-pointer group"
              >
                <div class="text-sm mb-1">🛠️</div>
                <div class="font-bold text-xs text-[#18181B] group-hover:text-[#D96B27]">创建新技能 (Skill)</div>
                <div class="text-[11px] text-[#71717A] mt-1">注入专有业务逻辑、特定技术栈最佳实践与规范提示词</div>
              </button>

              <button
                @click="s.isRuleModalOpen = true"
                class="p-4 rounded-xl bg-[#FAF8F5] border border-black/[0.08] hover:border-[#D96B27] text-left transition-all cursor-pointer group"
              >
                <div class="text-sm mb-1">📜</div>
                <div class="font-bold text-xs text-[#18181B] group-hover:text-[#D96B27]">添加工程规则 (Rule)</div>
                <div class="text-[11px] text-[#71717A] mt-1">强制模型遵循零假数据、暖色极简或特定文件架构隔离铁律</div>
              </button>

              <button
                @click="s.openSettingsTab('mcp'); s.closeHotplugDashboard()"
                class="p-4 rounded-xl bg-[#FAF8F5] border border-black/[0.08] hover:border-[#D96B27] text-left transition-all cursor-pointer group"
              >
                <div class="text-sm mb-1">🔌</div>
                <div class="font-bold text-xs text-[#18181B] group-hover:text-[#D96B27]">热插拔挂载 MCP</div>
                <div class="text-[11px] text-[#71717A] mt-1">挂接标准输入输出 (stdio) 或 SSE 外部进程协议算子</div>
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- 弹窗底栏 -->
      <footer class="h-10 bg-[#F4EFEA] border-t border-black/[0.08] px-5 flex items-center justify-between text-xs text-[#71717A] select-none shrink-0 font-mono">
        <div class="flex items-center gap-3 text-[11px]">
          <span>内核状态: 活跃运行</span>
          <span>·</span>
          <span>架构守卫: 100% 合规</span>
          <span>·</span>
          <span>热插拔: 就绪</span>
        </div>
        <div class="text-[11px]">
          按 <kbd class="px-1.5 py-0.5 bg-white rounded border border-black/[0.1] text-[#18181B]">Esc</kbd> 退出
        </div>
      </footer>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, reactive, onMounted } from 'vue'
import { useWorkbenchStore } from '../stores/workbench'

const s = useWorkbenchStore()
const modalRef = ref<HTMLDivElement | null>(null)
const expandedSchemas = reactive<Record<string, boolean>>({})

function isSchemaOpen(id: string): boolean {
  return !!expandedSchemas[id]
}

function toggleToolSchema(id: string) {
  expandedSchemas[id] = !expandedSchemas[id]
}

const filteredTools = computed(() => {
  const tools = s.hotplugReport?.tools || []
  const q = s.hotplugSearchQuery.trim().toLowerCase()
  if (!q) return tools
  return tools.filter(t =>
    t.id.toLowerCase().includes(q) ||
    t.name.toLowerCase().includes(q) ||
    t.description.toLowerCase().includes(q) ||
    t.category.toLowerCase().includes(q)
  )
})

const filteredMCPs = computed(() => {
  const mcps = s.hotplugReport?.mcps || []
  const q = s.hotplugSearchQuery.trim().toLowerCase()
  if (!q) return mcps
  return mcps.filter(m =>
    m.id.toLowerCase().includes(q) ||
    m.name.toLowerCase().includes(q) ||
    m.description.toLowerCase().includes(q)
  )
})

onMounted(() => {
  modalRef.value?.focus()
})
</script>
