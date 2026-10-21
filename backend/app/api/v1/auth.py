"""家庭空间与角色认证路由模块 (FastAPI Router)

包含：
1. 守护者建家与账号注册
2. 守护者密码登录
3. 守护者纳管家庭成员（长辈/儿童）与 Pin 码分配
4. 长辈/儿童专属免密快速登入 (邀请码 + Pin 码)
5. 当前身份查看与家庭成员列表、更新、移除
6. 守护者看护配置修改（合并写入，不覆盖插件开关）
"""
from __future__ import annotations

import secrets
import string
import time
import uuid
from datetime import datetime, timezone
from typing import List, Optional

from fastapi import APIRouter, Depends, HTTPException, Response, status

from app.api.deps import require_roles
from app.core.runtime_secrets import jwt_secret
from app.core.security import encode_jwt, hash_password, verify_password
from app.core.store import store
from app.schemas.auth import (
    FamilyMemberOut,
    FamilyMemberRole,
    FamilySettingsUpdate,
    LoginRequest,
    MemberAddRequest,
    MemberLoginRequest,
    MemberUpdateRequest,
    RegisterRequest,
    TokenResponse,
    UserContext,
    UserRole,
)

router = APIRouter(prefix="/api/v1")

# 登录失败计数写在 PostgreSQL，重启后窗口仍然有效。
LOGIN_FAIL_LIMIT = 5
LOGIN_FAIL_WINDOW_SECONDS = 300

MEMBER_LOGIN_DETAIL = "邀请码或 Pin 码错误"
ELDER_MIN_AGE = 60
CHILD_MAX_AGE = 12
GUARDIAN_MIN_AGE = 13
GUARDIAN_MAX_AGE = 59
PIN_LOGIN_ROLES = {UserRole.ELDER.value, UserRole.CHILD.value}

ADMIN_USER_ID = "usr_admin_root"
ADMIN_FAMILY_ID = "fam_admin_system"


def _generate_invite_code() -> str:
    """生成 6 位不重复的家庭邀请码。使用 secrets，而不是 random。"""
    alphabet = string.ascii_uppercase + string.digits
    for _ in range(32):
        code = "".join(secrets.choice(alphabet) for _ in range(6))
        if store.get_family_by_invite(code) is None:
            return code
    raise HTTPException(status_code=status.HTTP_500_INTERNAL_SERVER_ERROR, detail="无法分配家庭邀请码")


def _calc_age(birth_date_str: str) -> int:
    """根据出生年月日计算当前周岁。非法日期返回 0，仅用于展示。"""
    try:
        return _age_years(birth_date_str)
    except HTTPException:
        return 0


def _age_years(birth_date_str: str) -> int:
    try:
        bdate = datetime.strptime(birth_date_str, "%Y-%m-%d").date()
    except ValueError as exc:
        raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail="出生日期格式应为 YYYY-MM-DD") from exc
    today = datetime.now(timezone.utc).date()
    if bdate > today:
        raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail="出生日期不能晚于今天")
    age = today.year - bdate.year
    if (today.month, today.day) < (bdate.month, bdate.day):
        age -= 1
    return age


def _assert_age_for_role(role: str, birth_date_str: str) -> int:
    """长辈 ≥60，儿童 ≤12，守护者 13–59。不匹配则 422。"""
    age = _age_years(birth_date_str)
    if role == UserRole.ELDER.value and age < ELDER_MIN_AGE:
        raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail="长辈角色要求周岁年龄不小于 60")
    if role == UserRole.CHILD.value and age > CHILD_MAX_AGE:
        raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail="儿童角色要求周岁年龄不大于 12")
    if role == UserRole.GUARDIAN.value and not (GUARDIAN_MIN_AGE <= age <= GUARDIAN_MAX_AGE):
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
            detail="守护者角色要求周岁年龄在 13 到 59 之间",
        )
    return age


