"""家庭空间与角色认证路由模块 (FastAPI Router)

包含：
1. 守护者建家与账号注册
2. 守护者密码登录
3. 守护者纳管家庭成员（长辈/儿童）与 Pin 码分配
4. 长辈/儿童专属免密快速登入 (邀请码 + Pin 码)
5. 当前身份查看与家庭成员列表
6. 守护者看护配置修改（严格 RBAC 守卫）
"""
from __future__ import annotations

import random
import string
import uuid
from datetime import datetime, timezone
from typing import Dict, List, Optional

from fastapi import APIRouter, Depends, HTTPException, status

from app.api.deps import JWT_SECRET, get_current_user
from app.core.security import encode_jwt, hash_password, verify_password
from app.schemas.auth import (
    FamilyMemberOut,
    FamilySettingsUpdate,
    LoginRequest,
    MemberAddRequest,
    MemberLoginRequest,
    RegisterRequest,
    TokenResponse,
    UserContext,
    UserRole,
)

router = APIRouter(prefix="/api/v1")

# ================= 内存数据存储库（轻量解耦，可直接升级 SQLite/PG） =================
_FAMILIES: Dict[str, dict] = {}
_USERS: Dict[str, dict] = {}
_PHONE_TO_USER: Dict[str, str] = {}
_INVITE_TO_FAMILY: Dict[str, str] = {}


def _generate_invite_code() -> str:
    """生成 6 位不重复的家庭大写邀请码"""
    while True:
        code = "".join(random.choices(string.ascii_uppercase + string.digits, k=6))
        if code not in _INVITE_TO_FAMILY:
            return code


def _calc_age(birth_date_str: str) -> int:
    """根据出生年月日计算当前精确周岁"""
    try:
        today = datetime.now(timezone.utc).date()
        bdate = datetime.strptime(birth_date_str, "%Y-%m-%d").date()
        age = today.year - bdate.year
        if (today.month, today.day) < (bdate.month, bdate.day):
            age -= 1
        return max(0, age)
    except Exception:
        return 0


# ================= 路由接口实现 =================

@router.post("/auth/register", response_model=TokenResponse, status_code=status.HTTP_201_CREATED)
def register_guardian(req: RegisterRequest):
    """守护者注册账号并同时创建专属家庭空间。"""
    if req.phone in _PHONE_TO_USER:
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail="该手机号已注册，请直接登录")

    family_id = f"fam_{uuid.uuid4().hex[:10]}"
    invite_code = _generate_invite_code()
    _FAMILIES[family_id] = {
        "id": family_id,
        "name": req.family_name,
        "invite_code": invite_code,
        "settings": {"medication_reminder_enabled": True, "kids_curfew_enabled": True},
        "created_at": datetime.now(timezone.utc).isoformat(),
    }
    _INVITE_TO_FAMILY[invite_code] = family_id

    user_id = f"usr_{uuid.uuid4().hex[:10]}"
    _USERS[user_id] = {
        "id": user_id,
        "family_id": family_id,
        "nickname": req.nickname,
        "role": UserRole.GUARDIAN.value,
        "birth_date": req.birth_date,
        "phone": req.phone,
        "password_hash": hash_password(req.password),
        "pin_code": None,
    }
    _PHONE_TO_USER[req.phone] = user_id

    token = encode_jwt(
        {"sub": user_id, "fam": family_id, "role": UserRole.GUARDIAN.value, "name": req.nickname, "birth": req.birth_date},
        JWT_SECRET,
    )
    return TokenResponse(
        access_token=token,
        role=UserRole.GUARDIAN,
        user_id=user_id,
        family_id=family_id,
        name=req.nickname,
    )


@router.post("/auth/login", response_model=TokenResponse)
def login_guardian(req: LoginRequest):
    """守护者手机号 + 密码登录。"""
    user_id = _PHONE_TO_USER.get(req.phone)
    if not user_id:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="手机号或密码错误")

    user = _USERS[user_id]
    if not verify_password(req.password, user["password_hash"]):
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="手机号或密码错误")

    token = encode_jwt(
        {"sub": user["id"], "fam": user["family_id"], "role": user["role"], "name": user["nickname"], "birth": user["birth_date"]},
        JWT_SECRET,
    )
    return TokenResponse(
        access_token=token,
        role=UserRole(user["role"]),
        user_id=user["id"],
        family_id=user["family_id"],
        name=user["nickname"],
    )


