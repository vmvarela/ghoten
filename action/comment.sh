#!/usr/bin/env bash
# Ghoten Action — Comment: post/update PR comment with command output
set -euo pipefail

COMMAND="${INPUT_COMMAND}"
WORKING_DIR="${INPUT_WORKING_DIRECTORY}"
WORKSPACE="${INPUT_WORKSPACE:-default}"
TOKEN="${INPUT_GITHUB_TOKEN}"
EXIT_CODE="${COMMAND_EXITCODE:-0}"
CHANGES="${PLAN_HAS_CHANGES:-false}"
FMT="${FMT_RESULT:-false}"

# ─── Prerequisites ────────────────────────────────────────────────────────────
if ! command -v curl &>/dev/null; then
  echo "::warning title=Ghoten Comment::curl not found — skipping PR comment"
  exit 0
fi
if ! command -v jq &>/dev/null; then
  echo "::warning title=Ghoten Comment::jq not found — skipping PR comment"
  exit 0
fi

# ─── PR context ───────────────────────────────────────────────────────────────
PR_NUMBER=$(jq -r '.pull_request.number // empty' "$GITHUB_EVENT_PATH" 2>/dev/null || true)
if [[ -z "$PR_NUMBER" ]]; then
  echo "::warning title=Ghoten Comment::Could not determine PR number — skipping"
  exit 0
fi

# ─── Build comment ────────────────────────────────────────────────────────────
MARKER="<!-- ghoten:${WORKING_DIR}:${WORKSPACE}:${COMMAND} -->"

STDOUT_FILE="${RUNNER_TEMP}/ghoten_stdout.txt"
OUTPUT=""
[[ -f "$STDOUT_FILE" ]] && OUTPUT=$(cat "$STDOUT_FILE")

# Status line
case "$COMMAND" in
  plan)
    if [[ "$EXIT_CODE" != "0" ]]; then
      EMOJI="❌"; STATUS="Error"
    elif [[ "$CHANGES" == "true" ]]; then
      EMOJI="🔄"; STATUS="Changes detected"
    else
      EMOJI="✅"; STATUS="No changes"
    fi
    ;;
  apply)
    if [[ "$EXIT_CODE" != "0" ]]; then
      EMOJI="❌"; STATUS="Apply failed"
    else
      EMOJI="✅"; STATUS="Applied successfully"
    fi
    ;;
  destroy)
    if [[ "$EXIT_CODE" != "0" ]]; then
      EMOJI="❌"; STATUS="Destroy failed"
    else
      EMOJI="🗑️"; STATUS="Destroyed"
    fi
    ;;
  fmt)
    if [[ "$FMT" == "true" ]]; then
      EMOJI="⚠️"; STATUS="Formatting needed"
    else
      EMOJI="✅"; STATUS="Properly formatted"
    fi
    ;;
  validate)
    if [[ "$EXIT_CODE" != "0" ]]; then
      EMOJI="❌"; STATUS="Validation failed"
    else
      EMOJI="✅"; STATUS="Valid"
    fi
    ;;
esac

# Labels
TITLE_PARTS="${EMOJI} Ghoten \`${COMMAND}\`"
[[ "$WORKING_DIR" != "." ]] && TITLE_PARTS="${TITLE_PARTS} · \`${WORKING_DIR}\`"
[[ "$WORKSPACE" != "default" ]] && TITLE_PARTS="${TITLE_PARTS} · \`${WORKSPACE}\`"

