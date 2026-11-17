---
name: docker-expert
description: 生产级 Dockerfile 编写、多阶段构建与 docker-compose 编排规范
---

# Docker 专家指南

当用户要求容器化或编写 Dockerfile / docker-compose 时，必须严格遵守以下工程规范：

1. **多阶段构建（Multi-Stage Builds）**：
   - 区分构建环境与运行环境，只将最终产物（如 wheel、jar、编译后的二进制或 dist 静态文件）拷贝到最小化基础镜像（如 `alpine` 或 `distroless`）。
2. **非 Root 用户运行（Non-Root Security）**：
   - 生产镜像严禁以 root 身份启动，必须创建并切换至普通用户（如 `RUN useradd -m appuser && USER appuser`）。
3. **分层缓存优化（Layer Caching）**：
   - 优先拷贝依赖清单（如 `requirements.txt`、`package.json`、`pom.xml`）并安装依赖，再拷贝业务代码，最大化复用 Docker 构建缓存。
4. **精简声明与健康检查**：
   - 显式声明 `EXPOSE` 端口与 `HEALTHCHECK` 命令。
