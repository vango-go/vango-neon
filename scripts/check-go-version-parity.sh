#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
go_mod="${repo_root}/go.mod"
workflow="${repo_root}/.github/workflows/integration.yml"
spec="${repo_root}/NEON_INTEGRATION_SPEC.md"
build_plan="${repo_root}/PHASED_BUILD_PLAN.md"

fail() {
	echo "go-version parity check failed: $*" >&2
	exit 1
}

go_line="$(grep -E '^go [0-9]+\.[0-9]+(\.[0-9]+)?$' "${go_mod}" | head -n 1 || true)"
[ -n "${go_line}" ] || fail "could not find go directive in ${go_mod}"

go_full="${go_line#go }"
go_major_minor="$(echo "${go_full}" | sed -E 's/^([0-9]+\.[0-9]+).*/\1/')"
expected_ci="${go_major_minor}.x"
expected_workflow_line="go-version: \"${expected_ci}\""

if ! grep -Fq "${expected_workflow_line}" "${workflow}"; then
	echo "expected workflow line: ${expected_workflow_line}" >&2
	echo "actual workflow go-version lines:" >&2
	grep -n 'go-version:' "${workflow}" >&2 || true
	fail "workflow go-version is out of sync with go.mod (${go_full})"
fi

if ! grep -Fq "${expected_workflow_line}" "${spec}"; then
	echo "expected spec line: ${expected_workflow_line}" >&2
	echo "actual spec go-version lines:" >&2
	grep -n 'go-version:' "${spec}" >&2 || true
	fail "spec go-version snippet is out of sync with go.mod (${go_full})"
fi

if grep -En 'go-version:[[:space:]]*"1\.22"|[Gg]o 1\.22' "${spec}" "${build_plan}" >/dev/null; then
	echo "stale Go 1.22 references detected:" >&2
	grep -En 'go-version:[[:space:]]*"1\.22"|[Gg]o 1\.22' "${spec}" "${build_plan}" >&2 || true
	fail "remove stale Go 1.22 references from package spec/plan docs"
fi

echo "go-version parity check passed (go.mod=${go_full}, expected_ci=${expected_ci})"
