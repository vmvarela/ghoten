#!/usr/bin/env bash
# Ghoten Action — Run: execute the requested ghoten command
set -euo pipefail

WORKDIR="${GITHUB_WORKSPACE}/${INPUT_WORKING_DIRECTORY}"
if [[ ! -d "$WORKDIR" ]]; then
  echo "::error title=Invalid working directory::Directory '$WORKDIR' does not exist"
  exit 1
fi
cd "$WORKDIR"

COMMAND="${INPUT_COMMAND}"
STDOUT_FILE="${RUNNER_TEMP}/ghoten_stdout.txt"
# Include workspace and working directory in plan file path for uniqueness across matrix jobs
_HASH_INPUT=$(printf '%s:%s' "${INPUT_WORKING_DIRECTORY}" "${INPUT_WORKSPACE:-default}")
PLAN_DIR_HASH=$(printf '%s' "$_HASH_INPUT" | md5sum 2>/dev/null | cut -c1-8 \
  || printf '%s' "$_HASH_INPUT" | md5 2>/dev/null | cut -c1-8 \
  || printf '%s' "$_HASH_INPUT" | cksum | cut -d' ' -f1)
PLAN_FILE="${RUNNER_TEMP}/ghoten_${PLAN_DIR_HASH}.tfplan"
START_TIME=$(date +%s)

# ─── Build variable arguments ────────────────────────────────────────────────
VAR_ARGS=()
if [[ -n "${INPUT_VAR_FILE:-}" ]]; then
  VAR_ARGS+=(-var-file="${INPUT_VAR_FILE}")
