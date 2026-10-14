#!/usr/bin/env bash
# Behavioral test for the default nginx config: renders the chart, serves a
# fixture /sites tree with the nginx image from values.yaml and checks the
# responses the way Traefik forwards requests (addPrefix /<site>, and for
# path-prefixed sites stripPrefix, which sets X-Forwarded-Prefix).
set -euo pipefail

CHART_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
WORK="$(mktemp -d)"
NAME="pages-nginx-test-$$"
PORT="${NGINX_TEST_PORT:-18080}"

cleanup() {
    docker rm -f "$NAME" >/dev/null 2>&1 || true
    rm -rf "$WORK"
}
trap cleanup EXIT

helm template t "$CHART_DIR" --set "syncer.allowedHosts={example.com}" --show-only templates/configmap-nginx.yaml \
    | yq '.data["default.conf"]' > "$WORK/default.conf"
IMAGE="$(yq '.nginx.image.registry + "/" + .nginx.image.repository + ":" + .nginx.image.tag' "$CHART_DIR/values.yaml")"

mkdir -p "$WORK/sites/at19/programm" "$WORK/sites/at19/über uns" "$WORK/sites/docs/reference"
echo "at19" > "$WORK/sites/at19/index.html"
echo "programm" > "$WORK/sites/at19/programm/index.html"
echo "reference" > "$WORK/sites/docs/reference/index.html"
chmod -R a+rX "$WORK"

docker run -d --name "$NAME" -p "127.0.0.1:$PORT:80" \
    -v "$WORK/default.conf:/etc/nginx/conf.d/default.conf:ro" \
    -v "$WORK/sites:/sites:ro" \
    "$IMAGE" >/dev/null

for _ in $(seq 1 50); do
    curl -fs "http://127.0.0.1:$PORT/health" >/dev/null 2>&1 && break
    sleep 0.2
done

failures=0

# expect <description> <expected status> <expected Location or ""> <path> [header]
expect() {
    local desc="$1" want_status="$2" want_location="$3" path="$4" header="${5:-}"
    local args=(-s -o /dev/null -D - "http://127.0.0.1:$PORT$path")
    if [ -n "$header" ]; then
        args+=(-H "$header")
    fi
    local headers status location
    headers="$(curl "${args[@]}" | tr -d '\r')"
    status="$(echo "$headers" | awk 'NR==1 {print $2}')"
    location="$(echo "$headers" | awk 'tolower($1)=="location:" {print $2}')"
    if [ "$status" != "$want_status" ] || [ "$location" != "$want_location" ]; then
        echo "FAIL: $desc: got $status '$location', want $want_status '$want_location'"
        failures=$((failures + 1))
    else
        echo "ok:   $desc"
    fi
}

expect "site root with slash is served" 200 "" /at19/ "X-Forwarded-Prefix: /2019"
expect "site root without slash redirects to the public prefix" 301 /2019/ /at19 "X-Forwarded-Prefix: /2019"
expect "subdirectory without slash redirects to the public prefix" 301 /2019/programm/ /at19/programm "X-Forwarded-Prefix: /2019"
expect "an encoded path stays encoded" 301 /2019/%C3%BCber%20uns/ "/at19/%C3%BCber%20uns" "X-Forwarded-Prefix: /2019"
expect "the query string is kept" 301 "/2019/programm/?lang=en" "/at19/programm?lang=en" "X-Forwarded-Prefix: /2019"
expect "nested public prefix is kept" 301 /a/b/programm/ /at19/programm "X-Forwarded-Prefix: /a/b"
expect "site without path prefix redirects without the site name" 301 /reference/ /docs/reference
expect "a protocol-relative prefix is ignored" 301 /reference/ /docs/reference "X-Forwarded-Prefix: //evil.example"
expect "a prefix with a scheme is ignored" 301 /reference/ /docs/reference "X-Forwarded-Prefix: https://evil.example"
expect "a missing directory stays 404" 404 "" /docs/missing

if [ "$failures" -gt 0 ]; then
    echo "$failures check(s) failed"
    exit 1
fi
