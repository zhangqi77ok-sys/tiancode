# 50. 嵌套子仓库暂存防崩溃、Git Porcelain 路径清洗、工作区列表去重与采纳健壮性闭环

## ① 知识点与问题背景 (Context & Problem Statement)

在桌面 AI IDE 的 Git 版本管理和 Diff 审查模块中，用户点击“全部采纳 (Accept All)”或“全部放弃 (Revert All)”时触发批量物理暂存 (`git add`) 或回退 (`git checkout / rm`)。

在实际使用中，用户工作区可能包含测试留下的临时目录（如包含 `.git` 文件夹但尚未产生任何 commit 的空仓库，或带尾部斜杠 `/` 的未跟踪目录 `? testrepo/`）。当用户在 Diff 提示条点击“全部采纳”时，系统抛出严重异常 Toast 报错：
```
部分文件采纳失败: testrepo/: git add failed: exit status 128 (output: error: 'testrepo/' does not have a commit checked out error: unable to index file 'testrepo/' fatal: adding files failed )
```
同时引发以下级联故障：
1. **未提交嵌套 Git 仓库引发 Git 128 致命退出**：Git 对带有 `.git` 目录的子文件夹默认识别为 Submodule（Gitlink 模式 `160000`）。若该子仓库刚 `git init` 尚无提交，无法解析出 HEAD Commit SHA-1，执行 `git add -- subrepo/` 必定引发致命退出码 128，导致批量暂存全部中断；
2. **Porcelain 解析未区分普通文件与嵌入式空目录**：`git status --porcelain=v2` 默认输出中，未初始化的子模块以 `? testrepo/` 呈现，且旧解析逻辑未传递根目录路径，盲目将其塞入 `Untracked` 和 `Working` 待确认文件列表；
3. **前端文件列表重复注入与无序膨胀**：旧前端在计算 `workingTreeFiles` 时分别遍历 `working` 与 `untracked`，而后端在处理 `case "?"` 时同时向二者各写入一份，造成每个未追踪项被重复显示 2 次；
4. **Diff 查看器对目录盲目读取引发 I/O 异常**：Monaco Diff 审查流针对路径调用 `ComputeFileDiff`，未校验目录属性直接调用 `os.ReadFile`，导致读取目录时报错。

---

## ② 核心原理与知识内容 (Knowledge Content & Root Cause)

### 1. Git 子模块 (Submodule / Gitlink 160000) 的索引约束
- 在 Git 索引中，子模块被记录为模式 `160000` 的对象条目，该条目必须关联一个 40 位的 Commit Hash。
- 若一个子目录包含 `.git`，但内部是空的（刚 `git init` 且尚未执行过任何 `git commit`），`git -C <subrepo> rev-parse --verify HEAD` 必然失败（`Needed a single revision`）。
- 此时如果在主仓库执行 `git add -- <subrepo>`，Git 试图提取其 HEAD 提交哈希作为 Gitlink 写入暂存区，由于不存在检出的 commit，Git 直接报错 `error: '<subrepo>/' does not have a commit checked out`，并以退出码 128（或 1）终止。

### 2. Git Porcelain v2 的输出规范
- 普通更改：`1 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <path>`
  - 当 `<sub>` 字段为 `S...` 或模式为 `160000` 时，表明属于子模块。
- 未跟踪项：`? <path>`
  - 默认情况下，若目录中存在子仓库，Git 会以 `? dir/`（带尾部斜杠）形式将其暴露为单个候选子模块。
  - 若开启 `-uall`（`--untracked-files=all`），普通子目录会自动展开为所有具体文件的独立条目，唯独嵌入式 Git 仓库仍以 `? dir/` 呈现。

### 3. 路径规范化 (Path Sanitization) 与目录隔离
- 用户界面传入的路径可能包含尾部空白、换行或尾部斜杠（`/`、`\`）。
- 直接执行 `git add -- file.txt/` 在部分平台可能被误识别为目录查询；
- 在进入 Diff 比较器与 Monaco 渲染层前，必须判定 `os.Stat(absPath).IsDir()`，若为目录必须拒绝作为单文件差异进行文本比对。

---

## ③ 标准解决方案与实操步骤 (Actionable Solutions & Step-by-Step Guide)

### 1. 后端 `GitStage` 与 `git_tool` 嵌入式空仓库拦截与尾部斜杠规整
在 `app_vcs.go` 与 `plugins/tool/git/git_tool.go` 中，对所有接收文件路径的操作增加清洗：
```go
filePath = strings.TrimSpace(filePath)
filePath = strings.TrimRight(filePath, "/\\")
if filePath == "" {
    return fmt.Errorf("empty file path")
}

