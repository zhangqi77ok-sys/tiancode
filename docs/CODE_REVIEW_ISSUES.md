# WarmCompanion (伴暖) — severity-ranked code review

## Phase 1 status

This branch now implements the auth and family-member fixes. The findings below are the original review of `5debeec`; they are not all still open.

| Item | Status |
| :--- | :--- |
| C1 privilege escalation | Fixed. Member create accepts only `elder` / `child`. PIN login ignores any other role. `require_roles` guards guardian and admin routes. |
| C2 fail-open secrets | Fixed. `create_app()` refuses a missing or published `JWT_SECRET` / `ADMIN_PASSWORD`. README no longer prints a live admin password. |
| H3 settings wipe plugin toggles | Fixed. Settings update merges into the existing dict. |
| M1 family plugin off still runs the action | Fixed. `dispatch_action` receives family settings and returns 403. |
| M2 PIN storage, invite oracle, age/role, per-test state | Partially fixed: PIN is hashed, invite codes use `secrets`, bad invite and bad PIN share one 401, age must match role, deleted users fail `get_current_user`, and tests reset the in-memory stores. A 5-failure / 5-minute lockout covers member, guardian, and admin login. |
| H1, H2, H4, H5, M3 coverage gaps beyond the cases above, M4 session revocation, M5 TLS, L1–L4 | Still open. Phase 2: database, real `login.html` API calls, remove fake admin metrics, extend Jenkins smoke past `/readyz`. |

Reviewed tree: `5debeec` (`feat: enforce auth route guard, require user name on registration, and dynamically display name post-login`), 2026-10-21. The original pass did not change application code. Phase 1 did.

Method: read `README.md`, `docs/PRD_FAMILY_COMPANION.md`, `docs/deploy.md`, `Jenkinsfile`, `Dockerfile`, `pyproject.toml`, the deploy templates, every file under `backend/`, and `prototype/login.html`, `index.html`, and `admin.html`. Then, from a clean import of this tree:

- `python3 -m pytest -q` → **15 passed**
- `python3 -m ruff check backend` → **all checks passed**
- A `TestClient(create_app())` script checked register, member role, admin routes, the README demo accounts, the login-page token format, settings updates, and plugin actions. “Reproduced” below means that script.

`docs/deploy.md` is the most accurate document in the repo. The README and the PRD are not.

---

## 1. Executive summary

WarmCompanion is a small FastAPI process plus three static HTML pages. `backend/app/main.py` mounts health checks, the `/api/v1` routers, and `prototype/` as static files. Docker, a Jenkins pipeline, and an nginx snippet exist, and they deploy that process to `127.0.0.1:8092` behind `/tiancode/`. `docs/deploy.md` says plainly that storage is in-memory and there is no database.

The API that process actually implements is: guardian register/login, invite+PIN member login, a family member list filtered by `family_id`, two care flags, six canned plugins, an in-process kill-switch, and an admin config object. PyJWT HS256 with an algorithm allow-list is real. So is PBKDF2 for guardian passwords. Nothing calls a model, TTS, ASR, or a safety filter. Plugin “memory” and medication check-ins live in process dicts and disappear on restart, which every Jenkins deploy does.

The pages do not call this API. `prototype/login.html` accepts any non-empty guardian password and the preset PINs, writes `localStorage.satoken` as the string `satoken_` plus base64 JSON, and redirects. That value is not a JWT. `GET /api/v1/auth/me` with it returned **401**. `prototype/index.html` only checks that the localStorage keys exist, then renders a hardcoded elder/child/family layout. `prototype/admin.html` shows 12 families, 2,480 dialogues, and a fake audit log, and toasts success without a request.

Two security facts dominate a portfolio read. Any registered guardian can `POST /api/v1/family/members` with `"role": "admin"` and exchange the PIN for a token that `require_admin` accepts, including via the `satoken` header. Separately, if `JWT_SECRET` and `ADMIN_PASSWORD` are unset, the process uses literals that are in git, and the README prints `admin` / `AdminPassword123!` as the admin login. The Jenkins path can override them with `tiancode-deploy-env`. The documented local command (`uvicorn app.main:app` with no env) does not.

The pytest suite is not stubbed. Fifteen tests hit real routers and they pass, including `/healthz` and “static files must not swallow `/api`”. They never create an admin member, never open a second family, and never combine settings-save with a plugin toggle. The README badge “Tests 11/11 Passing” is a static shields.io image, not a Jenkins status, and the count is wrong.

