import { defineStore } from 'pinia'
import { ref } from 'vue'
import { bridge } from '../wails'
import { useToast } from '../composables/useToast'

// 工作区状态（共享单一事实源）：AppHeader 展示当前路径、SessionList 的
// "打开工作区"按钮与按空间分组的高亮都以这里为准。
export const useWorkspaceStore = defineStore('workspace', () => {
  const path = ref('')
  const { push: toast } = useToast()

  async function refresh() {
    path.value = (await bridge().app.GetWorkspace()) ?? ''
  }

  // 弹系统目录选择框 → 切换工作区；取消/未变化返回 false；失败 toast 可见，不静默
  async function pickAndSet(): Promise<boolean> {
    try {
      const dir = await bridge().app.PickWorkspace()
      if (!dir || dir === path.value) return false
      await bridge().app.SetWorkspace(dir)
      await refresh()
      return true
    } catch (e) {
      toast('error', String(e instanceof Error ? e.message : e))
      return false
    }
  }

  return { path, refresh, pickAndSet }
})
