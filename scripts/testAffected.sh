#!/usr/bin/env bash
# Runs only the Go test packages a change can affect. The full suite stays the
# pull request's job in CI (.github/workflows/deploy.yml).
#
#   scripts/testAffected.sh                  changes since master, run them
#   scripts/testAffected.sh --dry-run        only print what would run and why
#   scripts/testAffected.sh --base dev       compare with another branch/commit
#   scripts/testAffected.sh -- -run TestX    pass the rest to go test
#   scripts/testAffected.sh --dry-run --file internal/x/y.go
#                                            what a change to these files runs
#
# A change is every file that differs from the merge base with --base:
# commits, staged and unstaged edits, and untracked files. Each file maps to
# the Go package of its directory (a fixture or testdata file to the nearest
# package above it). A test package runs when its test binary depends on a
# changed package, which `go list -test -deps` answers, including test-only
# imports. Exceptions, because no import graph shows them:
#   - go.mod / go.sum, or a .go file whose directory is no longer a package:
#     everything runs.
#   - tests/architecture reads the sources under internal/ and cmd/ and
#     fly.toml as files, so any such change runs it (about 2 seconds).
#   - Documentation, CI, Docker and script files run nothing.
# A new test that reads files outside its imports belongs in
# tests/architecture, or needs a rule here.
#
# Integration tests need MongoDB (replica set) and Redis. When
# HOUSEFLOW_TEST_MONGO_URI is unset and a selected package lives under tests/,
# the script starts temporary mongod and redis-server processes (Homebrew
# binaries) and removes them afterwards; --no-services skips that, and the
# integration tests then skip themselves.
set -euo pipefail

base="master"
dry_run=false
race=true
services=true
run_all=false
go_test_args=()
given_files=()

while [ $# -gt 0 ]; do
	case "$1" in
	--base) base="$2"; shift 2 ;;
	--dry-run) dry_run=true; shift ;;
	--no-race) race=false; shift ;;
	--no-services) services=false; shift ;;
	--all) run_all=true; shift ;;
	--file) given_files+=("$2"); shift 2 ;;
	--) shift; go_test_args=("$@"); break ;;
	-h | --help) sed -n '2,30p' "$0"; exit 0 ;;
	*) echo "unknown option: $1 (see --help)" >&2; exit 2 ;;
	esac
done

cd "$(git rev-parse --show-toplevel)"
module="$(go list -m)"
work="$(mktemp -d "${TMPDIR:-/tmp}/testAffected.XXXXXX")"
cleanup() {
	if [ -f "$work/mongod.pid" ]; then kill "$(cat "$work/mongod.pid")" 2>/dev/null || true; fi
	if [ -n "${redis_port:-}" ]; then redis-cli -p "$redis_port" shutdown nosave >/dev/null 2>&1 || true; fi
	# mongod needs a moment to release its files.
	for _ in 1 2 3 4 5 6 7 8 9 10; do
		if [ -f "$work/mongod.pid" ] && kill -0 "$(cat "$work/mongod.pid")" 2>/dev/null; then sleep 0.5; else break; fi
	done
	rm -rf "$work"
}
trap cleanup EXIT

# --- 1. Changed files --------------------------------------------------------
merge_base="$(git merge-base HEAD "$base")"
if [ ${#given_files[@]} -gt 0 ]; then
	printf '%s\n' "${given_files[@]}" | sort -u >"$work/files"
else
	{
		git diff --name-only --no-renames "$merge_base"
		git ls-files --others --exclude-standard
	} | sort -u >"$work/files"
fi

# --- 2. Files to packages ----------------------------------------------------
go list -e -f '{{.Dir}}|{{.ImportPath}}' ./... | sed "s|^$PWD/||; s|^$PWD|.|" >"$work/packages"
package_of_dir() { awk -F'|' -v dir="$1" '$1 == dir { print $2; exit }' "$work/packages"; }

: >"$work/changed"  # import path|reason
: >"$work/ignored"
architecture=false
while IFS= read -r file; do
	[ -n "$file" ] || continue
	case "$file" in
	go.mod | go.sum)
		run_all=true; echo "$file|dependencies changed" >>"$work/changed"; continue ;;
	fly.toml)
		architecture=true; echo "$file|read by tests/architecture" >>"$work/changed"; continue ;;
	*.md | .github/* | docs/* | external/doc/* | deploy/* | scripts/* | Dockerfile | docker-compose*.yml | .gitignore | .dockerignore)
		echo "$file" >>"$work/ignored"; continue ;;
	esac
	dir="$(dirname "$file")"
	package=""
	while :; do
		package="$(package_of_dir "$dir")"
		if [ -n "$package" ] || [ "$dir" = "." ]; then break; fi
		if [ "${file##*.}" = "go" ]; then break; fi # a .go file's own directory or nothing
		dir="$(dirname "$dir")"
	done
	if [ -z "$package" ]; then
		case "$file" in
		*_test.go)
			# A test file of a removed package: nothing imports tests.
			echo "$file" >>"$work/ignored" ;;
		*.go)
			run_all=true; echo "$file|its package directory is gone" >>"$work/changed" ;;
		*)
			echo "$file" >>"$work/ignored" ;;
		esac
		continue
	fi
	echo "$package|$file" >>"$work/changed"
	case "$file" in
	internal/*.go | cmd/*.go) case "$file" in *_test.go) ;; *) architecture=true ;; esac ;;
	esac
done <"$work/files"

# --- 3. Test packages that depend on a changed package ----------------------
go list -e -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./... | sed '/^$/d' >"$work/tested"
if $run_all; then
	reason="$(grep -v '^[^|]*/' "$work/changed" | grep -E '^(go\.mod|go\.sum)\|' | head -1 || true)"
	[ -n "$reason" ] || reason="$(grep 'package directory is gone' "$work/changed" | head -1 || true)"
	[ -n "$reason" ] || reason="--all|requested"
	awk -v why="${reason%%|*}: ${reason#*|}" '{ print $0 "|" why }' "$work/tested" >"$work/selected"