---

## 2. Issues, ranked

### Critical

#### C1. Any guardian can mint a platform-admin token

**Evidence**

- `MemberAddRequest.role` is the full `UserRole` enum, including `admin` and `guardian` (`backend/app/schemas/auth.py`). The description says elder or child. There is no validator.
- `add_family_member` checks only that the caller is a guardian, then stores `req.role.value` and a PIN (`backend/app/api/v1/auth.py`).
- `member_quick_login` copies that role into the JWT.
- `require_admin` trusts `claims["role"]` and does not look the user up (`backend/app/api/deps.py`). `get_current_user` accepts the same token in `Authorization: Bearer` or in `satoken`.

**Reproduced** against `create_app()`: register, `POST /api/v1/family/members` with `"role": "admin"` and a PIN (201, role `admin`), `POST /api/v1/auth/member-login` (200, role `admin`), `GET /api/v1/admin/ai/config` with header `satoken: <that token>` (200). The same create path accepts `"role": "guardian"`.

**Why it matters.** Register is public. This is the admin boundary the PRD (section 8) and the README (“仅平台超级管理员”) describe. `backend/tests/test_admin_api.py` only shows that a normal guardian token gets 403. It never tries to create an admin member. On the Jenkins topology in `docs/deploy.md`, Swagger is on the public prefix (`/tiancode/docs`), so this is not a local-only bug.

**Fix direction.** Restrict `MemberAddRequest.role` to `{elder, child}` and reject anything else in the route. Issue admin tokens only from `admin_login`, with a claim member-login cannot set. Add a test that `role=admin` on member create is 422 and that the resulting token cannot call `/api/v1/admin/*`.

#### C2. Missing env vars fall open to a password and signing key the README publishes

**Evidence**

- `JWT_SECRET = os.getenv("JWT_SECRET", "tiancode-family-secret-32-bytes-min!")` in `backend/app/api/deps.py`.
- `ADMIN_USERNAME` / `ADMIN_PASSWORD` default to `admin` / `AdminPassword123!` in `backend/app/api/v1/admin.py`. Comparison is `!=`.
- `backend/tests/test_admin_api.py` logs in with that password.
- README section “体验测试预设凭据” prints the same admin password, plus `13800138000` / `Password123!`, invite `HAPPY8`, PINs `8888` and `6666`.
- `deploy/.env.example` and `docs/deploy.md` say to replace `JWT_SECRET` and `ADMIN_PASSWORD` and to keep the real file in Jenkins credential `tiancode-deploy-env`. That override exists only when the variable is set. `README` “后端服务运行与测试验证” starts uvicorn with no env. The image `CMD` does not set a secret (`Dockerfile`).
- `AiConfigManager` still initializes `_api_key` to `sk-demo-antigravity-warmcompanion-key` (`backend/app/core/ai_config.py`). `prototype/admin.html` hardcodes a different value, `sk-antigravity-demo-production-key`, and the hint says the key is stored encrypted and salted.

**Reproduced:** `POST /api/v1/admin/login` with `admin` / `AdminPassword123!` returned 200. `POST /api/v1/auth/login` with the README guardian phone returned **401**. `POST /api/v1/auth/member-login` with `HAPPY8` / `8888` returned **404**. Nothing in `create_app()` seeds those accounts. Invite codes are random (`_generate_invite_code`).

**Why it matters.** The deploy doc and the README disagree, and the code agrees with the README whenever the env file is absent. Anyone who can read the repo can forge a 30-day JWT for any `sub` / `role` on a process still using the default secret, which is the quick-start path. The family rows in the credential table do not work against the API at all. They only match the presets inside `login.html`, which does not check them against the server (H5).

**Fix direction.** Refuse to start when `JWT_SECRET` or `ADMIN_PASSWORD` is unset or equal to the old literal. Delete those literals. Stop printing a working admin password in the README; point local dev at `deploy/.env.example`. Hash the admin password. Remove the LLM key literals. If demo families are part of the pitch, seed them in one place and test that seed, or delete the table.

---

### High

#### H1. Jenkins is real, and its smoke test cannot see a broken product

**Evidence**

