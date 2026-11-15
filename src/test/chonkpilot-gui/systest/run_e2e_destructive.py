# -*- coding: utf-8 -*-
"""只跑破坏性测试点（stage=`destructive`）：独立工作区 + 独立 HOME。

等价于 `python run_e2e.py --init destructive --with-destructive`，供 bat 与人工单独执行。
破坏性用例可清空 usr/prj 配置，**不要**和常规全量放在同一 GUI 生命周期里跑。
"""

import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
if HERE not in sys.path:
    sys.path.insert(0, HERE)

import run_e2e  # noqa: E402  （导入即注册全部 cases）


def main(argv=None):
    argv = list(sys.argv[1:] if argv is None else argv)
    return run_e2e.main(["--init", "destructive", "--with-destructive"] + argv)


if __name__ == "__main__":
    sys.exit(main())
