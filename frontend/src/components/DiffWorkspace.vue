<template>
<!-- 右侧 Monaco Diff 审查工作区 (Diff Workspace) -->
      <section
        v-show="s.isDiffOpen && s.workspaceView !== 'chat'"
        :style="s.workspaceView === 'editor' ? { width: '100%', flex: '1 1 0%' } : { width: s.editorSplitPercent + '%', minWidth: '360px', maxWidth: '85%' }"
        class="border-l border-black/[0.08] bg-[#FAF8F5] flex flex-col select-none z-10 shrink-0 font-sans overflow-hidden"
      >
        <div class="flex-1 flex flex-col justify-between min-w-0 overflow-hidden">
        <!-- 多文件标签页栏 (Editor Tab Bar) -->
        <div v-if="s.openEditorTabs.length > 0" class="h-8 bg-[#F4EFEA] border-b border-black/[0.08] flex items-center px-2 gap-1 overflow-x-auto no-scrollbar shrink-0 select-none">
          <div
            v-for="tab in s.openEditorTabs"
            :key="tab.path"
            @click="s.switchEditorTab(tab.path)"
            :class="[
              'flex items-center gap-1.5 px-2.5 py-1 rounded-t-md font-mono text-[11px] cursor-pointer border-t border-x transition-all shrink-0',
              s.activeDiffFile === tab.path ? 'bg-white text-[#18181B] font-bold border-black/[0.08] shadow-2xs' : 'bg-transparent text-[#71717A] hover:bg-black/[0.03] border-transparent'
            ]"
            :title="tab.path"
          >
            <span>📄</span>
            <span class="truncate max-w-[120px]">{{ tab.title }}</span>
            <span v-if="tab.dirty" class="w-1.5 h-1.5 rounded-full bg-[#D96B27]" title="未保存改动"></span>
            <button
              @click.stop="s.closeEditorTab(tab.path, $event)"
              class="hover:text-red-500 rounded p-0.5 text-[10px] cursor-pointer"
              title="关闭标签页"
            >✕</button>
          </div>
        </div>

        <header class="h-10 min-h-[40px] bg-[#FAF8F5] border-b border-black/[0.08] px-3 flex items-center justify-between text-xs shrink-0">
          <div class="flex items-center gap-2 min-w-0">
            <span class="text-sm">📄</span>
            <span class="font-mono font-bold text-[#18181B] truncate">{{ s.activeDiffFile }}</span>
            <span class="text-[10px] font-mono text-[#10A37F] bg-[#10A37F]/10 px-1.5 py-0.2 rounded font-bold shrink-0">
              {{ s.diffReport?.stats || '0 行修改' }}
            </span>
          </div>

          <div class="flex items-center gap-1.5 shrink-0">
            <button
              @click="s.editorView = 'edit'"
              :class="['px-2 py-0.5 rounded-md text-[10px] cursor-pointer', s.editorView === 'edit' ? 'bg-[#18181B] text-white' : 'bg-white border border-black/[0.08]']"
            >编辑</button>
            <button
              @click="s.editorView = 'diff'"
              :class="['px-2 py-0.5 rounded-md text-[10px] cursor-pointer', s.editorView === 'diff' ? 'bg-[#18181B] text-white' : 'bg-white border border-black/[0.08]']"
            >Diff</button>
            <button
              v-if="s.editorView === 'edit'"
              @click="s.saveEditor"
              :disabled="!s.editorDirty"
              class="px-2 py-0.5 rounded-md bg-[#D96B27] text-white text-[10px] font-semibold disabled:opacity-40 cursor-pointer"
              title="写入磁盘 (Ctrl+S)"
            >保存</button>
            <button @click="s.loadDiff" class="p-1 rounded-md text-[#71717A] hover:bg-black/[0.04] cursor-pointer" title="刷新代码差异">🔄</button>
            <button
              @click="s.revertFileAction"
              class="flex items-center gap-1 px-2 py-0.8 rounded-md bg-white border border-red-200 text-xs font-semibold text-red-600 hover:bg-red-50 shadow-2xs transition-all cursor-pointer"
              title="丢弃本次物理改动 (Git Checkout)"
            >
              <span>✕</span><span>放弃</span>
            </button>
            <button
              @click="s.stageFileAction"
              class="flex items-center gap-1 px-2.5 py-0.8 rounded-md bg-[#10A37F] hover:bg-[#0D8C6D] text-white text-xs font-semibold shadow-xs transition-all cursor-pointer"
              title="确认采纳文件修改并提交暂存区 (Git Stage)"
            >
              <span>✓</span><span>采纳变更</span>
            </button>
            <button
              @click="s.toggleEditorFullscreen()"
              class="text-[#71717A] hover:text-[#18181B] p-1 rounded-md hover:bg-black/[0.05] cursor-pointer ml-0.5"
              :title="s.workspaceView === 'editor' ? '还原为双栏协同 (Alt+F)' : '代码全屏专注 (Alt+F)'"
            >
              <svg v-if="s.workspaceView === 'editor'" class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M8 3v3a2 2 0 0 1-2 2H3m18 0h-3a2 2 0 0 1-2-2V3m0 18v-3a2 2 0 0 1 2-2h3M3 16h3a2 2 0 0 1 2 2v3"/></svg>
              <svg v-else class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M15 3h6v6m-6 0l6-6M9 21H3v-6m6 0l-6 6M21 9V3h-6m0 6l6-6M3 15v6h6m-6 0l6-6"/></svg>
            </button>
            <button
              @click="s.toggleEditorPanel()"
              class="text-[#71717A] hover:text-[#18181B] p-1 rounded-md hover:bg-black/[0.05] cursor-pointer ml-0.5"
              title="收起代码面板 (Ctrl+\)"
            >✕</button>
          </div>
        </header>

        <div v-if="s.editorView === 'edit'" class="flex-1 min-h-0 bg-[#1e1e1e]">
          <MonacoEditor
            v-if="s.activeDiffFile"
            v-model="s.editorContent"
            :language="s.activeDiffFile"
            :diagnostics="s.editorDiagnostics"
            :line="s.targetEditorLine"
            @update:modelValue="s.markEditorDirty"
          />
          <div v-else class="h-full flex items-center justify-center text-xs text-[#A1A1AA]">从左侧文件树打开文件即可编辑</div>
        </div>

        <!-- 真实物理行级 Diff (Red / Green) -->
        <div v-else class="flex-1 overflow-y-auto bg-[#18181B] text-[#F4F4F5] font-mono text-[11px] p-2 space-y-2 select-text flex flex-col">
          <div v-if="!s.activeDiffFile || !s.diffReport?.lines || s.diffReport.lines.length === 0" class="flex-1 flex flex-col items-center justify-center p-8 text-center text-[#71717A] my-auto">
            <span class="text-3xl mb-3">📄</span>
            <p class="text-xs font-semibold text-[#A1A1AA]">暂无代码差异对比</p>
            <p class="text-[11px] text-[#71717A] mt-1.5 max-w-xs leading-relaxed">
              当前工作区干净，或尚未选定对比文件。可从左侧文件树或 Git 状态点击文件审查。
            </p>
          </div>

          <!-- 分块 Hunks 细粒度审查模式 -->
          <template v-if="s.diffReport?.hunks && s.diffReport.hunks.length > 0">
            <div
              v-for="(hunk, hIdx) in s.diffReport.hunks"
              :key="hIdx"
              class="p-2.5 rounded-xl bg-black/40 border border-white/[0.08] select-none"
            >
              <div class="flex items-center justify-between pb-1.5 mb-1.5 border-b border-white/[0.06] text-[10px]">
                <div class="flex items-center gap-1.5 font-mono min-w-0">
                  <span class="text-[#D96B27] font-bold shrink-0">块 #{{ hIdx + 1 }}</span>
                  <span class="text-white/40 truncate">{{ hunk.header }}</span>
                  <span v-if="hunk.add_count > 0" class="text-emerald-400 font-bold shrink-0">+{{ hunk.add_count }}</span>
                  <span v-if="hunk.del_count > 0" class="text-rose-400 font-bold shrink-0">-{{ hunk.del_count }}</span>
                </div>
                <div class="flex items-center gap-1.5 shrink-0">
                  <button
                    @click="s.applyHunkAction(hunk.index, true)"
                    title="将此块代码改动暂存入 Git Index (git apply --cached)"
                    class="px-2 py-0.5 rounded bg-[#10A37F]/20 hover:bg-[#10A37F]/30 text-[#10A37F] font-bold text-[10px] cursor-pointer transition-all active:scale-95"
                  >
                    ✓ 采纳块
                  </button>
                  <button
                    @click="s.discardHunkAction(hunk.index)"
                    title="无损丢弃撤销此块代码改动 (git apply --reverse)"
                    class="px-2 py-0.5 rounded bg-red-500/20 hover:bg-red-500/30 text-red-300 font-bold text-[10px] cursor-pointer transition-all active:scale-95"
                  >
                    ✕ 丢弃块
                  </button>
                </div>
              </div>
              <div class="space-y-0.5 font-mono text-[11px] select-text">
                <div
                  v-for="(line, lIdx) in hunk.lines"
                  :key="lIdx"
                  :class="[
                    'px-2 py-0.5 rounded leading-relaxed flex items-center gap-2 whitespace-pre-wrap font-mono transition-colors',
                    line.type === 'add' ? 'bg-[#10A37F]/15 text-emerald-300 border-l-2 border-emerald-500' : '',
                    line.type === 'del' ? 'bg-red-500/15 text-rose-300 border-l-2 border-rose-500' : '',
                    line.type === 'ctx' ? 'text-zinc-400 hover:bg-white/[0.02]' : ''
                  ]"
                >
                  <span class="flex-1">{{ line.text }}</span>
                </div>
              </div>
            </div>
          </template>

          <!-- 备用平铺模式 (Clean 工作区或无 Hunk 分块) -->
          <template v-else>
            <div v-if="s.diffReport?.header" class="text-white/40 pb-1 mb-1 border-b border-white/[0.06] text-[10px]">
              {{ s.diffReport.header }}
            </div>
            <div
              v-for="(line, idx) in (s.diffReport?.lines || [])"
              :key="idx"
              :class="[
                'px-2 py-0.5 rounded leading-relaxed flex items-center gap-2 whitespace-pre-wrap font-mono transition-colors',
                line.type === 'add' ? 'bg-[#10A37F]/15 text-emerald-300 border-l-2 border-emerald-500' : '',
                line.type === 'del' ? 'bg-red-500/15 text-rose-300 border-l-2 border-rose-500' : '',
                line.type === 'ctx' ? 'text-zinc-400 hover:bg-white/[0.02]' : ''
              ]"
            >
              <span class="w-5 text-[10px] select-none opacity-40 font-mono text-right">{{ idx + 1 }}</span>
              <span class="flex-1">{{ line.text }}</span>
            </div>
          </template>
        </div>

        <footer class="h-6 bg-[#FAF8F5] border-t border-black/[0.08] px-3 flex items-center justify-between text-[10px] text-[#71717A] font-mono select-none shrink-0">
          <span>{{ s.diffReport?.lang || 'Go · UTF-8' }}</span>
          <span class="text-emerald-700 font-bold">● 读写工作区磁盘</span>
        </footer>
        </div>
      </section>

    <!-- 未保存文件关闭确认弹窗 (严格遵循铁律 2 与铁律 5：暖色极简、屏幕居中、Esc退出、显式[X]、无原生confirm) -->
    <div
      v-if="s.pendingCloseTab"
      class="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 backdrop-blur-xs"
      @click.self="s.pendingCloseTab = null"
      @keydown.esc="s.pendingCloseTab = null"
    >
      <div class="w-[420px] bg-[#FAF8F5] border border-black/[0.12] rounded-xl shadow-2xl p-5 flex flex-col gap-3 animate-in fade-in zoom-in-95 duration-150">
        <div class="flex items-center justify-between">
          <div class="flex items-center gap-2">
            <span class="text-base">⚠️</span>
            <span class="font-bold text-sm text-[#18181B]">文件未保存修改</span>
          </div>
          <button @click="s.pendingCloseTab = null" class="p-1 rounded-md text-[#71717A] hover:bg-black/[0.05] cursor-pointer" title="关闭 (Esc)">✕</button>
        </div>
        <p class="text-xs text-[#52525B] leading-relaxed">
          文件 <span class="font-mono font-bold text-[#D96B27]">{{ s.pendingCloseTab }}</span> 包含未写入磁盘的编辑内容。关闭标签页将放弃这些修改，是否确定关闭？
        </p>
        <div class="flex items-center justify-end gap-2 pt-2 border-t border-black/[0.06]">
          <button
            @click="s.pendingCloseTab = null"
            class="px-3 py-1.5 rounded-lg text-xs text-[#71717A] hover:bg-black/[0.05] cursor-pointer"
          >
            取消
          </button>
          <button
            @click="s.forceCloseEditorTab(s.pendingCloseTab)"
            class="px-3 py-1.5 rounded-lg bg-rose-600 hover:bg-rose-700 text-white text-xs font-semibold cursor-pointer"
          >
            放弃修改并关闭
          </button>
          <button
            @click="s.saveAndCloseEditorTab(s.pendingCloseTab)"
            class="px-3 py-1.5 rounded-lg bg-[#D96B27] hover:bg-[#C25A1D] text-white text-xs font-semibold cursor-pointer"
          >
            保存并关闭
          </button>
        </div>
      </div>
    </div>
</template>

<script setup lang="ts">
import { useWorkbenchStore } from '../stores/workbench'
import MonacoEditor from './MonacoEditor.vue'
const s = useWorkbenchStore()
</script>
