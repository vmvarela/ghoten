#!/usr/bin/env bash
# Ghoten Action — Init: initialize Ghoten with ORAS backend
set -euo pipefail

cd "${GITHUB_WORKSPACE}/${INPUT_WORKING_DIRECTORY}"

# ─── Environment ──────────────────────────────────────────────────────────────
echo "TF_IN_AUTOMATION=true" >> "$GITHUB_ENV"
export TF_IN_AUTOMATION=true

# Workspace
if [[ -n "${INPUT_WORKSPACE}" && "${INPUT_WORKSPACE}" != "default" ]]; then
  echo "TF_WORKSPACE=${INPUT_WORKSPACE}" >> "$GITHUB_ENV"
  export TF_WORKSPACE="${INPUT_WORKSPACE}"
fi

# Backend repository — auto-compute if not set
BACKEND_REPO="${INPUT_BACKEND_REPOSITORY:-}"
if [[ -z "$BACKEND_REPO" ]]; then
  REPO_OWNER=$(echo "${GITHUB_REPOSITORY_OWNER}" | tr '[:upper:]' '[:lower:]')
  REPO_NAME=$(echo "${GITHUB_REPOSITORY#*/}" | tr '[:upper:]' '[:lower:]')
  BACKEND_REPO="ghcr.io/${REPO_OWNER}/tf-state.${REPO_NAME}"
fi
echo "TF_BACKEND_ORAS_REPOSITORY=${BACKEND_REPO}" >> "$GITHUB_ENV"
export TF_BACKEND_ORAS_REPOSITORY="${BACKEND_REPO}"

# Persist for summary.sh
echo "$BACKEND_REPO" > "${RUNNER_TEMP}/ghoten_backend_repo.txt"

# ─── Build init command ───────────────────────────────────────────────────────
CMD=(ghoten init -input=false -no-color)

# ORAS backend-config defaults
if [[ -n "${INPUT_COMPRESSION}" ]]; then
  CMD+=(-backend-config="compression=${INPUT_COMPRESSION}")
fi
if [[ -n "${INPUT_LOCK_TTL}" ]]; then
  CMD+=(-backend-config="lock_ttl=${INPUT_LOCK_TTL}")
fi
if [[ -n "${INPUT_MAX_VERSIONS}" ]]; then
  CMD+=(-backend-config="max_versions=${INPUT_MAX_VERSIONS}")
fi

# Additional backend-config from user
if [[ -n "${INPUT_BACKEND_CONFIG:-}" ]]; then
  while IFS= read -r line; do
    # Trim leading/trailing whitespace precisely (preserves internal whitespace in values)
    line=$(printf '%s\n' "$line" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    [[ -z "$line" || "$line" == \#* ]] && continue
    CMD+=(-backend-config="$line")
  done <<< "${INPUT_BACKEND_CONFIG}"
fi

# Additional init args
if [[ -n "${INPUT_INIT_ARGS:-}" ]]; then
  # shellcheck disable=SC2086
  read -ra EXTRA <<< "${INPUT_INIT_ARGS}"
  CMD+=("${EXTRA[@]}")
fi

# ─── Run init ─────────────────────────────────────────────────────────────────
INIT_OUTPUT="${RUNNER_TEMP}/ghoten_init.txt"

echo "::group::🔧 Initializing Ghoten"
echo "Backend:   ${BACKEND_REPO}"
echo "Workspace: ${INPUT_WORKSPACE:-default}"
echo "Directory: ${INPUT_WORKING_DIRECTORY}"
echo "Command:   ${CMD[*]}"
echo ""

set +e
"${CMD[@]}" 2>&1 | tee "$INIT_OUTPUT"
INIT_EXIT=${PIPESTATUS[0]}
set -e

echo "::endgroup::"

if [[ $INIT_EXIT -ne 0 ]]; then
  echo "::error title=Ghoten Init::Initialization failed (exit code ${INIT_EXIT})"
  exit "$INIT_EXIT"
fi

echo "✅ Initialization complete"