def _pin_taken(family_id: str, pin: str, except_user_id: Optional[str] = None) -> bool:
    for user in store.list_users():
        if user.get("family_id") != family_id or user.get("id") == except_user_id:
            continue
        pin_hash = user.get("pin_hash")
        if pin_hash and verify_password(pin, pin_hash):
            return True
    return False


def _allocate_pin(family_id: str) -> str:
    for _ in range(32):
        pin = "".join(secrets.choice(string.digits) for _ in range(4))
        if not _pin_taken(family_id, pin):
            return pin
    raise HTTPException(status_code=status.HTTP_500_INTERNAL_SERVER_ERROR, detail="无法分配 Pin 码")


def _fail_storage_key(key: str) -> str:
    return f"login_fail:{key}"


def _recent_failures(key: str) -> List[float]:
    raw = store.kv_get(_fail_storage_key(key)) or ""
    now = time.time()
    kept: List[float] = []
    for part in raw.split(","):
        if not part:
            continue
        try:
            stamp = float(part)
        except ValueError:
            continue
        if now - stamp < LOGIN_FAIL_WINDOW_SECONDS:
            kept.append(stamp)
    if kept:
        store.kv_set(_fail_storage_key(key), ",".join(str(stamp) for stamp in kept))
    else:
        store.kv_delete(_fail_storage_key(key))
    return kept


def enforce_login_attempt(key: str) -> None:
    """连续失败达到上限后，窗口内直接 429。正确口令也不能绕过。"""
    if len(_recent_failures(key)) >= LOGIN_FAIL_LIMIT:
        raise HTTPException(status_code=status.HTTP_429_TOO_MANY_REQUESTS, detail="尝试次数过多，请稍后再试")


def record_login_failure(key: str) -> None:
    kept = _recent_failures(key)
    kept.append(time.time())
    store.kv_set(_fail_storage_key(key), ",".join(str(stamp) for stamp in kept))


def clear_login_failures(key: str) -> None:
    store.kv_delete(_fail_storage_key(key))


def _issue_token(user: dict) -> TokenResponse:
    token = encode_jwt(
        {
            "sub": user["id"],
            "fam": user["family_id"],
            "role": user["role"],
            "name": user["nickname"],
            "birth": user["birth_date"],
        },
        jwt_secret(),
    )
    return TokenResponse(
        access_token=token,
        role=UserRole(user["role"]),
        user_id=user["id"],
        family_id=user["family_id"],
        name=user["nickname"],
    )


def _member_out(user: dict, pin_code: Optional[str] = None) -> FamilyMemberOut:
    return FamilyMemberOut(
        id=user["id"],
        nickname=user["nickname"],
        role=UserRole(user["role"]),
        birth_date=user["birth_date"],
        age=_calc_age(user["birth_date"]),
        pin_code=pin_code,
    )


def _family_member_or_404(member_id: str, family_id: str) -> dict:
    user = store.get_user(member_id)
    if not user or user.get("family_id") != family_id:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="家庭成员不存在")
    return user


def ensure_platform_admin() -> dict:
    """管理员登录成功后写入内存用户，使 get_current_user 能核对该令牌。"""
    user = {
        "id": ADMIN_USER_ID,
        "family_id": ADMIN_FAMILY_ID,
        "nickname": "平台超级管理员",
        "role": UserRole.ADMIN.value,
        "birth_date": "1990-01-01",
        "phone": None,
        "password_hash": None,
        "pin_hash": None,
        "pin_code": None,
    }
    store.upsert_user(user)
    return user


# ================= 路由接口实现 =================