# Truncate output (GitHub comment limit ≈ 65,536 chars)
MAX_OUTPUT=55000
TRUNCATED_NOTE=""
if [[ ${#OUTPUT} -gt $MAX_OUTPUT ]]; then
  OUTPUT="${OUTPUT:0:$MAX_OUTPUT}"
  TRUNCATED_NOTE=$'\n\n> ⚠️ Output truncated. See the [full log]('"${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}"').'
fi

# Change summary line for plan
CHANGE_LINE=""
if [[ "$COMMAND" == "plan" && "$CHANGES" == "true" ]]; then
  ADD=$(grep -oE '[0-9]+ to add' <<< "$OUTPUT" | grep -oE '[0-9]+' || echo "0")
  CHG=$(grep -oE '[0-9]+ to change' <<< "$OUTPUT" | grep -oE '[0-9]+' || echo "0")
  DEL=$(grep -oE '[0-9]+ to destroy' <<< "$OUTPUT" | grep -oE '[0-9]+' || echo "0")
  CHANGE_LINE=$'\n\n**'"+${ADD}"'** add · **'"\~${CHG}"'** change · **'"-${DEL}"'** destroy'
fi

# Short SHA and commit link
SHORT_SHA="${GITHUB_SHA:0:7}"
COMMIT_URL="${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/commit/${GITHUB_SHA}"
RUN_URL="${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}"

# Compose body
BODY=$(printf '%s\n' \
  "${MARKER}" \
  "### ${TITLE_PARTS} — ${STATUS}${CHANGE_LINE}" \
  "" \
  "<details><summary>Show Output</summary>" \
  "" \
  "\`\`\`hcl" \
  "${OUTPUT}" \
  "\`\`\`" \
  "${TRUNCATED_NOTE}" \
  "</details>" \
  "" \
  "<sub>Triggered by @${GITHUB_ACTOR} in <a href=\"${COMMIT_URL}\"><code>${SHORT_SHA}</code></a> · <a href=\"${RUN_URL}\">${GITHUB_WORKFLOW} #${GITHUB_RUN_NUMBER}</a></sub>")

# ─── Post/update comment ─────────────────────────────────────────────────────
REPO="${GITHUB_REPOSITORY}"
API_URL="${GITHUB_API_URL}/repos/${REPO}/issues/${PR_NUMBER}/comments"
AUTH_HEADER="Authorization: Bearer ${TOKEN}"
ACCEPT_HEADER="Accept: application/vnd.github+json"
API_VERSION="X-GitHub-Api-Version: 2022-11-28"

if [[ -z "$TOKEN" ]]; then
  echo "::warning title=Ghoten Comment::github-token is empty — skipping PR comment"
  exit 0
fi

# Search for existing comment (check up to 10 pages = 1000 comments)
EXISTING_ID=""
for PAGE in $(seq 1 10); do
  RESPONSE=$(curl -fsSL \
    -H "$AUTH_HEADER" -H "$ACCEPT_HEADER" -H "$API_VERSION" \
    "${API_URL}?per_page=100&page=${PAGE}" 2>/dev/null) || break

  FOUND=$(echo "$RESPONSE" | jq -r --arg m "$MARKER" \
    '.[] | select(.body | contains($m)) | .id' | head -1)

  if [[ -n "$FOUND" && "$FOUND" != "null" ]]; then
    EXISTING_ID="$FOUND"
    break
  fi

  # Stop if fewer than 100 results (last page)
  COUNT=$(echo "$RESPONSE" | jq 'length')
  [[ "$COUNT" -lt 100 ]] && break
done

JSON_BODY=$(jq -n --arg body "$BODY" '{body: $body}')

if [[ -n "$EXISTING_ID" ]]; then
  curl -fsSL -X PATCH \
    -H "$AUTH_HEADER" -H "$ACCEPT_HEADER" -H "$API_VERSION" \
    "${GITHUB_API_URL}/repos/${REPO}/issues/comments/${EXISTING_ID}" \
    -d "$JSON_BODY" > /dev/null 2>&1 || {
      echo "::warning title=Ghoten Comment::Failed to update comment. Check permissions (pull-requests: write)."
      exit 0
    }
  echo "💬 Updated PR comment #${EXISTING_ID}"
else
  curl -fsSL -X POST \
    -H "$AUTH_HEADER" -H "$ACCEPT_HEADER" -H "$API_VERSION" \
    "${API_URL}" \
    -d "$JSON_BODY" > /dev/null 2>&1 || {
      echo "::warning title=Ghoten Comment::Failed to create comment. Check permissions (pull-requests: write)."
      exit 0
    }
  echo "💬 Posted PR comment"
fi