@router.post("/family/members", response_model=FamilyMemberOut, status_code=status.HTTP_201_CREATED)
def add_family_member(req: MemberAddRequest, u: UserContext = Depends(get_current_user)):
    """为家庭新增成员（长辈或幼童），仅限家庭守护者（Guardian）操作。"""
    if u.role != UserRole.GUARDIAN:
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="权限不足：仅家庭守护者可添加家庭成员")

    pin = req.pin_code or "".join(random.choices(string.digits, k=4))
    user_id = f"usr_{uuid.uuid4().hex[:10]}"
    _USERS[user_id] = {
        "id": user_id,
        "family_id": u.family_id,
        "nickname": req.nickname,
        "role": req.role.value,
        "birth_date": req.birth_date,
        "phone": None,
        "password_hash": None,
        "pin_code": pin,
    }

    return FamilyMemberOut(
        id=user_id,
        nickname=req.nickname,
        role=req.role,
        birth_date=req.birth_date,
        age=_calc_age(req.birth_date),
        pin_code=pin,
    )


@router.post("/auth/member-login", response_model=TokenResponse)
def member_quick_login(req: MemberLoginRequest):
    """长辈或幼童使用家庭邀请码 + 个人 Pin 码免密极速登入。"""
    family_id = _INVITE_TO_FAMILY.get(req.invite_code.upper())
    if not family_id:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="家庭邀请码不存在")

    matched_user = None
    for u in _USERS.values():
        if u["family_id"] == family_id and u.get("pin_code") == req.pin_code:
            matched_user = u
            break

    if not matched_user:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="Pin 码错误或该成员未登记")

    token = encode_jwt(
        {"sub": matched_user["id"], "fam": family_id, "role": matched_user["role"], "name": matched_user["nickname"], "birth": matched_user["birth_date"]},
        JWT_SECRET,
    )
    return TokenResponse(
        access_token=token,
        role=UserRole(matched_user["role"]),
        user_id=matched_user["id"],
        family_id=family_id,
        name=matched_user["nickname"],
    )


@router.get("/auth/me")
def get_my_profile(u: UserContext = Depends(get_current_user)):
    """获取当前登录使用者的上下文信息。"""
    fam = _FAMILIES.get(u.family_id, {})
    return {
        "user_id": u.user_id,
        "family_id": u.family_id,
        "family_name": fam.get("name", "我的温馨家"),
        "invite_code": fam.get("invite_code", ""),
        "nickname": u.nickname,
        "role": u.role,
        "birth_date": u.birth_date,
        "age": _calc_age(u.birth_date),
    }


@router.get("/family/members", response_model=List[FamilyMemberOut])
def list_family_members(u: UserContext = Depends(get_current_user)):
    """列出当前家庭的所有成员（自动过滤其他家庭，严格多租户隔离）。"""
    results = []
    for user in _USERS.values():
        if user["family_id"] == u.family_id:
            # 仅守护者可见成员的 Pin 码，普通成员隐藏
            pin = user.get("pin_code") if u.role == UserRole.GUARDIAN else None
            results.append(
                FamilyMemberOut(
                    id=user["id"],
                    nickname=user["nickname"],
                    role=UserRole(user["role"]),
                    birth_date=user["birth_date"],
                    age=_calc_age(user["birth_date"]),
                    pin_code=pin,
                )
            )
    return results


@router.put("/family/settings")
def update_family_settings(req: FamilySettingsUpdate, u: UserContext = Depends(get_current_user)):
    """修改家庭看护设置（如用药提醒总开关、防沉迷开关），仅限家庭守护者。"""
    if u.role != UserRole.GUARDIAN:
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="权限不足：仅家庭守护者可调整看护设置")

    fam = _FAMILIES.get(u.family_id)
    if not fam:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="家庭空间不存在")

    fam["settings"] = req.model_dump()
    return {"status": "ok", "settings": fam["settings"]}


# ================= 跨模块辅助查询函数 =================

def get_family_by_id(family_id: str) -> Optional[dict]:
    """获取指定家庭对象引用"""
    return _FAMILIES.get(family_id)


def get_all_families_count() -> int:
    """获取平台已注册家庭总数"""
    return len(_FAMILIES)


def get_all_users() -> List[dict]:
    """获取全平台用户列表副本"""
    return list(_USERS.values())
