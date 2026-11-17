"""Built-in benchmark challenge definitions."""

from typing import Dict, List, Optional
from tiancode.eval.models import BenchmarkProblem

_CHALLENGES: List[BenchmarkProblem] = [
    BenchmarkProblem(
        id="p1_lru_cache",
        title="LRUCache 缓存淘汰策略修复",
        description=(
            "lru_cache.py 中实现了一个支持容量上限的 LRUCache。"
            "当前实现存在 Bug：每次 get(key) 或 put(key, val) 时没有正确更新键的访问热度，"
            "导致淘汰时误剔除了最近刚被访问过的 key。请修复该 Bug 并保证所有单元测试通过。"
        ),
        starter_files={
            "lru_cache.py": (
                "class LRUCache:\n"
                "    def __init__(self, capacity: int):\n"
                "        self.capacity = capacity\n"
                "        self.cache = {}\n"
                "\n"
                "    def get(self, key: int) -> int:\n"
                "        return self.cache.get(key, -1)\n"
                "\n"
                "    def put(self, key: int, value: int) -> None:\n"
                "        if key not in self.cache and len(self.cache) >= self.capacity:\n"
                "            first_key = next(iter(self.cache))\n"
                "            del self.cache[first_key]\n"
                "        self.cache[key] = value\n"
            )
        },
        test_files={
            "test_lru_cache.py": (
                "from lru_cache import LRUCache\n"
                "\n"
                "def test_lru_basic():\n"
                "    cache = LRUCache(2)\n"
                "    cache.put(1, 1)\n"
                "    cache.put(2, 2)\n"
                "    assert cache.get(1) == 1\n"
                "    cache.put(3, 3)  # 应淘汰 key 2，因为 key 1 最近刚被访问\n"
                "    assert cache.get(2) == -1\n"
                "    assert cache.get(1) == 1\n"
                "    assert cache.get(3) == 3\n"
                "\n"
                "def test_lru_update_existing():\n"
                "    cache = LRUCache(2)\n"
                "    cache.put(1, 10)\n"
                "    cache.put(2, 20)\n"
                "    cache.put(1, 15)  # 更新 key 1\n"
                "    cache.put(3, 30)  # 应淘汰 key 2\n"
                "    assert cache.get(2) == -1\n"
                "    assert cache.get(1) == 15\n"
                "    assert cache.get(3) == 30\n"
            )
        },
        timeout_seconds=60,
        max_turns=5,
    ),
    BenchmarkProblem(
        id="p2_semver_comparator",
        title="SemVer 语义化版本解析与比较器",
        description=(
            "semver.py 实现了语义化版本（如 '1.2.3' 或 '1.2.3-alpha'）的比较函数 compare_versions(v1, v2)。"
            "返回值：v1 > v2 返回 1，v1 < v2 返回 -1，相等返回 0。"
            "当前实现没有遵循 SemVer 规约：没有 pre-release 的正式版本优先级高于带有 pre-release 的同版本号"
            "（例如 '1.0.0' 应大于 '1.0.0-alpha'）。请修复该逻辑。"
        ),
        starter_files={
            "semver.py": (
                "def compare_versions(v1: str, v2: str) -> int:\n"
                "    \"\"\"Returns 1 if v1 > v2, -1 if v1 < v2, 0 if equal.\"\"\"\n"
                "    p1 = v1.split('-')\n"
                "    p2 = v2.split('-')\n"
                "    nums1 = [int(x) for x in p1[0].split('.')]\n"
                "    nums2 = [int(x) for x in p2[0].split('.')]\n"
                "    if nums1 > nums2:\n"
                "        return 1\n"
                "    elif nums1 < nums2:\n"
                "        return -1\n"
                "    return 0\n"
            )
        },
        test_files={
            "test_semver.py": (
                "from semver import compare_versions\n"
                "\n"
                "def test_basic_versions():\n"
                "    assert compare_versions('1.0.0', '1.0.0') == 0\n"
                "    assert compare_versions('1.2.0', '1.1.9') == 1\n"
                "    assert compare_versions('2.0.0', '2.0.1') == -1\n"
                "\n"
                "def test_pre_release_precedence():\n"
                "    # 正式版本高于预发布版本\n"
                "    assert compare_versions('1.0.0', '1.0.0-alpha') == 1\n"
                "    assert compare_versions('1.0.0-alpha', '1.0.0') == -1\n"
                "    assert compare_versions('1.0.0-alpha', '1.0.0-beta') == -1\n"
            )
        },
        timeout_seconds=60,
        max_turns=5,
    ),
    BenchmarkProblem(
        id="p3_cycle_detector",
        title="有向图环路检测器",
        description=(
            "graph.py 实现了 has_cycle(graph) 函数，输入为邻接表字典。"
            "当前使用深度优先遍历，但在递归回溯时状态标记混淆，将普通的菱形汇聚结构（无环 DAG）误判为存在环。"
            "请修复该环路检测算法，使其正确识别是否有环。"
        ),
        starter_files={
            "graph.py": (
                "from typing import Dict, List\n"
                "\n"
                "def has_cycle(graph: Dict[str, List[str]]) -> bool:\n"
                "    visited = set()\n"
                "    def dfs(node):\n"
                "        if node in visited:\n"
                "            return True\n"
                "        visited.add(node)\n"
                "        for neighbor in graph.get(node, []):\n"
                "            if dfs(neighbor):\n"
                "                return True\n"
                "        return False\n"
                "\n"
                "    for node in graph:\n"
                "        if dfs(node):\n"
                "            return True\n"
                "    return False\n"
            )
        },
        test_files={
            "test_graph.py": (
                "from graph import has_cycle\n"
                "\n"
                "def test_diamond_no_cycle():\n"
                "    # 菱形结构无环: A -> B, A -> C, B -> D, C -> D\n"
                "    dag = {'A': ['B', 'C'], 'B': ['D'], 'C': ['D'], 'D': []}\n"
                "    assert has_cycle(dag) is False\n"
                "\n"
                "def test_simple_cycle():\n"
                "    cyclic = {'A': ['B'], 'B': ['C'], 'C': ['A']}\n"
                "    assert has_cycle(cyclic) is True\n"
                "\n"
                "def test_disconnected_with_cycle():\n"
                "    graph = {'X': ['Y'], 'Y': [], 'A': ['B'], 'B': ['A']}\n"
                "    assert has_cycle(graph) is True\n"
            )
        },
        timeout_seconds=60,
        max_turns=5,
    ),
]


def get_all_challenges() -> List[BenchmarkProblem]:
    """Retrieve all built-in benchmark problems."""
    return list(_CHALLENGES)


def get_challenge(problem_id: str) -> Optional[BenchmarkProblem]:
    """Find a specific benchmark problem by ID."""
    for c in _CHALLENGES:
        if c.id == problem_id:
            return c
    return None