fi
if [[ -n "${INPUT_VARIABLES:-}" ]]; then
  while IFS= read -r line; do
    line=$(printf '%s\n' "$line" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    [[ -z "$line" || "$line" == \#* ]] && continue
    VAR_ARGS+=(-var "$line")
  done <<< "${INPUT_VARIABLES}"
fi

# Parse extra args
EXTRA_ARGS=()
if [[ -n "${INPUT_ARGS:-}" ]]; then
  # Intentional word splitting: INPUT_ARGS may contain multiple space-separated arguments
  # shellcheck disable=SC2086
  read -ra EXTRA_ARGS <<< "${INPUT_ARGS}"
fi

# ─── Execute command ──────────────────────────────────────────────────────────
EXIT_CODE=0

case "$COMMAND" in
  # ── Plan ──────────────────────────────────────────────────────────────────
  plan)
    CMD=(ghoten plan -input=false -no-color -detailed-exitcode -out="$PLAN_FILE")
    CMD+=("${VAR_ARGS[@]}" "${EXTRA_ARGS[@]}")

    echo "::group::📋 ghoten plan"
    set +e
    "${CMD[@]}" 2>&1 | tee "$STDOUT_FILE"
    EXIT_CODE=${PIPESTATUS[0]}
    set -e
    echo "::endgroup::"

    # detailed-exitcode: 0=no changes, 1=error, 2=changes
    if [[ $EXIT_CODE -eq 0 ]]; then
      echo "plan_has_changes=false" >> "$GITHUB_OUTPUT"
      echo "plan_file=${PLAN_FILE}" >> "$GITHUB_OUTPUT"
      echo ""
      echo "✅ No changes. Infrastructure is up-to-date."
    elif [[ $EXIT_CODE -eq 2 ]]; then
      echo "plan_has_changes=true" >> "$GITHUB_OUTPUT"
      echo "plan_file=${PLAN_FILE}" >> "$GITHUB_OUTPUT"
      EXIT_CODE=0
      echo ""
      echo "🔄 Changes detected — review the plan above."
    else
      echo "plan_has_changes=false" >> "$GITHUB_OUTPUT"
      echo "plan_file=" >> "$GITHUB_OUTPUT"
      echo "::error title=Ghoten Plan::Plan failed (exit code ${EXIT_CODE})"
    fi
    ;;

  # ── Apply ─────────────────────────────────────────────────────────────────
  apply)
    CMD=(ghoten apply -input=false -no-color)

    # Auto-detect plan file from a previous plan step
    if [[ -f "$PLAN_FILE" ]]; then
      CMD+=("$PLAN_FILE")
      echo "📎 Using plan file from previous step"
    else
      CMD+=(-auto-approve "${VAR_ARGS[@]}")
    fi
    CMD+=("${EXTRA_ARGS[@]}")

    echo "::group::🚀 ghoten apply"
    set +e
    "${CMD[@]}" 2>&1 | tee "$STDOUT_FILE"
    EXIT_CODE=${PIPESTATUS[0]}
    set -e
    echo "::endgroup::"

    if [[ $EXIT_CODE -ne 0 ]]; then
      echo "::error title=Ghoten Apply::Apply failed (exit code ${EXIT_CODE})"
    else
      echo ""
      echo "✅ Apply complete!"
    fi
    ;;

  # ── Destroy ───────────────────────────────────────────────────────────────
  destroy)
    CMD=(ghoten apply -destroy -input=false -no-color -auto-approve)
    CMD+=("${VAR_ARGS[@]}" "${EXTRA_ARGS[@]}")

    echo "::group::🗑️ ghoten destroy"
    set +e
    "${CMD[@]}" 2>&1 | tee "$STDOUT_FILE"
    EXIT_CODE=${PIPESTATUS[0]}
    set -e
    echo "::endgroup::"

    if [[ $EXIT_CODE -ne 0 ]]; then
      echo "::error title=Ghoten Destroy::Destroy failed (exit code ${EXIT_CODE})"
    else
      echo ""
      echo "✅ Destroy complete!"
    fi
    ;;

  # ── Format ────────────────────────────────────────────────────────────────
  fmt)
    CMD=(ghoten fmt -no-color -recursive)
    if [[ "${INPUT_FMT_CHECK}" == "true" ]]; then
      CMD+=(-check -diff)
    fi
    CMD+=("${EXTRA_ARGS[@]}")

    echo "::group::🎨 ghoten fmt"
    set +e
    "${CMD[@]}" 2>&1 | tee "$STDOUT_FILE"
    EXIT_CODE=${PIPESTATUS[0]}
    set -e
    echo "::endgroup::"

    if [[ $EXIT_CODE -ne 0 ]]; then
      echo "fmt_result=true" >> "$GITHUB_OUTPUT"
      if [[ "${INPUT_FMT_CHECK}" == "true" ]]; then
        echo "::warning title=Ghoten Fmt::Formatting differences detected"
      fi
    else
      echo "fmt_result=false" >> "$GITHUB_OUTPUT"
      echo "✅ All files properly formatted"
    fi
    ;;

  # ── Validate ──────────────────────────────────────────────────────────────
  validate)
    CMD=(ghoten validate -no-color)
    CMD+=("${EXTRA_ARGS[@]}")

    echo "::group::✔️ ghoten validate"
    set +e
    "${CMD[@]}" 2>&1 | tee "$STDOUT_FILE"
    EXIT_CODE=${PIPESTATUS[0]}
    set -e
    echo "::endgroup::"

    if [[ $EXIT_CODE -ne 0 ]]; then
      echo "::error title=Ghoten Validate::Validation failed"
    else
      echo "✅ Configuration is valid"
    fi
    ;;

  *)
    echo "::error title=Ghoten::Unknown command '${COMMAND}'. Use: plan, apply, destroy, fmt, validate"
    exit 1
    ;;
esac

# ─── Outputs ──────────────────────────────────────────────────────────────────
END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))
echo "$DURATION" > "${RUNNER_TEMP}/ghoten_duration.txt"

echo "exitcode=${EXIT_CODE}" >> "$GITHUB_OUTPUT"

# Strip noisy refresh/read progress lines from the captured output so that
# PR comments and Job Summaries stay readable on large states.
# The raw step log (written via tee above) is always preserved in full.
# Patterns removed:
#   "<resource>: Refreshing state... [id=...]"
#   "<resource>: Still refreshing... [Xs elapsed]"
#   "<resource>: Reading..."
#   "<resource>: Still reading... [Xs elapsed]"
#   "<resource>: Read complete after Xs [id=...]"
if [[ -f "$STDOUT_FILE" ]]; then
  FILTERED_FILE="${RUNNER_TEMP}/ghoten_stdout_filtered.txt"
  grep -vE ': (Refreshing state\.\.\.|Still refreshing\.\.\.|Reading\.\.\.|Still reading\.\.\.|Read complete after )' \
    "$STDOUT_FILE" > "$FILTERED_FILE" || true
  {
    echo "stdout<<GHOTEN_STDOUT_EOF"
    cat "$FILTERED_FILE"
    echo "GHOTEN_STDOUT_EOF"
  } >> "$GITHUB_OUTPUT"
  # Replace original so comment.sh and summary.sh also read filtered output
  mv "$FILTERED_FILE" "$STDOUT_FILE"
fi

exit "$EXIT_CODE"
