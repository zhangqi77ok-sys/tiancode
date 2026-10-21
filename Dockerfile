FROM python:3.12-slim

WORKDIR /srv
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PORT=8000 \
    PROTOTYPE_DIR=/srv/prototype

LABEL org.opencontainers.image.source="https://github.com/zhangqi77ok-sys/tiancode"

COPY pyproject.toml ./
COPY backend ./backend
COPY prototype ./prototype

RUN pip install --no-cache-dir . \
    && useradd -r app \
    && chown -R app:app /srv/prototype

USER app
EXPOSE 8000

# --root-path 只影响 OpenAPI/Swagger 的对外地址，不把路由挂到该前缀下。
CMD ["sh", "-c", "exec uvicorn app.main:app --host 0.0.0.0 --port ${PORT:-8000} --proxy-headers --forwarded-allow-ips='*' --root-path \"${ROOT_PATH:-}\""]

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD ["python", "-c", "import os,urllib.request; urllib.request.urlopen('http://127.0.0.1:' + os.environ.get('PORT','8000') + '/healthz')"]