else
	go list -e -test -deps=false -f '{{if eq .Name "main"}}{{.ImportPath}}|{{join .Deps " "}}{{end}}' ./... 2>/dev/null |
		grep '\.test|' >"$work/graph" || true
	awk -F'|' '
		NR == FNR { if (!($1 in why)) why[$1] = $2; next }
		{
			binary = $1; sub(/\.test$/, "", binary)
			n = split($2, deps, " ")
			for (i = 1; i <= n; i++) {
				dep = deps[i]
				if (dep ~ /^\[/ || dep ~ /\]$/) continue # variant markers like "[pkg.test]"
				if (dep in why) { print binary "|" why[dep]; next }
			}
		}' "$work/changed" "$work/graph" >"$work/selected"
	if $architecture && grep -qx "$module/tests/architecture" "$work/tested"; then
		echo "$module/tests/architecture|reads changed sources as files" >>"$work/selected"
	fi
	sort -t'|' -k1,1 -u "$work/selected" -o "$work/selected"
fi

# --- 4. Report ---------------------------------------------------------------
if [ ${#given_files[@]} -gt 0 ]; then
	echo "Files given with --file"
else
	echo "Base: $base ($(git rev-parse --short "$merge_base"))"
fi
echo "Changed files: $(wc -l <"$work/files" | tr -d ' ')"
if [ -s "$work/ignored" ]; then
	echo "Ignored (no Go package or not code):"
	sed 's/^/  /' "$work/ignored"
fi
if [ ! -s "$work/selected" ]; then
	echo "No test package is affected."
	exit 0
fi
echo "Test packages to run:"
awk -F'|' -v module="$module/" '{ p = $1; sub(module, "", p); r = $2; gsub(module, "", r); printf "  %-34s <- %s\n", p, r }' "$work/selected"
cut -d'|' -f1 "$work/selected" >"$work/run"
if $dry_run; then exit 0; fi

# --- 5. Services for integration tests ---------------------------------------
free_port() {
	local port="$1"
	while lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; do port=$((port + 1)); done
	echo "$port"
}
if $services && [ -z "${HOUSEFLOW_TEST_MONGO_URI:-}" ] && grep -q "^$module/tests/" "$work/run"; then
	if command -v mongod >/dev/null && command -v mongosh >/dev/null && command -v redis-server >/dev/null; then
		mongo_port="$(free_port 27100)"
		redis_port="$(free_port 6400)"
		mkdir -p "$work/mongo"
		mongod --replSet rs0 --port "$mongo_port" --dbpath "$work/mongo" --bind_ip 127.0.0.1 \
			--fork --logpath "$work/mongo.log" --pidfilepath "$work/mongod.pid" >/dev/null
		redis-server --port "$redis_port" --daemonize yes --save "" --appendonly no --dir "$work" >/dev/null
		mongosh --quiet --port "$mongo_port" --eval \
			"rs.initiate({_id: 'rs0', members: [{_id: 0, host: '127.0.0.1:$mongo_port'}]})" >/dev/null
		for _ in $(seq 1 60); do
			if [ "$(mongosh --quiet --port "$mongo_port" --eval 'db.hello().isWritablePrimary' 2>/dev/null)" = "true" ] &&
				redis-cli -p "$redis_port" ping >/dev/null 2>&1; then break; fi
			sleep 0.5
		done
		export HOUSEFLOW_TEST_MONGO_URI="mongodb://127.0.0.1:$mongo_port/?replicaSet=rs0"
		# Test packages use databases 1..11 of it (tests/testkit/main.go).
		export HOUSEFLOW_TEST_REDIS_URL="redis://127.0.0.1:$redis_port/1"
		echo "Started temporary MongoDB :$mongo_port and Redis :$redis_port"
	else
		echo "mongod, mongosh or redis-server not found: integration tests will skip." >&2
	fi
fi

# --- 6. Test -----------------------------------------------------------------
flags=(-count=1 -timeout 20m)
if $race; then flags+=(-race); fi
echo "+ go test ${flags[*]} <$(wc -l <"$work/run" | tr -d ' ') packages> ${go_test_args[*]:-}"
# shellcheck disable=SC2046
go test "${flags[@]}" $(cat "$work/run") "${go_test_args[@]+"${go_test_args[@]}"}"
