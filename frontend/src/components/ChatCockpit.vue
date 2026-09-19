<template>
<main class="flex-1 min-h-0 bg-[#FAF8F5] flex flex-col justify-between overflow-hidden relative font-sans" @dragover.prevent @drop.prevent="s.onChatDrop($event)">
        <!-- 顶栏: 场景标签、多模型切换器与收起代码按钮 -->
        <header class="h-10 min-h-[40px] bg-[#FAF8F5] border-b border-black/[0.08] px-2 flex items-center justify-between text-xs select-none z-10 shrink-0 gap-2">
          <div class="flex items-center gap-1 min-w-0 flex-1 overflow-x-auto no-scrollbar">
            <button
              v-for="tab in s.sessionTabs"
              :key="tab.id"
              draggable="true"
              @dragstart="s.onTabDragStart($event, tab.id)"
              @dragover.prevent
              @drop="s.onTabDrop($event, tab.id)"
              @contextmenu="s.openTabMenu($event, tab.id)"
              @click="s.selectSession(tab.id)"
              :class="[
                'flex items-center gap-1 px-2 py-1 rounded-md shrink-0 max-w-[160px] cursor-pointer',
                s.currentSessionId === tab.id ? 'bg-white border border-black/[0.1] font-semibold text-[#18181B]' : 'text-[#71717A] hover:bg-black/[0.04]'
              ]"
            >
              <span class="truncate">{{ tab.title }}</span>
              <span class="text-[#A1A1AA] hover:text-red-500" @click.stop="s.closeSessionTab(tab.id)">✕</span>
            </button>
            <span v-if="s.sessionTabs.length === 0" class="font-bold text-[#18181B] px-1 truncate">{{ s.currentSession.title }}</span>
            <span class="text-[10px] text-[#71717A] bg-black/[0.04] px-1.5 py-0.2 rounded font-mono">{{ s.selectedModel || '未配置渠道' }}</span>

            <select
              v-if="s.availableModels.length > 0"
              v-model="s.selectedModel"
              class="bg-white border border-black/[0.1] rounded-lg px-2 py-0.8 text-xs font-mono font-medium text-[#10A37F] focus:outline-none focus:border-[#D96B27] cursor-pointer shadow-2xs"
            >
              <option v-for="m in s.availableModels" :key="m" :value="m">{{ m }}</option>
            </select>
            <button
              v-else
              type="button"
              class="text-[10px] text-[#D96B27] underline cursor-pointer"
              @click="s.openSettingsTab('models')"
            >添加渠道</button>

            <!-- 任务状态机指示徽章 (Task Status) -->
            <div
              v-if="s.currentTaskStatus && s.currentTaskStatus !== 'idle'"
              class="flex items-center gap-1 px-2 py-0.8 rounded-lg text-[11px] font-medium shadow-2xs shrink-0 select-none transition-all ml-1"
              :class="{
                'bg-[#D96B27]/10 border border-[#D96B27]/30 text-[#D96B27]': s.currentTaskStatus === 'running',
                'bg-[#D96B27]/15 border border-[#D96B27]/40 text-[#B8551B] font-semibold animate-pulse': s.currentTaskStatus === 'pending_diff',
                'bg-amber-500/10 border border-amber-500/30 text-amber-700': s.currentTaskStatus === 'capped',
                'bg-red-500/10 border border-red-500/30 text-red-600': s.currentTaskStatus === 'tdd_failed' || s.currentTaskStatus === 'failed',
                'bg-[#10A37F]/10 border border-[#10A37F]/30 text-[#10A37F]': s.currentTaskStatus === 'completed',
                'bg-zinc-100 border border-zinc-200 text-zinc-600': s.currentTaskStatus === 'interrupted'
              }"
              :title="'任务状态机状态: ' + s.currentTaskStatus"
            >
              <span v-if="s.currentTaskStatus === 'running'" class="inline-block animate-spin text-[10px]">⚡</span>
              <span v-else-if="s.currentTaskStatus === 'pending_diff'">📝</span>
              <span v-else-if="s.currentTaskStatus === 'capped'">⚠️</span>
              <span v-else-if="s.currentTaskStatus === 'tdd_failed' || s.currentTaskStatus === 'failed'">❌</span>
              <span v-else-if="s.currentTaskStatus === 'completed'">✓</span>
              <span v-else-if="s.currentTaskStatus === 'interrupted'">⏹</span>

              <span>
                {{
                  s.currentTaskStatus === 'running' ? '执行中' :
                  s.currentTaskStatus === 'pending_diff' ? '待确认 Diff' :
                  s.currentTaskStatus === 'capped' ? '已达上限' :
                  s.currentTaskStatus === 'tdd_failed' ? '测试未通过' :
                  s.currentTaskStatus === 'failed' ? '执行失败' :
                  s.currentTaskStatus === 'completed' ? '已完成' :
                  s.currentTaskStatus === 'interrupted' ? '已中断' : s.currentTaskStatus
                }}
              </span>
            </div>
          </div>

          <div class="flex items-center gap-1.5 shrink-0">
            <button
              @click="s.openHotplugDashboard()"
              class="flex items-center gap-1 px-2 py-1 rounded-lg bg-white border border-black/[0.08] text-xs font-medium text-[#52525B] hover:text-[#D96B27] hover:border-[#D96B27]/30 shadow-2xs transition-all cursor-pointer"
              title="打开插件热插拔中心与 DSH 算子大盘"
            >
              <span>🧩</span>
              <span>算子大盘</span>
              <span v-if="s.hotplugReport" class="text-[10px] font-mono px-1 rounded bg-[#D96B27]/10 text-[#D96B27]">
                {{ s.hotplugReport.summary.total_tools }}
              </span>
            </button>
            <button
              @click="s.setWorkspaceView(s.isDiffOpen ? 'chat' : 'split')"
              class="flex items-center gap-1.5 px-2.5 py-1 rounded-lg bg-white border border-black/[0.08] text-xs text-[#52525B] hover:text-[#18181B] hover:bg-black/[0.02] shadow-2xs transition-all cursor-pointer"
            >
              <span>{{ s.isDiffOpen ? '收起代码面板' : '💻 代码面板' }}</span>
            </button>
          </div>
        </header>

        <!-- 待采纳代码变更提示条 (已写入工作区，请审查 Diff，人点接受才算完成) -->
        <div v-if="s.pendingDiffFiles.length > 0" class="bg-[#D96B27]/10 border-b border-[#D96B27]/20 px-3 py-1.5 flex items-center justify-between text-xs shrink-0 select-none gap-2">
          <div class="flex items-center gap-2 text-[#B8551B] min-w-0 flex-1">
            <span class="animate-pulse">⚠️</span>
            <span class="font-semibold shrink-0">已写入工作区，请审查 Diff：</span>
            <div class="flex items-center gap-1.5 overflow-x-auto no-scrollbar py-0.5">
              <button
                v-for="f in s.pendingDiffFiles"
                :key="f"
                @click="s.openFileDiff(f, 'diff')"
                type="button"
                class="font-mono text-[11px] px-1.5 py-0.5 rounded border transition-colors cursor-pointer shrink-0"
                :class="s.activeDiffFile === f ? 'bg-[#D96B27] text-white border-[#D96B27] font-bold shadow-2xs' : 'bg-white/80 hover:bg-white text-[#D96B27] border-[#D96B27]/30'"
                :title="'点击审查 ' + f + ' 的 Diff'"
              >
                {{ f }}
              </button>
            </div>
          </div>
          <div class="flex items-center gap-2 shrink-0">
            <button
              @click="s.revertAllPendingDiffFilesAction"
              class="px-2 py-0.5 rounded bg-white border border-red-200 text-[11px] font-medium text-red-600 hover:bg-red-50 cursor-pointer shadow-2xs transition-all"
              title="放弃工作区中全部待确认文件的改动 (Git Checkout)"
            >
              全部放弃
            </button>
            <button
              @click="s.stageAllPendingDiffFilesAction"
              class="px-2 py-0.5 rounded bg-[#10A37F] text-white text-[11px] font-semibold hover:bg-[#0D8C6D] cursor-pointer shadow-2xs transition-all"
              title="采纳全部待确认文件的改动并暂存 (Git Stage)"
            >
              ✓ 全部采纳
            </button>
          </div>
        </div>

        <!-- 真实动态对话消息列表：跟最新输出，也可拖拽/滚轮回看 -->
        <div class="flex-1 min-h-0 relative">
        <div
          ref="messagesContainerRef"
          class="h-full overflow-y-scroll overflow-x-hidden p-4 space-y-4 flex flex-col select-text overscroll-contain"
          :class="dragging ? 'cursor-grabbing' : 'cursor-grab'"
          @scroll="s.onMessagesScroll"
          @mousedown="onTranscriptDown"
          @mousemove="onTranscriptMove"
          @mouseup="onTranscriptUp"
          @mouseleave="onTranscriptUp"
        >
          <!-- 干净真实的空会话状态 -->
          <div v-if="!s.currentSession.messages || s.currentSession.messages.length === 0" class="flex-1 flex flex-col items-center justify-center text-center p-8 select-none my-auto">
            <div class="w-14 h-14 rounded-2xl bg-white border border-black/[0.08] shadow-xs flex items-center justify-center text-2xl mb-4">
              💬
            </div>
            <h3 class="text-sm font-bold text-[#18181B] mb-1.5">湉码 / tiancode</h3>
            <p class="text-xs text-[#71717A] max-w-sm mb-5 leading-relaxed">
              这是一条尚未保存的新对话。输入任务并发送后，会按当前项目记入左侧列表。
            </p>
            <div class="flex items-center gap-2">
              <button
                @click="s.createNewSession"
                class="px-3.5 py-1.5 rounded-xl bg-[#D96B27] text-white text-xs font-semibold shadow-xs hover:bg-[#B8551B] transition-all cursor-pointer flex items-center gap-1"
              >
                <span>＋</span><span>新建会话</span>
              </button>
            </div>
          </div>

          <button
            v-if="s.hiddenHistoryCount > 0"
            type="button"
            class="self-center text-[11px] text-[#D96B27] underline cursor-pointer"
            @click="s.revealFullHistory"
          >显示更早的 {{ s.hiddenHistoryCount }} 条（默认只渲染最近 80 条）</button>
          <template v-for="msg in s.visibleMessages" :key="msg.id">
            <!-- 用户提问气泡 -->
            <div v-if="msg.role === 'user'" class="flex justify-end">
              <div class="max-w-[80%] bg-[#F4EFEA] text-[#18181B] px-4 py-3 rounded-2xl rounded-tr-sm border border-black/[0.06] shadow-2xs text-xs leading-relaxed whitespace-pre-line">
                {{ msg.content }}
              </div>
            </div>

            <!-- Agent 回答卡片组 -->
            <div v-else class="flex flex-col items-start space-y-3.5 max-w-3xl w-full">
              <div class="flex items-center gap-2 text-xs font-semibold text-[#18181B]">
                <div class="w-4 h-4 rounded bg-[#D96B27] text-white flex items-center justify-center text-[9px] font-bold">T</div>
                <span>湉码 Agent</span>
                <span class="text-[10px] text-[#10A37F] bg-[#10A37F]/10 px-1.5 py-0.2 rounded font-mono">{{ s.selectedModel }} · 自主算子模式</span>
              </div>

              <!-- 深度思考抽屉 (真实 reasoning_content) -->
              <div v-if="msg.thinking" class="w-full rounded-xl border border-black/[0.08] bg-white/70 shadow-2xs overflow-hidden transition-all">
                <div class="p-2.5 flex items-center justify-between bg-black/[0.02] text-xs font-semibold text-[#18181B] cursor-pointer hover:bg-black/[0.04]" @click="msg._thinkingExpanded = !msg._thinkingExpanded">
                  <div class="flex items-center gap-2">
                    <span>🧠</span><span>深度心智思考 (Reasoning Process)</span>
                  </div>
                  <span class="text-[10px] text-[#71717A] font-mono">{{ msg._thinkingExpanded ? '▲ 收起' : '▼ 展开' }}</span>
                </div>
                <div v-show="msg._thinkingExpanded" class="px-3 pb-3 text-xs text-[#71717A] leading-relaxed italic border-t border-black/[0.04] pt-2 whitespace-pre-wrap font-mono">
                  {{ msg.thinking }}
                </div>
              </div>

              <!-- Tool Call 算子执行卡片列表 (多轮自主执行时序链路) -->
              <div v-if="(msg.tools && msg.tools.length > 0) || msg.tool" class="w-full space-y-2">
                <div class="w-full rounded-xl border border-black/[0.08] bg-white/70 shadow-2xs overflow-hidden transition-all">
                  <div class="p-2.5 flex items-center justify-between bg-black/[0.02] text-xs font-semibold text-[#18181B] cursor-pointer hover:bg-black/[0.04]" @click="msg._toolsExpanded = !msg._toolsExpanded">
                    <div class="flex items-center gap-2">
                      <span>🔧</span><span>调用 {{ msg.tools && msg.tools.length > 0 ? msg.tools.length : 1 }} 个工具</span>
                    </div>
                    <span class="text-[10px] text-[#71717A] font-mono">{{ msg._toolsExpanded ? '▲ 收起' : '▼ 展开' }}</span>
                  </div>
                  <div v-show="msg._toolsExpanded" class="p-2 space-y-2 bg-black/[0.01]">
                    <div
                      v-for="(tItem, tIdx) in (msg.tools && msg.tools.length > 0 ? msg.tools : [msg.tool!])"
                      :key="tItem.id || tIdx"
                      class="rounded-xl border border-black/[0.08] bg-white shadow-2xs overflow-hidden"
                    >
                      <div class="p-2 flex items-center justify-between bg-black/[0.02] text-xs font-mono">
                        <span class="font-bold text-[#18181B]">$_ {{ tItem.name }} {{ typeof tItem.args === 'string' ? tItem.args : JSON.stringify(tItem.args) }}</span>
                        <span class="text-[10px]" :class="(tItem.output || '').startsWith('[') || (tItem.output || '').includes('error') || (tItem.output || '').includes('拦截') ? 'text-red-500' : ((tItem.output === '正在执行...' || !tItem.output) ? 'text-amber-600' : 'text-[#10A37F]')">
                          {{ (tItem.output || '').startsWith('[') || (tItem.output || '').includes('拦截') ? '● 已拦截/失败' : ((tItem.output === '正在执行...' || !tItem.output) ? '● 执行中' : '● 完成') }}
                        </span>
                      </div>
                      <div class="p-2.5 bg-[#18181B] text-emerald-400 font-mono text-[11px] whitespace-pre-wrap max-h-64 overflow-y-auto">
                        {{ tItem.output }}
                      </div>
                    </div>
                  </div>
                </div>
              </div>


              <!-- 优雅 Markdown 正文 -->
              <div
                class="markdown-body text-xs text-[#27272A] leading-relaxed space-y-2 bg-white/70 p-3.5 rounded-xl border border-black/[0.04] w-full"
                v-html="s.renderMarkdown(msg.content)"
              ></div>
              <div class="flex items-center gap-2 text-[10px] text-[#A1A1AA]">
                <button class="hover:text-[#18181B] cursor-pointer" @click="s.copyMessage(msg.content)">复制</button>
                <button class="hover:text-[#18181B] cursor-pointer" @click="s.regenerateLast">重新生成</button>
              </div>
            </div>
          </template>

          <!-- 选择题卡片 (WP-H1) -->
          <div v-if="s.pendingChoice" class="flex flex-col items-start space-y-3.5 max-w-3xl w-full">
            <div class="flex items-center gap-2 text-xs font-semibold text-[#18181B]">
              <div class="w-4 h-4 rounded bg-[#D96B27] text-white flex items-center justify-center text-[9px] font-bold">?</div>
              <span>需要您的选择</span>
            </div>
            <div class="w-full rounded-xl border border-[#D96B27]/40 bg-[#FFFaf5] shadow-2xs overflow-hidden p-4 space-y-3">
              <h3 class="text-sm font-bold text-[#18181B]">{{ s.pendingChoice.question }}</h3>
              <div class="space-y-2">
                <label v-for="opt in s.pendingChoice.options" :key="opt.id" class="flex items-start gap-3 p-3 rounded-lg border bg-white cursor-pointer hover:border-[#D96B27]/60 transition-all" :class="s.pendingChoiceSelected === opt.id ? 'border-[#D96B27] bg-[#D96B27]/5' : 'border-black/[0.08]'">
                  <input type="radio" :value="opt.id" v-model="s.pendingChoiceSelected" name="choice_opt" class="mt-0.5 accent-[#D96B27]">
                  <div class="flex flex-col">
                    <span class="text-sm font-medium text-[#18181B] flex items-center gap-2">
                      {{ opt.label }}
                      <span v-if="opt.recommended" class="text-[10px] text-white bg-[#D96B27] px-1.5 rounded">推荐</span>
                    </span>
                    <span v-if="opt.description" class="text-xs text-[#71717A] mt-1">{{ opt.description }}</span>
                  </div>
                </label>
              </div>
              <div v-if="s.pendingChoice.allow_custom" class="mt-2">
                <input v-model="s.pendingChoiceCustomNote" type="text" placeholder="补充说明（可选）..." class="w-full text-xs p-2 rounded-lg border border-black/[0.08] bg-white focus:outline-none focus:border-[#D96B27]" />
              </div>
              <div class="flex items-center justify-end gap-2 pt-2 border-t border-[#D96B27]/10 mt-2">
                <button @click="s.submitAgentChoice(s.pendingChoiceSelected, s.pendingChoiceCustomNote)" :disabled="!s.pendingChoiceSelected" class="px-4 py-1.5 rounded-lg bg-[#D96B27] text-white text-xs font-semibold shadow-xs hover:bg-[#B8551B] disabled:opacity-50 disabled:cursor-not-allowed">确定提交</button>
              </div>
            </div>
          </div>

          <!-- 危险命令确认卡片 (WP-H2) -->
          <div v-if="s.pendingConfirm" class="flex flex-col items-start space-y-3.5 max-w-3xl w-full">
            <div class="flex items-center gap-2 text-xs font-semibold text-amber-600">
              <div class="w-4 h-4 rounded bg-amber-500 text-white flex items-center justify-center text-[9px] font-bold">!</div>
              <span>危险操作确认</span>
            </div>
            <div class="w-full rounded-xl border border-amber-500/40 bg-amber-50/50 shadow-2xs overflow-hidden p-4 space-y-3">
              <h3 class="text-sm font-bold text-[#18181B]">拦截原因：{{ s.pendingConfirm.reason }}</h3>
              <div class="p-3 bg-black/5 rounded-lg border border-black/10 font-mono text-[11px] text-[#18181B] whitespace-pre-wrap break-all">{{ s.pendingConfirm.args_preview }}</div>
              <div class="flex items-center gap-3 pt-2">
                <button @click="s.submitAgentConfirm(false)" class="flex-1 py-1.5 rounded-lg border border-amber-600/30 bg-white text-amber-700 text-xs font-semibold shadow-sm hover:bg-amber-50">拒绝并让模型改方案</button>
                <button @click="s.submitAgentConfirm(true)" class="px-4 py-1.5 rounded-lg bg-amber-600 text-white text-xs font-semibold shadow-xs hover:bg-amber-700">允许这一次</button>
              </div>
            </div>
          </div>

        </div>
        <button
          v-if="!s.stickToBottom"
          type="button"
          class="absolute bottom-3 left-1/2 -translate-x-1/2 z-10 px-3 py-1.5 rounded-full bg-[#18181B] text-white text-[11px] font-medium shadow-xs cursor-pointer"
          @click="s.followLatestChat"
        >↓ 回到最新</button>
        </div>

        <!-- 底部输入胶囊舱 (Prompt Capsule) -->
        <div class="p-3 bg-[#FAF8F5] border-t border-black/[0.06] select-none relative">
          <!-- 真实附件预览托盘 -->
          <div v-if="s.attachedFiles.length" class="flex items-center gap-1.5 pb-2 overflow-x-auto no-scrollbar">
            <div
              v-for="(file, idx) in s.attachedFiles"
              :key="idx"
              class="flex items-center gap-1.5 px-2.5 py-1 rounded-lg bg-white border border-black/[0.08] text-xs font-mono shadow-2xs text-[#18181B]"
            >
              <span class="text-[#D96B27]">📎</span>
              <span class="truncate max-w-xs">{{ file }}</span>
              <button @click="s.attachedFiles.splice(idx, 1)" class="text-[#71717A] hover:text-red-500 cursor-pointer">✕</button>
            </div>
          </div>

          <!-- 输入卡片 -->
          <div class="rounded-2xl bg-white border border-black/[0.12] shadow-sm focus-within:border-[#D96B27] focus-within:ring-2 focus-within:ring-[#D96B27]/15 transition-all p-2.5 flex flex-col gap-2">
            <textarea
              v-model="s.inputPrompt"
              rows="2"
              placeholder="给 湉码 Agent 发送指令（@ 引用会话/技能/文件，/ 调起指令，Shift+Enter 换行，拖入文件作为附件）"
              class="w-full text-xs text-[#18181B] placeholder-[#A1A1AA] bg-transparent focus:outline-none resize-none leading-relaxed"
              @keydown="s.handleComposerKeydown"
            ></textarea>
            <div
              v-if="s.mentionOpen && s.mentionItems.length > 0"
              class="absolute left-4 right-4 bottom-[7.5rem] z-20 bg-white border border-black/[0.1] rounded-xl shadow-lg max-h-48 overflow-y-auto"
            >
              <button
                v-for="(item, idx) in s.mentionItems"
                :key="item.id"
                class="w-full text-left px-3 py-1.5 text-xs flex justify-between cursor-pointer"
                :class="idx === s.mentionIndex ? 'bg-[#D96B27]/10' : 'hover:bg-black/[0.03]'"
                @mousedown.prevent="s.applyMention(item)"
              >
                <span>{{ item.label }}</span>
                <span class="text-[10px] text-[#A1A1AA]">{{ item.kind }}</span>
              </button>
            </div>

            <div class="flex items-center justify-between border-t border-black/[0.04] pt-2 text-xs">
              <div class="flex items-center gap-1.5 min-w-0 flex-1">
                <button
                  @click="s.triggerUpload"
                  class="px-2.5 py-1 rounded-full text-xs text-[#52525B] hover:text-[#18181B] hover:bg-black/[0.04] flex items-center gap-1 cursor-pointer shrink-0"
                  title="调起系统文件选择框"
                >
                  <span>📎</span><span>上传</span>
                </button>
                <div class="h-3.5 w-px bg-black/[0.1] mx-0.5 shrink-0"></div>
                <span class="text-[11px] text-[#71717A] truncate font-sans hidden sm:inline-block">
                  💡 输入 @ 关联文件，/ 查看指令，自然语言自由对话与改码
                </span>
              </div>

              <div class="flex items-center gap-2 shrink-0">
                <span class="text-[10px] text-[#A1A1AA] font-mono">{{ s.isStreaming ? '正在流式推理...' : '就绪' }}</span>
                <button
                  v-if="s.isStreaming"
                  @click="s.stopGenerationAction"
                  title="中断本次生成 (Esc)"
                  class="w-7 h-7 rounded-xl flex items-center justify-center font-bold shadow-xs transition-all cursor-pointer bg-red-500 hover:bg-red-600 text-white animate-pulse"
                >
                  ■
                </button>
                <button
                  v-else
                  @click="s.handleSend"
                  title="发送消息 (Enter)"
                  class="w-7 h-7 rounded-xl flex items-center justify-center font-bold shadow-xs transition-all cursor-pointer bg-[#D96B27] hover:bg-[#B8551B] text-white"
                >
                  ↑
                </button>
              </div>
            </div>
          </div>
        </div>
      </main>

    <!-- 项目宪法弹窗 (符合铁律 5: 暖色极简、严格居中、Esc退出、显式[X]) -->


    <!-- 待确认 Diff 拦截二次确认弹窗 (符合铁律 5: 居中、暖米白、Esc退出、显式[X]) -->
    <div
      v-if="s.isPendingDiffPromptOpen"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/45 backdrop-blur-xs font-sans"
      @keydown.esc="s.isPendingDiffPromptOpen = false"
      tabindex="-1"
    >
      <div class="w-[90vw] max-w-md bg-white rounded-2xl shadow-2xl border border-black/[0.1] flex flex-col overflow-hidden">
        <header class="h-11 bg-[#FAF8F5] border-b border-black/[0.08] px-4 flex items-center justify-between select-none shrink-0">
          <div class="flex items-center gap-2">
            <span>📝</span>
            <h4 class="font-bold text-xs text-[#18181B]">工作区存在待确认代码改动</h4>
          </div>
          <button @click="s.isPendingDiffPromptOpen = false" class="p-1 rounded-md text-[#71717A] hover:bg-black/[0.05] cursor-pointer" title="关闭 (Esc)">✕</button>
        </header>
        <div class="p-4 text-xs space-y-3">
          <p class="text-[#52525B] leading-relaxed">
            当前工作区有 <strong class="text-[#D96B27]">{{ s.pendingDiffFiles.length }}</strong> 个待确认的代码改动文件（如 <code class="font-mono text-[11px] bg-black/[0.04] px-1 py-0.5 rounded">{{ s.pendingDiffFiles.slice(0, 3).join(', ') }}</code> 等）。
          </p>
          <div class="p-3 bg-[#FAF8F5] rounded-xl border border-black/[0.06] text-[11px] text-[#71717A] leading-relaxed">
            💡 <strong>强烈建议</strong>：在开启新一轮推理前，先在右侧 Diff 审查区中处理改动（采纳或放弃），避免后续修改冲掉现有工作区代码。
          </div>
        </div>
        <footer class="h-12 bg-[#FAF8F5] border-t border-black/[0.08] px-4 flex items-center justify-end gap-2 select-none shrink-0">
          <button
            @click="s.isPendingDiffPromptOpen = false"
            class="px-3 py-1.5 rounded-lg border border-black/[0.1] text-xs hover:bg-black/[0.03] cursor-pointer"
          >
            取消
          </button>
          <button
            @click="s.forceSendWithPendingDiff = true; s.isPendingDiffPromptOpen = false; s.handleSend()"
            class="px-3 py-1.5 rounded-lg border border-[#D96B27]/40 text-[#D96B27] hover:bg-[#D96B27]/10 text-xs font-semibold cursor-pointer"
          >
            暂不处理，仍然发送
          </button>
          <button
            @click="s.isPendingDiffPromptOpen = false; s.isDiffOpen = true; s.editorView = 'diff'"
            class="px-3.5 py-1.5 rounded-lg bg-[#D96B27] hover:bg-[#B8551B] text-white text-xs font-bold shadow-xs cursor-pointer"
          >
            前往 Diff 审查
          </button>
        </footer>
      </div>
    </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { storeToRefs } from 'pinia'