- `Jenkinsfile` stages: gitleaks, `pip install -e '.[dev]'`, `ruff check backend`, `pytest`, `docker build`, GHCR push, SSH deploy, smoke. `docs/deploy.md` matches that file. This review did not re-run gitleaks or a remote deploy.
- Smoke curls `127.0.0.1:$API_PORT/readyz` (and, for the default path, `/` and `/admin.html`) and requires HTTP 200. `/readyz` is “plugin registry is non-empty” (`backend/app/main.py`). `/` and `/admin.html` are the static prototype (`test_routes` in `backend/tests/test_main_health.py` only checks status and a substring).
- `deploy/docker-compose.yml` and `docs/deploy.md` both say there is no database. A new container starts empty. Rollback restores another empty process plus whatever image was tagged.
- The README badge is `https://img.shields.io/badge/Tests-11%2F11%20Passing-success.svg`. It is not a Jenkins or GitHub status badge. This tree has **15** passing tests, not 11.

**Why it matters.** A green Jenkins run means “the container listens and the HTML files are on disk.” It does not mean login, RBAC, plugins, or the admin kill-switch work, and it cannot go red when C1 is present, because pytest does not cover C1. The badge will stay green if the suite starts failing. For a portfolio, “CI/CD with smoke and rollback” over-reads what the smoke asserts.

**Fix direction.** Keep the pipeline. Add a smoke step that registers a user, hits `/api/v1/auth/me`, and confirms an elder token cannot call `/api/v1/admin/ai/config`. Point the README badge at the real job, or delete it. Say in the README, as `docs/deploy.md` already does, that a deploy drops every family.

#### H2. Family data is process memory, and a valid JWT does not require a live user

**Evidence**

- `_FAMILIES`, `_USERS`, `_PHONE_TO_USER`, `_INVITE_TO_FAMILY` in `backend/app/api/v1/auth.py`. The comment still says this can be “直接升级 SQLite/PG”. There is no database driver in `pyproject.toml`.
- Plugin state is more dicts on the singletons: `MemoryKeeperPlugin._user_memories`, `MedicationPlugin._checkin_status`, `OperaPlugin._current_indices`, `InteractiveStoryPlugin._user_story_state`, `HabitChallengePlugin._user_stars`. Keys are `user_id`, not `family_id`. The memory action tells the user the story was “永久珍藏”.
- `get_current_user` never loads `sub` from `_USERS`. `encode_jwt` defaults to 30 days (`DEFAULT_EXPIRES_IN` in `backend/app/core/security.py`).
- `plugin_registry` and `ai_config_manager` are process singletons. A kill-switch or prompt edit does not survive a restart and is not shared by a second worker. Compose runs one container, so the second case is latent.
- No scheduler. Medication time is the literal `"08:30"`. `GET /ws` is 404. There is no TTS route.

**Reproduced:** deleting the elder from `_USERS` left `GET /api/v1/auth/me` at 200. A story action left `ai_config_manager.dialogue_count` at 0.

**What the tenant filter does.** `list_family_members` keeps `user["family_id"] == jwt.fam`. That filter was checked earlier on this codebase with two registered families and held. It was not re-broken by `5debeec`. Memories and check-ins are not family rows, and no route returns them to the guardian. Cross-family reads of plugin dicts were not demonstrated.

**Why it matters.** README “人生时光回忆册…永久沉淀” and the PRD diagram (SQLite/Postgres, long-term memory, `family_id` as the boundary for memories and schedules) describe a store this process does not have. `docs/deploy.md` already admits the wipe. The README does not, so the public description and the deploy description cannot both be shown to a reviewer.

**Fix direction.** Either add one database and load the user on every request, or change the README/PRD to the sentence already in `docs/deploy.md`. Scope plugin rows by `(family_id, user_id)` before calling them family history. Shorten the 30-day token once revocation exists.

#### H3. Saving care settings deletes plugin toggles, and the care flags are never read

**Evidence**

- `toggle_family_plugin` writes `family["settings"]["plugins"]` (`backend/app/api/v1/plugins.py`).
- `update_family_settings` replaces the dict: `fam["settings"] = req.model_dump()` (`backend/app/api/v1/auth.py`). `FamilySettingsUpdate` only has `medication_reminder_enabled` and `kids_curfew_enabled`.
- Those two names appear in the schema, the register default, the settings route, and `test_auth_api.py`. No plugin reads them. `MedicationPlugin.render_card` ignores `family_state`. Nothing implements 21:00.

**Reproduced:** after toggling `elder_opera` off, `PUT /api/v1/family/settings` left `settings` as only the two booleans. The `plugins` key was gone.

**Why it matters.** These two guardian routes are the README’s “家庭插件功能箱” and “防沉迷” controls. Used in that order, the settings call undoes the toggle. The tests call them in separate files, so both stay green. The curfew and medication switches do not change plugin behavior.

