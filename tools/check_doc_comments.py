#!/usr/bin/env python3
# ─────────────────────────────────────────────────────────────
# LangCross · 导出面文档注释门禁（棘轮）
#
# 目的：把「导出符号必须有中文文档注释」变成可执行、可回归的断言。
#   补注释最大的问题是「没有守门人」——补完当次有效，下个提交又漏。
#   本脚本即守门人：超基线（欠账变多）则 exit 1；只降不升。
#
# 判定规则（刻意保守，避免误报）：
#   Go     ：func Foo( / func (r R) Foo( / type Foo struct|interface，且首字母大写，
#            要求紧邻上一行是 //（Go doc comment 语义就是紧邻，空行不算）。
#   TS/TSX ：export function / export async function / export default function /
#            export const / export interface / export type，
#            要求紧邻上一行以 //、/* 或 * 开头。
#   跳过    ：*.d.ts（codegen 产物）、vendor/、node_modules/、.git/。
#
# 用法：
#   python3 tools/check_doc_comments.py                # 超基线则 exit 1
#   python3 tools/check_doc_comments.py --list         # 只列缺口清单
#   python3 tools/check_doc_comments.py --update-baseline   # 把当前值写入基线
#   python3 tools/check_doc_comments.py --selftest     # 自证：探针必须命中、有注释必须放行
#
# 基线文件：tools/.doc_comments_baseline（形如 go=0 / fe=0），入库作为棘轮锚点。
# ─────────────────────────────────────────────────────────────
import os
import re
import sys
import tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BASELINE_FILE = os.path.join(os.path.dirname(os.path.abspath(__file__)), ".doc_comments_baseline")

GO_ROOTS = ["server"]
TS_ROOTS = ["web/src", "miniapp/src"]
SKIP_DIRS = {"node_modules", "vendor", ".git", "dist", "bin"}

# Go 顶层导出声明：func Foo( / func (r R) Foo( / type Foo struct / type Foo interface
GO_DECL = re.compile(
    r"^(?:func\s+(?:\([^)]*\)\s*)?([A-Z]\w*)\s*\(|type\s+([A-Z]\w*)\s+(?:struct|interface)\b)"
)
# TS 导出声明
TS_DECL = re.compile(
    r"^export\s+(?:default\s+)?(?:async\s+)?(?:function|const|let|class|interface|type|enum)\s+(\w+)"
)


def iter_files(roots, suffixes):
    for root in roots:
        base = os.path.join(ROOT, root)
        if not os.path.isdir(base):
            continue
        for dirpath, dirnames, filenames in os.walk(base):
            dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
            for name in sorted(filenames):
                if name.endswith(suffixes) and not name.endswith(".d.ts"):
                    yield os.path.join(dirpath, name)


def go_symbol(line):
    """返回 Go 声明行的导出符号名；不是导出声明返回 None。"""
    m = GO_DECL.match(line)
    if not m:
        return None
    return m.group(1) or m.group(2)


def ts_symbol(line):
    """返回 TS 导出声明行的符号名；不是导出声明返回 None。"""
    m = TS_DECL.match(line)
    if not m:
        return None
    return m.group(1)


def scan():
    """返回 {语言: {文件: [缺失符号名]}}。"""
    gaps = {"go": {}, "fe": {}}

    for path in iter_files(GO_ROOTS, (".go",)):
        rel = os.path.relpath(path, ROOT)
        lines = open(path, encoding="utf-8").read().splitlines()
        missing = []
        for i, line in enumerate(lines):
            sym = go_symbol(line)
            if not sym:
                continue
            # Go doc comment 语义：紧邻上一行必须是 //（空行即不算）
            if i == 0 or not lines[i - 1].lstrip().startswith("//"):
                missing.append(sym)
        if missing:
            gaps["go"][rel] = missing

    for path in iter_files(TS_ROOTS, (".ts", ".tsx")):
        rel = os.path.relpath(path, ROOT)
        lines = open(path, encoding="utf-8").read().splitlines()
        missing = []
        for i, line in enumerate(lines):
            sym = ts_symbol(line)
            if not sym:
                continue
            if i == 0:
                missing.append(sym)
                continue
            prev = lines[i - 1].lstrip()
            # // 行注释、/* 开块、* 续行 都算有文档注释
            if prev.startswith("//") or prev.startswith("/*") or prev.startswith("*"):
                continue
            missing.append(sym)
        if missing:
            gaps["fe"][rel] = missing

    return gaps


