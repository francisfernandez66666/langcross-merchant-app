#!/bin/bash
# 干净环境包装器：规避本机 PATH shim（toybox grep）与 HTTP 代理对 localhost 的拦截。
# 用法：bash tools/clean_env.sh <命令...>
set -u
export PATH="/usr/local/go/bin:/opt/homebrew/bin:/Users/zhangzifei/.workbuddy/binaries/node/versions/22.22.2-6/bin:/Users/zhangzifei/.workbuddy/binaries/python/versions/3.13.12/bin:/usr/bin:/bin:/usr/sbin:/sbin"
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy
export no_proxy="localhost,127.0.0.1"
export NO_PROXY="localhost,127.0.0.1"
echo "[clean] grep = $(command -v grep) / $(grep --version 2>&1 | head -1)"
echo "[clean] go   = $(command -v go) $(go version 2>/dev/null | awk '{print $3}')"
echo "[clean] node = $(command -v node) $(node --version 2>/dev/null)"
echo "[clean] proxy unset, no_proxy=localhost,127.0.0.1"
exec "$@"