**Fix direction.** Merge into the existing settings dict, or store toggles elsewhere. Read the flags in the medication and story plugins, or remove the fields. One test should toggle, save settings, and assert the toggle is still off.

#### H4. Model, voice, and safety behavior is configuration plus canned text

**Evidence**

- `AiConfigManager` (`backend/app/core/ai_config.py`) stores provider, prompts, Edge-TTS voice names, rates, and `safety_filter_enabled`. `record_dialogue` and `record_safety_alert` have no callers. No module imports an LLM or TTS client. `pyproject.toml` dependencies are FastAPI, uvicorn, Pydantic, and PyJWT.
- Plugins return fixed copy. `InteractiveStoryPlugin` has two hardcoded act-2 scenes. `WonderEncyclopediaPlugin` returns the rainbow sentence and `listen_audio_url: "#audio-rainbow"`. `ask_question` echoes the child text and a fixed reply. `MedicationPlugin` always shows “清晨降压温水药” and “已同步给女儿小敏”.
- `GET /api/v1/admin/dashboard/stats` reports those dead counters. The schema text says “今日”. They stay 0.
- `prototype/admin.html` does not call the API. It ships `stat-families=12`, `stat-dialogues=2,480`, “Uptime 99.98%”, “DeepSeek / Qwen … 低延迟链路畅通”, “Edge-TTS … 双音色就绪”, and three audit lines. `saveAiConfig`, `refreshData`, and `toggleKillswitch` only mutate the page or show a toast.

**Why it matters.** The README feature list (DeepSeek/Qwen hot switch, Edge-TTS, kill-switch “全网”, safety audit) is what a hiring reader will grade. The kill-switch flag inside `PluginRegistry.dispatch_action` is real for one process. The admin page’s switch is not that flag. The safety dashboard cannot move.

**Fix direction.** Delete provider, TTS, and safety fields until a client calls them, or add one client and increment the counter only from that client. Remove the invented metrics from `admin.html`, or label the page as a static mock in the UI. Do not describe an echoed child question as a reviewed answer.

#### H5. The new login portal does not talk to the API, and the home page treats that as a session

**Evidence**

- `prototype/login.html` `submitMemberLogin`, `submitGuardianLogin`, and `submitGuardianRegister` never call `fetch`. Any non-empty guardian phone and password succeed. PIN `6666` becomes 豆豆; every other PIN becomes 外婆. The token is `"satoken_" + btoa(...)`.
- **Reproduced:** that token on `GET /api/v1/auth/me` as `satoken` returned 401.
- `prototype/index.html` lines 7–17 redirect to `login.html` when `localStorage.satoken` or `satoken_user` is missing. Any non-empty string passes. The layout role is `getModeByAge(calcAge(birth))`, not `user.role`. The API role value is `child`; the page’s branch is `role === 'kids'`. The stored role is not what picks the view.
- `submitPinLogin` in the same file still defaults an empty invite to `HAPPY8` and an empty PIN to `8888`, treats any PIN starting with `8` as elder, writes a different key (`warm_jwt_token`) shaped like `ey.header.<b64>.signature`, and `alert`s that a JWT was issued.
- `toggleVoice` waits 2.6s and inserts a fixed sentence. It does not open a microphone. `speakContent` is `speechSynthesis` at rate 0.85 for elder mode. That is the only TTS in the repo.
- `sendFamilyMemo` and `callDaughter` are `alert`s. The family bento is static (“5星”, “对讲 18 分钟”). `toggleFamilyPluginPrototype` sets `display` on DOM nodes. There is no `fetch(` in `prototype/`.
- Copy disagrees with the plugins: 女驸马 performer is 严凤英 on the page and 韩再芬 in `OPERA_PLAYLIST`. The page’s medication list is not “清晨降压温水药” at 08:30. Story text differs. The family plugin box omits `elder_medication` and `child_encyclopedia`.
- `login.html` links to `admin.html` with no credential. The admin footer already says “Root Admin / 全局超级权限”.

**Why it matters.** README “快速上手” says opening `login.html` is guardian registration and PIN login, that success “激活 Sa-Token 协议 30 天持久免登会话”, and that cards come from `/api/v1/plugins/active`. The header check added in `5debeec` only proves the browser kept a string. The API rejects that string. A reviewer who follows the README against the running server will fail the demo accounts (C2) and, if they inspect the token, will see it is not a JWT.

