import { defineStore } from 'pinia'
import { ref } from 'vue'
import { bridge } from '../wails'
import { useToast } from '../composables/useToast'

// 工作区状态（共享单一事实源）：AppHeader 展示当前路径、SessionList 的
// "打开工作区"按钮与按空间分组的高亮都以这里为准。
// 默认无工作区（纯对话）——启动不再自动恢复，由用户显式进入某工作区
//（顶栏菜单 / 侧栏空间组 ＋ / 侧栏"打开"）。
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

  // 直接切入已有工作区（无目录选择器）：用于顶栏菜单与侧栏空间组 ＋ 等快捷入口
  async function setPath(dir: string): Promise<boolean> {
    if (!dir || dir === path.value) return false
    try {
      await bridge().app.SetWorkspace(dir)
      await refresh()
      return true
    } catch (e) {
      toast('error', String(e instanceof Error ? e.message : e))
      return false
    }
  }

  // 退出工作区（纯对话模式）：空串由后端特判为清除——本地文件工具下线，
  // 之后的会话无归属，落侧栏"会话"区
  async function clear(): Promise<boolean> {
    try {
      await bridge().app.SetWorkspace('')
      await refresh()
      return true
    } catch (e) {
      toast('error', String(e instanceof Error ? e.message : e))
      return false
    }
  }

  return { path, refresh, pickAndSet, setPath, clear }
})
