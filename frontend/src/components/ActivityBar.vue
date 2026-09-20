<template>
<nav class="w-[48px] min-w-[48px] bg-[#EFEAE4] border-r border-black/[0.08] flex flex-col justify-between items-center py-2 z-20 shrink-0 select-none">
        <div class="flex flex-col items-center gap-2 w-full">
          <button
            @click="s.switchToChatActivity"
            :class="['relative w-10 h-10 rounded-xl flex items-center justify-center transition-all cursor-pointer', s.activeActivity === 'chat' && s.isLeftDrawerOpen ? 'bg-white shadow-2xs text-[#D96B27]' : 'text-[#71717A] hover:text-[#18181B] hover:bg-white/60']"
            title="会话分支列表 (点击折叠/展开)"
          >
            <svg class="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>
            <span v-if="s.activeActivity === 'chat' && s.isLeftDrawerOpen" class="absolute -left-1 top-2.5 w-1 h-5 bg-[#D96B27] rounded-r-full"></span>
          </button>
          <button
            @click="s.switchToFileActivity"
            :class="['relative w-10 h-10 rounded-xl flex items-center justify-center transition-all cursor-pointer', s.activeActivity === 'files' && s.isLeftDrawerOpen ? 'bg-white shadow-2xs text-[#D96B27]' : 'text-[#71717A] hover:text-[#18181B] hover:bg-white/60']"
            title="工程文件资源管理器 (点击折叠/展开)"
          >
            <svg class="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>
            <span v-if="s.activeActivity === 'files' && s.isLeftDrawerOpen" class="absolute -left-1 top-2.5 w-1 h-5 bg-[#D96B27] rounded-r-full"></span>
          </button>
          <button
            @click="s.switchToGitActivity"
            :class="['relative w-10 h-10 rounded-xl flex items-center justify-center transition-all cursor-pointer', s.activeActivity === 'git' && s.isLeftDrawerOpen ? 'bg-white shadow-2xs text-[#D96B27]' : 'text-[#71717A] hover:text-[#18181B] hover:bg-white/60']"
            title="Git 变更与代码审查 (点击折叠/展开)"
          >
            <svg class="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="18" cy="18" r="3"/><circle cx="6" cy="6" r="3"/><path d="M13 6h3a2 2 0 0 1 2 2v7"/><line x1="6" y1="9" x2="6" y2="21"/></svg>
            <span v-if="s.activeActivity === 'git' && s.isLeftDrawerOpen" class="absolute -left-1 top-2.5 w-1 h-5 bg-[#D96B27] rounded-r-full"></span>
          </button>
          <button
            @click="s.toggleTerminalDrawer()"
            :class="['w-10 h-10 rounded-xl flex items-center justify-center transition-all cursor-pointer font-mono font-bold text-xs', s.isTerminalOpen ? 'bg-white shadow-2xs text-[#D96B27]' : 'text-[#71717A] hover:text-[#18181B] hover:bg-white/60']"
            title="集成终端 (Ctrl+`)"
          >
            <span>$_</span>
          </button>
          <button
            @click="s.openHotplugDashboard()"
            :class="['relative w-10 h-10 rounded-xl flex items-center justify-center transition-all cursor-pointer text-base', s.isHotplugDashboardOpen ? 'bg-white shadow-2xs text-[#D96B27]' : 'text-[#71717A] hover:text-[#18181B] hover:bg-white/60']"
            title="插件热插拔中心与 DSH 算子大盘"
          >
            <span>🧩</span>
            <span v-if="s.isHotplugDashboardOpen" class="absolute -left-1 top-2.5 w-1 h-5 bg-[#D96B27] rounded-r-full"></span>
          </button>
          <button
            @click="s.openArchitectureModal()"
            :class="['relative w-10 h-10 rounded-xl flex items-center justify-center transition-all cursor-pointer text-base', s.isKnowledgeGraphOpen ? 'bg-white shadow-2xs text-[#D96B27]' : 'text-[#71717A] hover:text-[#18181B] hover:bg-white/60']"
            title="代码架构与依赖治理工作板"
          >
            <span>🏛️</span>
            <span v-if="s.isKnowledgeGraphOpen" class="absolute -left-1 top-2.5 w-1 h-5 bg-[#D96B27] rounded-r-full"></span>
          </button>
        </div>

        <div class="flex flex-col items-center gap-2 w-full">
          <button
            @click="s.isSettingsOpen = true"
            class="w-10 h-10 rounded-xl flex items-center justify-center text-[#71717A] hover:text-[#18181B] hover:bg-white/60 transition-all cursor-pointer"
            title="系统设置"
          >
            <svg class="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0-2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>
          </button>
          <div class="w-9 h-9 rounded-full bg-[#D96B27]/15 text-[#D96B27] flex items-center justify-center font-bold text-xs">湉</div>
        </div>
      </nav>
</template>

<script setup lang="ts">
import { useWorkbenchStore } from '../stores/workbench'
const s = useWorkbenchStore()
</script>
