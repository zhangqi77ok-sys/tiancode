#!/bin/bash
# 部署机上的冒烟。默认路径 /readyz 时，再检查页面、匿名管理接口 401、
# 注册后的 /auth/me 200，以及家庭令牌访问管理接口 403。
set -eu
cd "$(dirname "$0")"
P="$(grep ^API_PORT= .env 2>/dev/null | cut -d= -f2 || true)"
P="${P:-8092}"
BASE="http://127.0.0.1:${P}"
PATH_CHECK="${SMOKE_PATH:-/readyz}"

ok=0
for i in $(seq 1 30); do
  c="$(curl -s -o /dev/null -w '%{http_code}' "${BASE}${PATH_CHECK}" || true)"
  echo "smoke api $i $c"
  if [ "$c" = 200 ]; then
    ok=1
    break
  fi
  sleep 3
done
[ "$ok" = 1 ]

if [ "$PATH_CHECK" != /readyz ]; then
  exit 0
fi

for path in / /login.html /admin.html; do
  c="$(curl -s -o /dev/null -w '%{http_code}' "${BASE}${path}" || true)"
  echo "smoke prototype $path $c"
  [ "$c" = 200 ]
done

c="$(curl -s -o /dev/null -w '%{http_code}' "${BASE}/api/v1/admin/ai/config" || true)"
echo "smoke admin-anon $c"
[ "$c" = 401 ]

sec="$(date +%s)"
phone="$(printf '1%010d' $(( (sec + $$) % 10000000000 )))"
payload="$(printf '{"phone":"%s","password":"SmokePass123!","family_name":"冒烟家庭","nickname":"冒烟","birth_date":"1990-01-01"}' "$phone")"
tmp="$(mktemp)"
code="$(curl -s -o "$tmp" -w '%{http_code}' -H 'Content-Type: application/json' -d "$payload" "${BASE}/api/v1/auth/register" || true)"
echo "smoke register $code"
[ "$code" = 201 ]
token="$(sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p' "$tmp")"
rm -f "$tmp"
[ -n "$token" ]

c="$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $token" "${BASE}/api/v1/auth/me" || true)"
echo "smoke me $c"
[ "$c" = 200 ]

c="$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $token" "${BASE}/api/v1/admin/ai/config" || true)"
echo "smoke admin-guardian $c"
[ "$c" = 403 ]

echo "smoke authz ok"