// 针对嵌套 Git 仓库（如子模块或未提交空仓库）的安全防崩溃检测
cleanAbs := filepath.Join(a.workspace, filePath)
if fi, err := os.Stat(cleanAbs); err == nil && fi.IsDir() {
    dotGit := filepath.Join(cleanAbs, ".git")
    if _, dotGitErr := os.Stat(dotGit); dotGitErr == nil {
        headCheck := exec.Command("git", "-C", cleanAbs, "rev-parse", "--verify", "HEAD")
        if attr := windowsSysProcAttr(); attr != nil {
            headCheck.SysProcAttr = attr
        }
        if err := headCheck.Run(); err != nil {
            return fmt.Errorf("nested repository '%s' has no commit checked out", filePath)
        }
    }
}
```

### 2. `GetStatus` 开启 `-uall` 并过滤无 commit 嵌入式仓库
在 `plugins/tool/git/git_tool.go` 中：
```go
rawStatus, err := t.execGit("status", "--porcelain=v2", "-uall")
```
在 `parsePorcelainV2(rawStatus, branch string, rootDir ...string)` 中，过滤掉无有效提交的空仓库目录：
```go
case "?":
    if len(parts) >= 2 {
        filePath := strings.Join(parts[1:], " ")
        if strings.HasSuffix(filePath, "/") && len(rootDir) > 0 && rootDir[0] != "" {
            cleanRel := strings.TrimSuffix(filePath, "/")
            fullPath := filepath.Join(rootDir[0], cleanRel)
            if fi, err := os.Stat(fullPath); err == nil && fi.IsDir() {
                dotGit := filepath.Join(fullPath, ".git")
                if _, dotGitErr := os.Stat(dotGit); dotGitErr == nil {
                    headCheck := exec.Command("git", "-C", fullPath, "rev-parse", "--verify", "HEAD")
                    if headCheck.Run() != nil {
                        continue // 自动跳过无有效提交的嵌套空仓库
                    }
                }
            }
        }
        report.Untracked = append(report.Untracked, filePath)
        report.Working = append(report.Working, GitFileStatus{
            Path:     filePath,
            WorkCode: "U",
        })
    }
```

### 3. Diff 比较器目录熔断保护 (`ComputeFileDiff`)
在 `internal/diff/differ.go` 中，在读取物理文件前增加目录拦截：
```go
if fi, statErr := os.Stat(absPath); statErr == nil && fi.IsDir() {
    return report, fmt.Errorf("[%s] is a directory, not a diffable file", relPath)
}
```

### 4. 前端状态管理去重与过滤保护 (`workbench.ts`)
在 `frontend/src/stores/workbench.ts` 中：
- `workingTreeFiles` 与 `stagedTreeFiles` 引入 `Set<string>` 防止 `working` 和 `untracked` 产生重复文件；
- `pendingDiffFiles` 显式过滤以 `/` 结尾的目录项：
  ```ts
  const pendingDiffFiles = computed(() => [...new Set(workingTreeFiles.value.map(f => f.path.trim()).filter(p => p && !p.endsWith('/')))])
  ```
- `stageAllPendingDiffFilesAction` 与 `revertAllPendingDiffFilesAction` 针对待处理列表做 `new Set()` 去重与空白清洗。

---

## ④ 避坑指南与最佳实践 (Troubleshooting & Best Practices)

1. **Git 命令参数严禁拼接触发模糊匹配**：
   - 永远使用 `git add -- <path>` 与 `git rm -- <path>`（必须带 `--` 分隔符），防止以 `-` 开头的文件名被 Git 解析为命令行参数。
2. **测试用例必须使用独立临时目录**：
   - 编写针对 Git 仓库初始化的测试时，必须使用 `os.MkdirTemp("", ...)` 并在 `defer` 中执行 `os.RemoveAll`，严禁在项目工作区根目录下直接创建临时 `testrepo` 文件夹。
3. **`.gitignore` 防御性配置**：
   - 在 `.gitignore` 中加入 `testrepo*` 与 `testrepo*/`，确保即使有外部脚本在根目录残留测试仓库，也不会污染主工作区版本树。
