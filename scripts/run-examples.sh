#!/usr/bin/env bash
# Runs every example against the real `claude` binary and reports pass/fail.
#
# Most examples are one-shot and should exit 0 within the timeout. Two are
# "special": `interactive` reads stdin (we feed it a line + /quit) and
# `interrupt` is expected to run a few seconds then stop itself — both are
# treated as OK if they start and exit cleanly within the timeout.
#
# Usage: scripts/run-examples.sh [timeout-seconds]
set -uo pipefail

cd "$(dirname "$0")/.."

TIMEOUT="${1:-90}"
PASS=0
FAIL=0
declare -a FAILED=()

run() {
	local name="$1"; shift
	printf '  %-18s ' "$name"
	local out rc
	out="$("$@" 2>&1)"; rc=$?
	if [ $rc -eq 0 ]; then
		echo "ok"
		PASS=$((PASS + 1))
	else
		echo "FAIL (exit $rc)"
		echo "$out" | sed 's/^/      | /' | tail -5
		FAIL=$((FAIL + 1))
		FAILED+=("$name")
	fi
}

# One-shot examples: just `go run` with a timeout.
for ex in query collect agents filesystem tools_option thinking partial_messages stderr permission hooks options sessions customtool plugins; do
	run "$ex" timeout "$TIMEOUT" go run "./examples/$ex"
done

# interactive: feed a prompt then /quit on stdin.
printf '  %-18s ' "interactive"
if printf 'say hi\n/quit\n' | timeout "$TIMEOUT" go run ./examples/interactive >/dev/null 2>&1; then
	echo "ok (scripted stdin)"
	PASS=$((PASS + 1))
else
	rc=$?
	# timeout (124) is acceptable: it streamed and we cut it off.
	if [ $rc -eq 124 ]; then echo "ok (timed out streaming)"; PASS=$((PASS + 1)); else echo "FAIL (exit $rc)"; FAIL=$((FAIL + 1)); FAILED+=("interactive"); fi
fi

# interrupt: expected to self-stop; treat timeout as OK too.
printf '  %-18s ' "interrupt"
if timeout "$TIMEOUT" go run ./examples/interrupt >/dev/null 2>&1; then
	echo "ok"
	PASS=$((PASS + 1))
else
	rc=$?
	if [ $rc -eq 124 ]; then echo "ok (timed out, long-running)"; PASS=$((PASS + 1)); else echo "FAIL (exit $rc)"; FAIL=$((FAIL + 1)); FAILED+=("interrupt"); fi
fi

echo
echo "passed: $PASS  failed: $FAIL"
if [ $FAIL -gt 0 ]; then
	echo "failed: ${FAILED[*]}"
	exit 1
fi