@router.post("/auth/register", response_model=TokenResponse, status_code=status.HTTP_201_CREATED)
def register_guardian(req: RegisterRequest):
    """守护者注册账号并同时创建专属家庭空间。"""
    _assert_age_for_role(UserRole.GUARDIAN.value, req.birth_date)
    if store.get_user_by_phone(req.phone):
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail="该手机号已注册，请直接登录")

    family_id = f"fam_{uuid.uuid4().hex[:10]}"
    invite_code = _generate_invite_code()
    store.upsert_family(
        {
            "id": family_id,
            "name": req.family_name,
            "invite_code": invite_code,
            "settings": {"medication_reminder_enabled": True, "kids_curfew_enabled": True},
            "created_at": datetime.now(timezone.utc).isoformat(),
        }
    )

    user_id = f"usr_{uuid.uuid4().hex[:10]}"
    user = {
        "id": user_id,
        "family_id": family_id,
        "nickname": req.nickname,
        "role": UserRole.GUARDIAN.value,
        "birth_date": req.birth_date,
        "phone": req.phone,
        "password_hash": hash_password(req.password),
        "pin_hash": None,
        "pin_code": None,
    }
    store.upsert_user(user)
    return _issue_token(user)


@router.post("/auth/login", response_model=TokenResponse)
def login_guardian(req: LoginRequest):
    """守护者手机号 + 密码登录。"""
    key = f"guardian:{req.phone}"
    enforce_login_attempt(key)
    user = store.get_user_by_phone(req.phone)
    if not user or not user.get("password_hash") or not verify_password(req.password, user["password_hash"]):
        record_login_failure(key)
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="手机号或密码错误")
    if user.get("role") != UserRole.GUARDIAN.value:
        record_login_failure(key)
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="手机号或密码错误")
    clear_login_failures(key)
    return _issue_token(user)


@router.post("/family/members", response_model=FamilyMemberOut, status_code=status.HTTP_201_CREATED)
def add_family_member(
    req: MemberAddRequest,
    u: UserContext = Depends(require_roles([UserRole.GUARDIAN])),
):
    """为家庭新增成员（长辈或幼童），仅限家庭守护者。"""
    if req.role not in (FamilyMemberRole.ELDER, FamilyMemberRole.CHILD):
        raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail="仅可纳管长辈或儿童")
    role = req.role.value
    age = _assert_age_for_role(role, req.birth_date)
    pin = req.pin_code or _allocate_pin(u.family_id)
    if _pin_taken(u.family_id, pin):
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail="该 Pin 码已被家庭中其他成员使用")

    user_id = f"usr_{uuid.uuid4().hex[:10]}"
    user = {
        "id": user_id,
        "family_id": u.family_id,
        "nickname": req.nickname,
        "role": role,
        "birth_date": req.birth_date,
        "phone": None,
        "password_hash": None,
        "pin_hash": hash_password(pin),
        "pin_code": None,
    }
    store.upsert_user(user)
    created = _member_out(user, pin_code=pin)
    created.age = age
    return created


@router.put("/family/members/{member_id}", response_model=FamilyMemberOut)
def update_family_member(
    member_id: str,
    req: MemberUpdateRequest,
    u: UserContext = Depends(require_roles([UserRole.GUARDIAN])),
):
    """更新本家庭成员称呼、出生日期或 Pin。Pin 只在本次响应中出现一次。"""
    user = _family_member_or_404(member_id, u.family_id)
    if user.get("role") == UserRole.ADMIN.value:
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="权限不足：不能修改平台管理员")

    revealed_pin: Optional[str] = None
    if req.nickname is not None:
        user["nickname"] = req.nickname
    if req.birth_date is not None:
        _assert_age_for_role(str(user["role"]), req.birth_date)
        user["birth_date"] = req.birth_date
    if req.pin_code is not None:
        if user.get("role") not in PIN_LOGIN_ROLES:
            raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail="该成员不使用 Pin 登录")
        if _pin_taken(u.family_id, req.pin_code, except_user_id=user["id"]):
            raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail="该 Pin 码已被家庭中其他成员使用")
        user["pin_hash"] = hash_password(req.pin_code)
        user["pin_code"] = None
        revealed_pin = req.pin_code
    store.upsert_user(user)
    return _member_out(user, pin_code=revealed_pin)