def read_baseline():
    base = {"go": 0, "fe": 0}
    if not os.path.exists(BASELINE_FILE):
        return base
    for ln in open(BASELINE_FILE, encoding="utf-8"):
        ln = ln.strip()
        if not ln or "=" not in ln:
            continue
        k, v = ln.split("=", 1)
        k = k.strip()
        if k in base:
            try:
                base[k] = int(v.strip())
            except ValueError:
                pass
    return base


def write_baseline(gaps):
    go_n = sum(len(v) for v in gaps["go"].values())
    fe_n = sum(len(v) for v in gaps["fe"].values())
    with open(BASELINE_FILE, "w", encoding="utf-8") as f:
        f.write(f"go={go_n}\nfe={fe_n}\n")
    return go_n, fe_n


def selftest():
    """自证：注入探针必须被命中；有注释的必须被放行。"""
    probes_go = [
        ("func Exported(a int) {}", True),          # 无注释 → 应命中
        ("func exported(a int) {}", False),          # 未导出 → 应放行
        ("// 文档\nfunc Documented() {}", False),    # 有注释 → 应放行
        ("\nfunc AfterBlank() {}", True),            # 空行隔开 → 应命中（Go 紧邻语义）
        ("type Config struct{}", True),              # 导出类型无注释 → 应命中
        ("// 文档\ntype Config struct{}", False),    # 导出类型有注释 → 应放行
        ("func (s *Server) Handle() {}", True),      # 导出方法无注释 → 应命中
        ("// 文档\nfunc (s *Server) Handle() {}", False),
    ]
    probes_ts = [
        ("export function Foo() {}", True),
        ("// 文档\nexport function Foo() {}", False),
        ("/* 块注释 */\nexport const X = 1", False),
        ("export default function Page() {}", True),
        ("export interface Props {}", True),
        ("function internal() {}", False),           # 未导出 → 放行
        ("export const internalThing = 1", True),    # 导出常量无注释 → 命中
    ]
    ok = True
    for src, should_miss in probes_go:
        lines = src.split("\n")
        hit = False
        for i, ln in enumerate(lines):
            sym = go_symbol(ln)
            if sym and (i == 0 or not lines[i - 1].lstrip().startswith("//")):
                hit = True
        if hit != should_miss:
            print(f"  ✗ Go 探针失败: {src!r} 期望命中={should_miss} 实际={hit}")
            ok = False
    for src, should_miss in probes_ts:
        lines = src.split("\n")
        hit = False
        for i, ln in enumerate(lines):
            sym = ts_symbol(ln)
            if sym:
                if i == 0:
                    hit = True
                    continue
                prev = lines[i - 1].lstrip()
                if not (prev.startswith("//") or prev.startswith("/*") or prev.startswith("*")):
                    hit = True
        if hit != should_miss:
            print(f"  ✗ TS 探针失败: {src!r} 期望命中={should_miss} 实际={hit}")
            ok = False
    if ok:
        print("✓ selftest 通过：探针命中/放行行为符合预期")
        return 0
    return 1


def main():
    args = sys.argv[1:]
    if "--selftest" in args:
        return selftest()

    gaps = scan()
    go_n = sum(len(v) for v in gaps["go"].values())
    fe_n = sum(len(v) for v in gaps["fe"].values())

    if "--list" in args:
        for lang, label in (("go", "Go"), ("fe", "TS/TSX")):
            if gaps[lang]:
                print(f"── {label} 缺口 {sum(len(v) for v in gaps[lang].values())} 个 ──")
                for path, syms in sorted(gaps[lang].items()):
                    print(f"  {path}")
                    for s in syms:
                        print(f"      {s}")
        if go_n == 0 and fe_n == 0:
            print("✓ 无缺口")
        return 0

    if "--update-baseline" in args:
        g, f = write_baseline(gaps)
        print(f"基线已更新：go={g} fe={f}")
        return 0

    base = read_baseline()
    over = []
    if go_n > base["go"]:
        over.append(f"Go 缺口 {go_n} > 基线 {base['go']}")
    if fe_n > base["fe"]:
        over.append(f"TS/TSX 缺口 {fe_n} > 基线 {base['fe']}")
    if over:
        print("✗ 导出面文档注释欠账增加：")
        for o in over:
            print(f"    {o}")
        print("  请为新增导出符号补中文文档注释，或用 --list 查看缺口。")
        return 1
    print(f"✓ 导出面文档注释达标（go {go_n}≤{base['go']} / fe {fe_n}≤{base['fe']}）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
