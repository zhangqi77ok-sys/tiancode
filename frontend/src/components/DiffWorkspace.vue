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

        <div v-if="s.editorView === 'edit'" class="flex-1 min-h-0 bg-[#1E1C1A]">
          <MonacoEditor
            v-if="s.activeDiffFile"
            v-model="s.editorContent"
            :language="s.activeDiffFile"
            :diagnostics="s.editorDiagnostics"
            :line="s.targetEditorLine"
            @update:modelValue="s.markEditorDirty"
          />
          <div v-else class="h-full flex flex-col items-center justify-center p-8 text-center text-[#71717A]">
            <span class="text-3xl mb-3">✍️</span>
            <p class="text-xs font-semibold text-[#D4D4D8]">未选定编辑文件</p>
            <p class="text-[11px] text-[#A1A1AA] mt-1.5 max-w-sm leading-relaxed">
              可从左侧文件树点击任意源码文件开启 Monaco 实时编辑，支持实时语法高亮与 Ctrl+S 保存。
            </p>
            <div class="flex flex-wrap items-center justify-center gap-2 mt-4 max-w-md">
              <button
                @click="s.activeActivity = 'files'; s.isLeftDrawerOpen = true"
                class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-[#27272A] hover:bg-[#3F3F46] border border-white/10 text-white text-xs font-medium cursor-pointer transition-all shadow-xs"
              >
                <span>📂</span><span>浏览文件树</span>
              </button>
              <button
                @click="s.openCreateFileModal('')"
                class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-[#D96B27]/20 hover:bg-[#D96B27]/30 border border-[#D96B27]/50 text-[#FFA97A] text-xs font-medium cursor-pointer transition-all shadow-xs"
              >
                <span>＋📄</span><span>新建空白文件</span>
              </button>
            </div>
          </div>
        </div>

        <!-- 原生高保真 Monaco Diff 审查引擎 -->
        <div v-else class="flex-1 min-h-0 flex flex-col bg-[#18181B] relative">
          <div v-if="!s.activeDiffFile || !s.diffReport?.lines || s.diffReport.lines.length === 0" class="flex-1 flex flex-col items-center justify-center p-8 text-center text-[#71717A] my-auto">
            <span class="text-3xl mb-3">📄</span>
            <p class="text-xs font-semibold text-[#D4D4D8]">暂无代码差异对比</p>
            <p class="text-[11px] text-[#A1A1AA] mt-1.5 max-w-sm leading-relaxed">
              当前工作区干净，或尚未选定对比文件。可从左侧文件树选择文件进入编辑，或点击 Git 状态审查改动。
            </p>
            <div class="flex flex-wrap items-center justify-center gap-2 mt-4 max-w-md">
              <button
                @click="s.activeActivity = 'files'; s.isLeftDrawerOpen = true"
                class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-[#27272A] hover:bg-[#3F3F46] border border-white/10 text-white text-xs font-medium cursor-pointer transition-all shadow-xs"
              >
                <span>📂</span><span>文件资源管理器</span>
              </button>
              <button
                @click="s.activeActivity = 'files'; s.isLeftDrawerOpen = true; s.explorerTab = 'search'"
                class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-[#27272A] hover:bg-[#3F3F46] border border-white/10 text-white text-xs font-medium cursor-pointer transition-all shadow-xs"
              >
                <span>🔍</span><span>全局代码检索</span>
              </button>
              <button
                @click="s.openCreateFileModal('')"
                class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-[#D96B27]/20 hover:bg-[#D96B27]/30 border border-[#D96B27]/50 text-[#FFA97A] text-xs font-medium cursor-pointer transition-all shadow-xs"
              >
                <span>＋📄</span><span>新建文件</span>
              </button>
            </div>
          </div>

          <template v-else>
            <!-- 顶部轻量 Hunk 分块采纳/丢弃操作带 (如存在 Git Hunk) -->
            <div
              v-if="s.diffReport?.hunks && s.diffReport.hunks.length > 0"
              class="h-8 bg-[#1F1D1A] border-b border-white/[0.08] px-3 flex items-center gap-2 overflow-x-auto no-scrollbar text-[11px] font-mono shrink-0 select-none z-10"
            >
              <span class="text-[#D96B27] font-bold text-[10px] shrink-0">HUNK 块操作:</span>
              <div
                v-for="(hunk, hIdx) in s.diffReport.hunks"
                :key="hIdx"
                class="flex items-center gap-1.5 bg-black/40 px-2 py-0.5 rounded-md border border-white/10 shrink-0 text-[10px]"
              >
                <span class="text-white/80 font-bold">#{{ hIdx + 1 }}</span>
                <span v-if="hunk.add_count > 0" class="text-emerald-400 font-semibold">+{{ hunk.add_count }}</span>
                <span v-if="hunk.del_count > 0" class="text-rose-400 font-semibold">-{{ hunk.del_count }}</span>
                <button
                  @click="s.applyHunkAction(hunk.index, true)"
                  class="text-emerald-400 hover:text-emerald-300 ml-1 px-1 py-0.2 rounded hover:bg-emerald-500/20 cursor-pointer transition-all font-semibold"
                  title="将此块代码改动暂存入 Git Index"
                >✓ 采纳</button>
                <button
                  @click="s.discardHunkAction(hunk.index)"
                  class="text-rose-400 hover:text-rose-300 px-1 py-0.2 rounded hover:bg-rose-500/20 cursor-pointer transition-all font-semibold"
                  title="无损丢弃撤销此块代码改动"
                >✕ 丢弃</button>
              </div>
            </div>

            <!-- Monaco 真实 Diff 视图 -->
            <div class="flex-1 min-h-0">
              <MonacoDiffEditor
                :original="originalDiffText"
                :modified="modifiedDiffText"
                :language="s.activeDiffFile || 'plaintext'"
              />
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
import { computed } from 'vue'
import { useWorkbenchStore } from '../stores/workbench'
import MonacoEditor from './MonacoEditor.vue'
import MonacoDiffEditor from './MonacoDiffEditor.vue'

const s = useWorkbenchStore()

const originalDiffText = computed(() => {
  if (!s.diffReport?.lines) return ''
  return s.diffReport.lines
    .filter(l => l.type === 'ctx' || l.type === 'del')
    .map(l => l.text)
    .join('\n')
})

const modifiedDiffText = computed(() => {
  if (!s.diffReport?.lines) return ''
  return s.diffReport.lines
    .filter(l => l.type === 'ctx' || l.type === 'add')
    .map(l => l.text)
    .join('\n')
})
</script>
