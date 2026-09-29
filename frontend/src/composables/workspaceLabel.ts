// 顶栏工作区标签（0.0.07）：人看到的路必须是**这场对话正在用的路**。
//   - 已落账会话：主标签 = 会话归属（账本首个 workspace 事件，侧栏分组同源）；
//   - 草稿：主标签 = 下一场的根（ws.path——它就是这场草稿发出后的归属）；
//   - 纯对话/无归属：主标签「这场对话没有工作区」。
// ws.path 与会话归属不同时，两者必须叫法不同：主=「这场对话」，副=「新建对话将使用」。
// 短标签至少显示上一级 + 当前名（只给末级时 frontend/dist 这类同名目录无法区分）。

export interface WorkspaceLabel {
  main: string
  mainTitle: string
  sub: string
  subTitle: string
}

export function shortDir(p: string): string {
  const segs = p.split(/[\\/]/).filter(Boolean)
  if (segs.length === 0) return ''
  if (segs.length === 1) return segs[0]
  return `${segs[segs.length - 2]}/${segs[segs.length - 1]}`
}

export function workspaceLabel(opts: {
  isDraft: boolean
  sessionWorkspace: string
  draftWorkspace: string
}): WorkspaceLabel {
  const { isDraft, sessionWorkspace, draftWorkspace } = opts

  if (isDraft) {
    // 草稿：还没有归属；ws.path 就是它发出后将用的根
    if (draftWorkspace) {
      return {
        main: shortDir(draftWorkspace),
        mainTitle: `这场对话发出后将使用：${draftWorkspace}`,
        sub: '',
        subTitle: '',
      }
    }
    return { main: '这场对话没有工作区', mainTitle: '纯对话：文件与命令工具不可用', sub: '', subTitle: '' }
  }

  // 已落账会话
  if (sessionWorkspace) {
    const label: WorkspaceLabel = {
      main: shortDir(sessionWorkspace),
      mainTitle: `这场对话正在使用：${sessionWorkspace}`,
      sub: '',
      subTitle: '',
    }
    if (draftWorkspace && draftWorkspace !== sessionWorkspace) {
      label.sub = `新建对话将使用 ${shortDir(draftWorkspace)}`
      label.subTitle = draftWorkspace
    }
    return label
  }

  // 已落账但无归属（纯对话会话）
  const label: WorkspaceLabel = {
    main: '这场对话没有工作区',
    mainTitle: '这场对话以纯对话开始：文件与命令工具不可用',
    sub: '',
    subTitle: '',
  }
  if (draftWorkspace) {
    label.sub = `新建对话将使用 ${shortDir(draftWorkspace)}`
    label.subTitle = draftWorkspace
  }
  return label
}
