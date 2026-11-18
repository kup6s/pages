#!/usr/bin/env bash
# Behavioral test for the default nginx config: renders the chart, serves a
# fixture /sites tree with the nginx image from values.yaml and checks the
# responses the way Traefik forwards requests (addPrefix /<namespace>/<name>, and for
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

mkdir -p "$WORK/sites/aaf/at19/programm" "$WORK/sites/aaf/at19/über uns" "$WORK/sites/team/docs/reference" \
    "$WORK/sites/.clones/team/docs/src" "$WORK/sites/.repos/docs/src" "$WORK/sites/team/.tmp-link-docs-0123abcd"
echo "at19" > "$WORK/sites/aaf/at19/index.html"
echo "programm" > "$WORK/sites/aaf/at19/programm/index.html"
echo "reference" > "$WORK/sites/team/docs/reference/index.html"
echo "source" > "$WORK/sites/.clones/team/docs/src/notes.txt"
echo "source" > "$WORK/sites/.repos/docs/src/notes.txt"
echo "temporary" > "$WORK/sites/team/.tmp-link-docs-0123abcd/index.html"
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
    local args=(-s --path-as-is -o /dev/null -D - "http://127.0.0.1:$PORT$path")
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

expect "site root with slash is served" 200 "" /aaf/at19/ "X-Forwarded-Prefix: /2019"
expect "site root without slash redirects to the public prefix" 301 /2019/ /aaf/at19 "X-Forwarded-Prefix: /2019"
expect "subdirectory without slash redirects to the public prefix" 301 /2019/programm/ /aaf/at19/programm "X-Forwarded-Prefix: /2019"
expect "an encoded path stays encoded" 301 /2019/%C3%BCber%20uns/ "/aaf/at19/%C3%BCber%20uns" "X-Forwarded-Prefix: /2019"
expect "the query string is kept" 301 "/2019/programm/?lang=en" "/aaf/at19/programm?lang=en" "X-Forwarded-Prefix: /2019"
expect "nested public prefix is kept" 301 /a/b/programm/ /aaf/at19/programm "X-Forwarded-Prefix: /a/b"
expect "site without path prefix redirects without the site name" 301 /reference/ /team/docs/reference
expect "a protocol-relative prefix is ignored" 301 /reference/ /team/docs/reference "X-Forwarded-Prefix: //evil.example"
expect "a prefix with a scheme is ignored" 301 /reference/ /team/docs/reference "X-Forwarded-Prefix: https://evil.example"
expect "a missing directory stays 404" 404 "" /team/docs/missing

# Every site shares root /sites, so a request must never leave the
# /<namespace>/<name> segments that addPrefix put in front of it.
expect "a dot-dot segment is rejected" 400 "" /aaf/at19/../../team/docs/reference/
expect "an encoded dot-dot segment is rejected" 400 "" /aaf/at19/%2e%2e/docs/reference/
expect "a mixed-case encoded dot-dot segment is rejected" 400 "" /aaf/at19/.%2E/docs/reference/
expect "an encoded slash is rejected" 400 "" /aaf/at19/..%2fdocs/reference/
expect "an upper-case encoded slash is rejected" 400 "" /aaf/at19/..%2Fdocs/reference/
expect "repository checkouts are not served" 404 "" /.clones/team/docs/src/notes.txt
expect "legacy repository checkouts are not served" 404 "" /.repos/docs/src/notes.txt
expect "temporary entries of a namespace are not served" 404 "" /team/.tmp-link-docs-0123abcd/index.html
expect "repository checkouts are not reachable by traversal" 400 "" /aaf/at19/%2e%2e%2f%2e%2e%2f.clones/team/docs/src/notes.txt
expect "dots inside a file name are allowed" 404 "" /aaf/at19/a..b.html
expect "encoded characters in the query are allowed" 200 "" "/aaf/at19/?next=%2Fdocs%2F..%2F"

if [ "$failures" -gt 0 ]; then
    echo "$failures check(s) failed"
    exit 1
fi
