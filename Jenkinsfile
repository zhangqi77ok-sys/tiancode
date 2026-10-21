// tiancode（伴暖）CI/CD。Jenkins 与应用在同一台练习机上，模式对齐 agent-memory，但没有数据库。
// 凭据（Jenkins Credentials，仓库里只出现 ID）：
//   deploy-ssh            SSH 私钥，登录部署机 deploy 用户（与 agent-memory 共用）
//   ghcr-token            用户名 + PAT（write:packages），部署分支必需（与 agent-memory 共用）
//   tiancode-deploy-env   Secret file，部署机 /opt/tiancode/.env 的内容。不要复用 deploy-env。
// Jenkins 全局环境变量：DEPLOY_HOST（不进仓库）、DEPLOY_USER（默认 deploy）
pipeline {
  agent any
  options {
    buildDiscarder(logRotator(numToKeepStr: '10'))
    timeout(time: 30, unit: 'MINUTES')
    timestamps()
    disableConcurrentBuilds()
  }
  triggers { githubPush(); pollSCM('H/2 * * * *') }
  parameters {
    string(name: 'DEPLOY_BRANCH', defaultValue: 'main', description: '只有这个分支会推镜像、部署、冒烟')
    string(name: 'GHCR_CRED_ID', defaultValue: 'ghcr-token', description: 'GHCR 凭据 ID')
    booleanParam(name: 'FORCE_DEPLOY', defaultValue: false, description: '跳过「同一提交已部署」去重，强制部署并冒烟')
    string(name: 'SMOKE_PATH', defaultValue: '/readyz', description: '冒烟路径；回滚演练时改成不存在的路径')
  }
  environment {
    GHCR_IMAGE = 'ghcr.io/zhangqi77ok-sys/tiancode'
    LOCAL_IMAGE = 'tiancode'
    APP_DIR = '/opt/tiancode'
    COMPOSE_PROJECT = 'tiancode'
  }
  stages {
    stage('Init') {
      steps {
        script {
          env.BR = (env.BRANCH_NAME ?: env.GIT_BRANCH ?: '').replaceFirst(/^origin\//, '')
          env.DO_DEPLOY = (env.BR == (params.DEPLOY_BRANCH ?: 'main')) ? 'true' : 'false'
          env.TAG = "${env.GIT_COMMIT.take(12)}-b${env.BUILD_NUMBER}"
          env.DEPLOY_USER = env.DEPLOY_USER ?: 'deploy'
          env.SMOKE_PATH = params.SMOKE_PATH ?: '/readyz'
          env.GHCR_CRED = params.GHCR_CRED_ID ?: 'ghcr-token'
          env.FORCE_DEPLOY = (params.FORCE_DEPLOY ?: false) ? 'true' : 'false'
          if (env.DO_DEPLOY == 'true' && !env.DEPLOY_HOST) {
            error 'DEPLOY_HOST 未配置（Jenkins 系统设置 → 全局属性 → 环境变量）'
          }
          echo "branch=${env.BR} deploy=${env.DO_DEPLOY} force=${env.FORCE_DEPLOY} tag=${env.TAG}"
        }
      }
    }

    stage('Secrets scan') {
      // Jenkins 跑在容器里，宿主 docker 看不到容器内路径，用 --volumes-from 共享工作目录。
      steps {
        sh 'docker run --rm --volumes-from "$(hostname)" zricethezav/gitleaks:v8.18.4 detect -s "$PWD" --no-banner'
      }
    }

    stage('Lint & Test') {
      steps {
        sh '''
          python3 -m venv .venv && . .venv/bin/activate
          pip install -q -e '.[dev]'
          ruff check backend
          pytest -q --junitxml=report.xml
        '''
      }
      post {
        always { junit testResults: 'report.xml', allowEmptyResults: false }
      }
    }

    stage('Build image') {
      steps {
        sh 'docker build -t $LOCAL_IMAGE:$TAG -t $GHCR_IMAGE:$TAG .'
      }
    }

    stage('Push GHCR') {
      when { expression { env.DO_DEPLOY == 'true' } }
      steps {
        script {
          withCredentials([usernamePassword(credentialsId: env.GHCR_CRED, usernameVariable: 'U', passwordVariable: 'P')]) {
            sh '''
              echo "$P" | docker login ghcr.io -u "$U" --password-stdin
              docker push $GHCR_IMAGE:$TAG
              docker tag $GHCR_IMAGE:$TAG $GHCR_IMAGE:latest
              docker push $GHCR_IMAGE:latest
            '''
          }
          env.DEPLOY_IMAGE = env.GHCR_IMAGE
        }
      }
    }

    stage('Dedup check') {
      // 同一提交已经在跑且 /readyz 为 200 时跳过部署（webhook 与轮询重复触发）。
      when { expression { env.DO_DEPLOY == 'true' && env.FORCE_DEPLOY != 'true' } }
      steps {
        sshagent(['deploy-ssh']) {
          script {
            env.COMMIT12 = env.GIT_COMMIT.take(12)
            def out = sh(returnStdout: true, script: '''
              ssh -o StrictHostKeyChecking=accept-new "$DEPLOY_USER@$DEPLOY_HOST" \
                "APP_DIR='$APP_DIR' COMMIT12='$COMMIT12' bash -s" <<'REMOTE' || echo SSH_FAIL
set -eu
cd "$APP_DIR" 2>/dev/null || { echo NO_DIR; exit 0; }
[ -s .last_tag ] || { echo NO_TAG; exit 0; }
L=$(cat .last_tag)
LT=${L##*:}
[ "${LT%-b*}" = "$COMMIT12" ] || { echo DIFF "$LT"; exit 0; }
P=$(grep ^API_PORT= .env | cut -d= -f2 || true)
c=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:${P:-8092}/readyz")
[ "$c" = 200 ] || { echo UNHEALTHY "readyz=$c"; exit 0; }
echo SAME "$LT"
REMOTE
            ''').trim()
            echo "dedup: ${out}"
            if (out.readLines().any { it.startsWith('SAME ') }) {
              env.SKIP_DEPLOY = 'true'
              echo "already deployed ${out}"
            }
          }
        }
      }
    }

    stage('Deploy') {
      when { expression { env.DO_DEPLOY == 'true' && env.SKIP_DEPLOY != 'true' } }
      steps {
        script {
          withCredentials([
            file(credentialsId: 'tiancode-deploy-env', variable: 'ENVF'),
            usernamePassword(credentialsId: env.GHCR_CRED, usernameVariable: 'GHCR_U', passwordVariable: 'GHCR_P'),
          ]) {
            sshagent(['deploy-ssh']) {
              sh '''
                set -eu
                T="$DEPLOY_USER@$DEPLOY_HOST"
                SSH="ssh -o StrictHostKeyChecking=accept-new"
                $SSH "$T" "mkdir -p $APP_DIR"
                scp -o StrictHostKeyChecking=accept-new -q deploy/docker-compose.yml "$T:$APP_DIR/docker-compose.yml"
                scp -o StrictHostKeyChecking=accept-new -q "$ENVF" "$T:$APP_DIR/.env"
                if [ "$DEPLOY_IMAGE" = "$GHCR_IMAGE" ] && [ -n "${GHCR_P:-}" ]; then
                  echo "$GHCR_P" | $SSH "$T" "docker login ghcr.io -u '$GHCR_U' --password-stdin"
                  $SSH "$T" "docker image inspect $DEPLOY_IMAGE:$TAG >/dev/null 2>&1 || docker pull $DEPLOY_IMAGE:$TAG"
                fi
                $SSH "$T" "cd $APP_DIR && chmod 600 .env && echo '$DEPLOY_IMAGE:$TAG' > .last_tag.new && IMAGE='$DEPLOY_IMAGE' TAG='$TAG' docker compose -p $COMPOSE_PROJECT up -d --remove-orphans"
              '''
            }
          }
        }
      }
    }

    stage('Smoke') {
      when { expression { env.DO_DEPLOY == 'true' && env.SKIP_DEPLOY != 'true' } }
      steps {
        sshagent(['deploy-ssh']) {
          sh '''
            set -eu
            ssh -o StrictHostKeyChecking=accept-new "$DEPLOY_USER@$DEPLOY_HOST" \
              "APP_DIR='$APP_DIR' SMOKE_PATH='$SMOKE_PATH' bash -s" <<'REMOTE'
set -eu
cd "$APP_DIR"
P=$(grep ^API_PORT= .env | cut -d= -f2 || true)
P=${P:-8092}
ok=0
for i in $(seq 1 30); do
  c=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${P}${SMOKE_PATH}")
  echo "smoke api $i $c"
  if [ "$c" = 200 ]; then ok=1; break; fi
  sleep 3
done
[ "$ok" = 1 ]
if [ "$SMOKE_PATH" = /readyz ]; then
  for path in / /admin.html; do
    c=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${P}${path}")
    echo "smoke prototype $path $c"
    [ "$c" = 200 ]
  done
fi
REMOTE
          '''
        }
      }
      post {
        success {
          sshagent(['deploy-ssh']) {
            sh 'ssh -o StrictHostKeyChecking=accept-new "$DEPLOY_USER@$DEPLOY_HOST" "cd $APP_DIR && mv -f .last_tag.new .last_tag && echo current=\\$(cat .last_tag)"'
          }
        }
        failure {
          // 冒烟失败则回到上一次成功的镜像。没有 .last_tag 时只报错，不猜一个 latest。
          sshagent(['deploy-ssh']) {
            sh '''
              ssh -o StrictHostKeyChecking=accept-new "$DEPLOY_USER@$DEPLOY_HOST" \
                "APP_DIR='$APP_DIR' bash -s" <<'REMOTE'
set -eu
cd "$APP_DIR"
rm -f .last_tag.new
if [ ! -s .last_tag ]; then
  echo "ROLLBACK: 没有上一个成功版本，无法回滚"
  exit 1
fi
PREV=$(cat .last_tag)
PI=${PREV%:*}
PT=${PREV##*:}
echo "ROLLBACK to $PREV"
docker image inspect "$PI:$PT" >/dev/null 2>&1 || docker pull "$PI:$PT"
IMAGE="$PI" TAG="$PT" docker compose -p tiancode up -d --remove-orphans
P=$(grep ^API_PORT= .env | cut -d= -f2 || true)
P=${P:-8092}
for i in $(seq 1 30); do
  c=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${P}/readyz")
  if [ "$c" = 200 ]; then echo "ROLLBACK OK readyz=200"; exit 0; fi
  sleep 3
done
exit 1
REMOTE
            '''
          }
        }
      }
    }
  }
}