import { useWorkbenchStore } from '../stores/workbench'
const s = useWorkbenchStore()
const { messagesContainerRef } = storeToRefs(s)

const dragging = ref(false)
let dragY = 0
let dragStartScroll = 0
let moved = false

function onTranscriptDown(e: MouseEvent) {
  if (e.button !== 0) return
  const t = e.target as HTMLElement
  if (t.closest('button, a, textarea, input, pre, code')) return
  dragging.value = true
  dragY = e.clientY
  dragStartScroll = messagesContainerRef.value?.scrollTop || 0
  moved = false
}

function onTranscriptMove(e: MouseEvent) {
  if (!dragging.value || !messagesContainerRef.value) return
  const dy = e.clientY - dragY
  if (Math.abs(dy) > 3) moved = true
  if (moved) {
    e.preventDefault()
    messagesContainerRef.value.scrollTop = dragStartScroll - dy
    s.onMessagesScroll()
  }
}

function onTranscriptUp() {
  dragging.value = false
}
</script>


<style>
.markdown-body pre { background-color:#18181B; color:#F4F4F5; padding:0.75rem; border-radius:0.5rem; overflow-x:auto; font-family:'Fira Code',monospace; margin:0.5rem 0; border:1px solid rgba(255,255,255,0.1); }
.markdown-body code { font-family:'Fira Code',monospace; background-color:rgba(0,0,0,0.05); padding:0.1rem 0.3rem; border-radius:0.25rem; }
.markdown-body pre code { background-color:transparent; padding:0; }
.markdown-body p { margin-bottom:0.5rem; }
.markdown-body ul, .markdown-body ol { padding-left:1.25rem; margin-bottom:0.5rem; }
.markdown-body ul { list-style-type:disc; }
.markdown-body ol { list-style-type:decimal; }
</style>