@router.delete("/family/members/{member_id}", status_code=status.HTTP_204_NO_CONTENT)
def remove_family_member(
    member_id: str,
    u: UserContext = Depends(require_roles([UserRole.GUARDIAN])),
):
    """移除本家庭成员。不能删除当前登录的守护者本人。"""
    user = _family_member_or_404(member_id, u.family_id)
    if user["id"] == u.user_id:
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="权限不足：不能移除当前登录账号")
    if user.get("role") == UserRole.ADMIN.value:
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="权限不足：不能移除平台管理员")
    store.delete_user(user["id"])
    return Response(status_code=status.HTTP_204_NO_CONTENT)


@router.post("/auth/member-login", response_model=TokenResponse)
def member_quick_login(req: MemberLoginRequest):
    """长辈或幼童使用家庭邀请码 + 个人 Pin 码登录。

    邀请码不存在与 Pin 错误返回同一句 401，避免枚举家庭。
    平台 admin 角色即使被写入用户表，也不能从这里换到令牌。
    """
    invite = req.invite_code.upper()
    key = f"member:{invite}"
    enforce_login_attempt(key)

    family = store.get_family_by_invite(invite)
    family_id = family["id"] if family else None
    matched_user = None
    if family_id:
        for user in store.list_users():
            if user.get("family_id") != family_id or user.get("role") not in PIN_LOGIN_ROLES:
                continue
            pin_hash = user.get("pin_hash")
            if pin_hash and verify_password(req.pin_code, pin_hash):
                matched_user = user
                break

    if not matched_user:
        record_login_failure(key)
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail=MEMBER_LOGIN_DETAIL)

    clear_login_failures(key)
    return _issue_token(matched_user)


@router.post("/auth/logout", status_code=status.HTTP_204_NO_CONTENT)
def logout(u: UserContext = Depends(require_roles(list(UserRole)))):
    """注销当前这枚令牌。同一用户的其他未注销令牌在过期前仍然有效。"""
    store.revoke_jti(u.jti, u.token_exp)
    return Response(status_code=status.HTTP_204_NO_CONTENT)


@router.get("/auth/me")
def get_my_profile(u: UserContext = Depends(require_roles(list(UserRole)))):
    """获取当前登录使用者的上下文。邀请码只返回给守护者。"""
    fam = store.get_family(u.family_id) or {}
    invite_code = fam.get("invite_code", "") if u.role == UserRole.GUARDIAN else ""
    profile = {
        "user_id": u.user_id,
        "family_id": u.family_id,
        "family_name": fam.get("name", "我的温馨家"),
        "invite_code": invite_code,
        "nickname": u.nickname,
        "role": u.role,
        "birth_date": u.birth_date,
        "age": _calc_age(u.birth_date),
    }
    if u.role == UserRole.GUARDIAN:
        profile["settings"] = fam.get("settings", {})
    return profile


@router.get("/family/members", response_model=List[FamilyMemberOut])
def list_family_members(u: UserContext = Depends(require_roles(list(UserRole)))):
    """列出当前家庭的成员。Pin 不以明文返回。"""
    results = []
    for user in store.list_users():
        if user.get("family_id") == u.family_id:
            results.append(_member_out(user, pin_code=None))
    return results


@router.put("/family/settings")
def update_family_settings(
    req: FamilySettingsUpdate,
    u: UserContext = Depends(require_roles([UserRole.GUARDIAN])),
):
    """合并家庭看护设置。不会清掉 settings.plugins。"""
    fam = store.get_family(u.family_id)
    if not fam:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="家庭空间不存在")

    settings = fam.setdefault("settings", {})
    settings.update(req.model_dump())
    store.upsert_family(fam)
    return {"status": "ok", "settings": settings}


# ================= 跨模块辅助查询函数 =================

def get_family_by_id(family_id: str) -> Optional[dict]:
    """读取家庭。返回的是副本，改完要 save_family。"""
    return store.get_family(family_id)


def save_family(family: dict) -> None:
    """把家庭名称、邀请码和 settings（含插件开关）写回 PostgreSQL。"""
    store.upsert_family(family)


def get_all_families_count() -> int:
    """获取平台已注册家庭总数"""
    return store.count_families()


def get_all_users() -> List[dict]:
    """获取全平台用户列表副本"""
    return store.list_users()
