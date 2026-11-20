#!/bin/bash
# Real end-to-end check: ask the local squid to open a tunnel to the exchange. A bare port
# check is not enough — a hung squid still accepts TCP but never answers CONNECT.
TARGET="${HEALTHCHECK_TARGET:-api.bybit.com:443}"
exec 3<>/dev/tcp/127.0.0.1/3128 || exit 1
printf 'CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n' "$TARGET" "$TARGET" >&3
IFS= read -r -t 8 line <&3 || exit 1
[[ "$line" == *" 200 "* ]]