**Fix direction.** One client. If these files are mocks, say so in the README and stop emitting “JWT issued” / “Sa-Token 会话”. If they are the product UI, `fetch` the real routes, store the JWT the server returns, and render `GET /plugins/active`. Use one role vocabulary (`child`, not `kids` vs `child`).

---

### Medium

#### M1. Turning a plugin off for a family hides the card and still runs the action

**Evidence.** `PluginRegistry.get_active_cards` skips a plugin when the family map says false (`backend/app/plugins/registry.py`). `dispatch_action` checks existence, the global flag, and `can_access`. It never sees family settings. `POST /plugins/{id}/action` therefore cannot enforce the family toggle (`backend/app/api/v1/plugins.py`).

**Reproduced:** with `elder_opera` off, the elder card list omitted it, and `next_track` still returned 200.

The global kill-switch does block the action and is what `test_admin_api.py` covers. `test_plugins_api.py` only asserts the card is gone. The README’s “家庭级按需启停” and “平台级全局一键熔断” are not the same control. Only the second one rejects the call, and only inside the process that flipped it (H2).

**Fix direction.** Pass the family plugin map into `dispatch_action` and return 403 when the family flag is off. Assert that in `test_plugins_api.py`.

#### M2. PIN and password handling do not match section 6 of the PRD

**Evidence**

- PINs are stored and returned in plaintext to guardians (`list_family_members`).
- Compare is `==`. Invite codes and PINs come from `random.choices`, not `secrets`.
- `GET /api/v1/auth/me` returns `invite_code` to every role. **Reproduced:** a child token received the invite code. A 4-digit PIN and no lockout makes every other member’s login an online guess, including the elder health card the PRD says a child must not open.
- Duplicate PINs are last-writer-wins by dict order (first match). Wrong invite is 404 and wrong PIN is 401, and `test_auth_api.py` locks that in.
- `hash_password` builds the salt from `sha256(str(time.time()))` (`backend/app/core/security.py`), not `secrets.token_bytes`. `verify_password` does use `hmac.compare_digest`. Iterations are 100_000, as advertised.
- `RegisterRequest.phone` is length 11 only. **Reproduced:** `abcdefghijk` registered with 201. Birth dates are not validated. **Reproduced:** birth `2020-05-05` with `role: "elder"` returned age 6. PRD section 3.0’s age lock is client-side only (`getModeByAge` in `index.html`).

**Why it matters.** The public gateway in `docs/deploy.md` makes the missing lockout a remote property, not a notebook property. The JWT library change did not touch this path.

**Fix direction.** `secrets` for invite and PIN, store a hash, rate-limit `member-login`, use one error for bad invite and bad PIN, reject duplicate PINs, stop returning the invite code to elder and child, validate phone digits and `YYYY-MM-DD`, and enforce age bands on the server if they are a product rule. Replace the time-derived salt.

#### M3. The tests pass, and they do not cover the failures above

**Evidence**

- `pytest -q`: **15 passed**. `ruff check backend`: clean. Assertions check status codes and bodies. They are not `assert True`.
- New in this tree and worth keeping: `backend/tests/test_main_health.py` (health, prototype served, `/api` not shadowed, `/tiancode/readyz` is 404 because nginx strips the prefix), `test_satoken_header_support`, `test_get_family_by_id_annotation_resolves`, and the 30-day `exp - iat` check.
- API tests each build a `FastAPI` or call `create_app()`, but they share `_USERS`, `_FAMILIES`, `plugin_registry`, and `ai_config_manager`. Nothing truncates them. Phones differ across files, so they do not collide today. `test_admin_api.py` turns the global kill-switch off and on inside one function. A failure before the restore leaves `child_story` off for the next test in the same process.
- No second family. No `role=admin` member. No settings-save after a toggle. No assertion on `dialogue_count`. `test_auth_api.py` uses `child age <= 10` for birth `2018-09-01`, which is false from 2028-09-01 onward.
- Docstrings still say “严格遵循 TDD” and “E2E”. There is no second process and no browser. The login page is untested.

**Why it matters.** A green `pytest` line in Jenkins will be read as “RBAC is tested.” It tests the happy path of an honest token. The static 11/11 badge makes that claim even when the number of tests changes.

**Fix direction.** Autouse fixture that clears the four dicts, registry flags, and the AI singleton. Add the C1, H3, and M1 cases, plus one two-family list test. Freeze the clock for the age assert. Delete the handmade badge or generate it from pytest’s count.

#### M4. “Sa-Token” and “三维 RBAC” are a second header and a few comparisons

**Evidence**

- `require_roles` is unused. Its inner function is `role_checker(u: UserContext = None)` with no `Depends(get_current_user)` (`backend/app/api/deps.py`).
- Guardian checks are inline `if u.role != UserRole.GUARDIAN` on member create, settings, and the two family-plugin routes. Admin checks go through `require_admin`. That split is how C1 exists only on the admin side.
- There is no refresh, logout, `jti`, or denylist. `logoutAndRedirect` in `index.html` only deletes localStorage. The server cannot revoke the 30-day token.
- PyJWT is called with `algorithms=["HS256"]` (`decode_jwt`). A token with `alg: none` does not verify. That part of the old hand-rolled decoder is in better shape. What is absent is issuer, audience, key id, and any difference between an admin token and a member token besides the `role` string.
- The README mermaid says “RFC 7519 HS256 JWT 原生令牌 (零三方依赖)”. The next section says “工业级 PyJWT”. `pyproject.toml` depends on `pyjwt`. The directory comment for `security.py` still says “原生零外部依赖”. `TokenResponse.expires_in` is a schema default of 2592000, not a value read back from the token.
- PRD 7.1 says the guardian enables a plugin “针对特定长辈或孩子”. The flag is family-wide.

**Why it matters.** Sa-Token, as a product, is a server-side session stack. Here the name is a header alias for the same JWT, and the browser copy of that header is not a JWT (H5). A reviewer who knows the library will treat the README as name-dropping. The permission model is four role strings.

**Fix direction.** Use one `require_roles` that depends on `get_current_user` for every mutating route. Keep admin issuance on its own path (C1). Describe the header as “JWT also accepted in `satoken`” until there is a real server session. Drop “零三方依赖”.

#### M5. The public deploy doc is HTTP, and the container trusts every forwarded hop

**Evidence**

- `docs/deploy.md` publishes `http://<HOST>/tiancode/` for the app, admin page, and Swagger. The nginx snippet sets `X-Forwarded-Proto $scheme` and does not contain a TLS listener. TLS may live in a server block that is not in the repo; the doc a reader has is HTTP.
- `Dockerfile` `CMD` passes `--proxy-headers --forwarded-allow-ips='*'`.
- Compose binds `127.0.0.1:${API_PORT:-8092}:8000`, so the container is not on the public interface by itself. The `*` still means any client who can reach that port can set `X-Forwarded-*`.

**Why it matters.** The token lifetime is 30 days (H2) and the admin password fallback is public (C2). An HTTP gateway plus a long bearer token is a stolen-token problem the moment the host is shared. This is a practice-machine layout, and `docs/deploy.md` is honest about the path. It is still the wrong default to describe as the production story in the README.

**Fix direction.** Document TLS in front of nginx, or say the host is HTTP-only and local. Restrict `forwarded-allow-ips` to the nginx hop. Do not treat this as a substitute for C1 and C2.

---

### Low

#### L1. README contradicts itself and the license file is missing

**Evidence.** The mermaid says zero third-party JWT; the following section requires PyJWT. The tree comment on `security.py` says zero dependencies; the file imports `jwt`. The feature list says the UI is driven by `/api/v1/plugins/active`; the prototype has no `fetch`. The badge says 11/11; pytest reports 15. The closing section says MIT and links to `LICENSE`. There is no `LICENSE` file.

**Why it matters.** These are checkable in one sitting. They are the difference between a README that scopes a demo and one a reviewer will not trust.

**Fix direction.** One description, aligned with `docs/deploy.md`. Add the license file or remove the claim. Fix the badge.

#### L2. Accessibility numbers in the README are not the CSS

**Evidence.** README and PRD 3.1.1 require 20px+ type, line-height 1.6, WCAG AAA, contrast at least 7:1, and a 64px hit target. Elder mode sets `--font-scale: 1.2`. Chat text is `calc(15px * var(--font-scale))` (18px) at line-height 1.5 (`prototype/index.html`). The 1.6 line-height in that file is on `.rec-features-list` at 12px. `.sos-btn` is 56px tall, `.voice-main-btn` is 58px, `.memory-record-btn` is 48px. This review did not measure contrast, so it does not claim a measured AAA failure. It claims the written spec is not what the stylesheet implements, and the repo has no contrast or hit-target check. PRD section 5’s Vue, Tailwind, and PWA service worker are also absent: one HTML file, no manifest, no service worker.

**Why it matters.** “WCAG AAA” and “64px” are specific. Devtools will not match them.

**Fix direction.** Change the CSS to the spec and add a check, or delete the AAA / PWA / Vue sentences.

#### L3. Leftover contract nits

**Evidence**

- `CardTemplateType.BENTO_STAT` is unused (`backend/app/schemas/plugin.py`). `PluginRegistry.unregister` and `require_roles` are unused. `record_dialogue` is unused (H4).
- `load_builtin_plugins()` runs at import in `admin.py` and `plugins.py`, and again in `create_app()`. Registration is idempotent. Tests cannot build an empty registry without touching the singleton (M3).
- `.gitignore` ignores `*.png` and `*.zip`, so a screenshot or release zip will not be added by a normal `git add`. `.pytest_cache/` is ignored now.

**Why it matters.** Small, but they sit under a README that says the plugin contract is what the UI renders.

**Fix direction.** Delete unused symbols or use them. Construct the registry in `create_app` only. Narrow the gitignore.

#### L4. The page and the plugins already disagree on content

**Evidence.** Same ids (`elder_opera`, `elder_memory`, `child_story`, `child_habit`) on both sides, different performer, lyrics, drug name, clock time, and story text. See H5.

**Why it matters.** Wiring `fetch` later will not line up with the HTML that is already on screen.

**Fix direction.** Render cards from `GET /plugins/active`, or one JSON fixture both sides load.

---

## 3. Top 5 to fix first (portfolio bar)

1. **C1 and C2, with tests.** Unrestricted `role`, plus a signing key and admin password that the quick start actually uses, are the items a backend interview will stop on. The Jenkins env override does not fix the fallback or the README table.
2. **Make `login.html` either a labeled mock or a client (H5).** Today it advertises a 30-day Sa-Token session and writes a token the API rejects. That is the first thing a reviewer will click.
3. **Make the README match `docs/deploy.md` on storage (H2, H1).** Say that a restart drops every family, or add a database and bind tokens to rows. Extend Jenkins smoke past `/readyz` and the HTML files.
4. **Fix the two plugin-state bugs (H3, M1).** Settings save must not delete toggles. A family “off” must reject `POST /plugins/{id}/action`. Both are smaller than a new feature and both are currently green in CI.
5. **Replace the three long API tests with isolated cases (M3).** Clear singletons. Two families. Admin-role rejection. Family-disable blocks the action. Drop the 11/11 image. Keep `test_main_health.py` and the JWT unit tests; they are the ones that match the code.

---

## 4. Claims that are overstated or unimplemented

“Real” means this tree does the behavior, not that it is safe to ship. “Partial” means a slice exists and the sentence around it does not.

### README.md (current text, not the old prototype-only README)

| Claim | Verdict |
| :--- | :--- |
| FastAPI backend + mobile web, birth date locks a single identity | Partial. The server ignores age when assigning role (a 6-year-old `elder` was accepted). The page locks layout from the birth date in localStorage, after a fake login. |
| WCAG AAA, 20px+, line-height 1.6, 64px voice target, 0.85× TTS | Partial. `speechSynthesis` rate 0.85 is real. The size and AAA sentences do not match the CSS (L2). |
| Opera playback, permanent oral history, medication sync to the daughter, emergency call | Partial metadata and canned strings. No audio files. “永久珍藏” and “已同步” do not persist or notify. The call button is an `alert`. |
| AI branching story, encyclopedia, 21:00 anti-addiction, content safety | Unimplemented as stated. Two fixed scenes. Curfew flag is unread. No safety filter. |
| Live family bento, remote voice memos, guardian plugin box driving the member UI | Unimplemented on the page (static HTML). The API toggle exists and is then discarded by settings save (H3) and not enforced on the action (M1). |
| Admin census, DeepSeek/Qwen/OpenAI hot switch, Edge-TTS, live fraud audit | Unimplemented. Enum and voice strings are stored. Admin HTML numbers and audit lines are hardcoded. Dialogue and alert counters stay 0. |
| Global kill-switch takes the card down for every family and blocks the call | Partial, one process only (`PluginRegistry`). The admin page switch does not call it. Not durable across deploy. |
| “零三方依赖” HS256 **and** “工业级 PyJWT” | The second is real (`pyjwt` in `pyproject.toml`, `algorithms=["HS256"]`). The first is false. |
| Sa-Token dual header and a 30-day session | Partial. The API accepts a JWT in `satoken` or `Authorization` and sets `exp` 30 days out. There is no server session. The login page’s `satoken_` token is rejected (401). |
| UI loads cards from `/api/v1/plugins/active` | False. No `fetch` in `prototype/`. |
| Tests 11/11 Passing | False as a count (15 passed here) and false as a CI badge (static image). The suite itself does run. |
| Demo logins: admin password, `13800138000`, `HAPPY8` / `8888` / `6666` | Only the admin password works, and only when `ADMIN_PASSWORD` is unset. The family rows are 401 and 404 against `create_app()`. |
| MIT license | No `LICENSE` file. |
| Directory tree, `main.py`, Docker, Jenkins, `docs/deploy.md` | Real. This part of the README matches the tree. |

### PRD sections 2 and 5 (unchanged file)

| Claim | Verdict |
| :--- | :--- |
| WebSocket audio gateway | Absent. `GET /ws` is 404. |
| Elder, child, and family agents | Two prompt strings, never sent. No family agent. |
| LangChain / LiteLLM, DeepSeek-V3, Qwen | Enum only. |
| Edge-TTS, Whisper, CosyVoice | Absent. Browser `speechSynthesis` only. |
| Whole-path safety filter | Boolean stored, never read. |
| SQLite now, Postgres later | Absent. `docs/deploy.md` is the correction. |
| PWA, service worker, Vue 3, Tailwind | Absent. |

### PRD section 3 (product behavior)

| Claim | Verdict |
| :--- | :--- |
| Server rebuilds the UI from birth date and hides the other persona | Client only, and only after the localStorage gate. All three views are in `index.html`. |
| Memory cards the family can reopen | Process dict keyed by `user_id`. No guardian read API. |
| Opera with synced sung lyrics | Titles and lyric strings. No audio. Page and API disagree on the singer. |
| Medication check-in synced to the daughter | One hardcoded drug. No sync. |
| Emergency call | `alert`. |
| Generated story, habit stars, encyclopedia | Fixed copy and local state. |
| 30-second bento from real check-ins | Static HTML. `BENTO_STAT` is unused. |
| Remote memo and 21:00 curfew | `alert`, and an unread boolean. |

### PRD sections 6–8 (tenancy, RBAC, plugins, admin)

| Claim | Verdict |
| :--- | :--- |
| `family_id` isolates memories, medication, reports | Partial. The member list is filtered by `family_id`. Memories and check-ins are not. |
| Cross-role plugin calls return 403 | Real for an honest JWT. `test_plugins_api.py` covers elder → `child_story`. C1 forges the role that this check trusts. |
| Invite + PIN as a safe passwordless design | The exchange works. Lockout, hashing, and age binding do not (M2). |
| Stdlib JWT, constant-time signature compare, PBKDF2 × 100,000 | PBKDF2 and `compare_digest` on passwords are real. JWT is now PyJWT, not the stdlib implementation the PRD still describes. The password salt is not a CSPRNG. Admin and PIN secrets are not hashed. |
| Plugin auto-discovery | Hardcoded list in `load_builtin_plugins`. |
| `GET /plugins/active` drives the UI | Route exists. UI does not call it. |
| Per-member plugin toggle | Family-wide flag only, and it does not block the action (M1). |
| Admin login, masked model config, prompt update, plugin list, dashboard | Routes exist and edit process memory. The masked key is a prefix plus stars. Dialogue and safety stats stay 0. |
| Live fraud and content audit | Absent. The admin page invents the events. |

### Claims that match the code, if they are worded this narrowly

- `uvicorn app.main:app` serves `/healthz`, `/readyz`, the prototype, and `/api/v1`, and `/tiancode/readyz` is intentionally 404 on the app because nginx strips the prefix (`test_main_health.py`).
- A guardian can register, log in, add an elder and a child, and those members can log in with invite + PIN. Passwords are PBKDF2. JWTs are HS256 via PyJWT, 30-day `exp`, rejected when expired, tampered, or signed with the wrong secret.
- A normal guardian token receives 403 on `/api/v1/admin/*`. An elder token receives 403 on `PUT /family/settings` and on a child plugin action. A missing token receives 401.
- The six plugin ids in the PRD are registered and filtered by `allowed_roles` on `GET /api/v1/plugins/active`.
- The global kill-switch flag, inside one process, removes the card and rejects the action.
- Jenkins, Docker, and `docs/deploy.md` describe a real single-container deploy of this process, with no database, smoke-testing `/readyz`, `/`, and `/admin.html`.

Those sentences are smaller than the README. They are also the ones this tree can defend.
