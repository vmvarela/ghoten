#!/usr/bin/env bash
set -euo pipefail
: "${TAG:?}" "${SOURCE_SHA:?}" "${IS_PRERELEASE:?}" "${GITHUB_REPOSITORY:?}"
# Assets are staged and verified while private; publication is the last operation.
RELEASES="$(gh api "repos/$GITHUB_REPOSITORY/releases?per_page=100")"
DRAFT_ID="$(jq -r --arg tag "$TAG" '.[] | select(.draft and .tag_name == $tag) | .id' <<<"$RELEASES")"
if [[ -z "$DRAFT_ID" && "$IS_PRERELEASE" == false ]]; then
  DRAFT_ID="$(jq -r '.[] | select(.draft and .name == "Next Ghoten release") | .id' <<<"$RELEASES")"
fi
if [[ -n "$DRAFT_ID" ]]; then
  [[ "$DRAFT_ID" =~ ^[0-9]+$ ]]
  gh api --method PATCH "repos/$GITHUB_REPOSITORY/releases/$DRAFT_ID" \
    -f tag_name="$TAG" -f target_commitish="$SOURCE_SHA" -f name="$TAG" \
    -F draft=true -F prerelease="$IS_PRERELEASE" >/dev/null
else
  if [[ "$IS_PRERELEASE" == true ]]; then
    gh release create "$TAG" --repo "$GITHUB_REPOSITORY" --target "$SOURCE_SHA" \
      --title "$TAG" --generate-notes --draft --prerelease
  else
    gh release create "$TAG" --repo "$GITHUB_REPOSITORY" --target "$SOURCE_SHA" \
      --title "$TAG" --generate-notes --draft
  fi
  DRAFT_ID="$(gh release view "$TAG" --repo "$GITHUB_REPOSITORY" --json databaseId --jq .databaseId)"
fi
# A retried draft must not retain stale assets from an earlier attempt.
gh api --paginate "repos/$GITHUB_REPOSITORY/releases/$DRAFT_ID/assets" --jq '.[].id' |
  while read -r asset; do
    gh api --method DELETE "repos/$GITHUB_REPOSITORY/releases/assets/$asset"
  done

gh release upload "$TAG" artifacts/* --repo "$GITHUB_REPOSITORY"
VERIFY_DIR="$(mktemp -d)"
trap 'rm -rf "$VERIFY_DIR"' EXIT
gh release download "$TAG" --repo "$GITHUB_REPOSITORY" --dir "$VERIFY_DIR"
# Compare the manifest itself, then all uploaded bytes and the exact file set.
cmp artifacts/sha256sums.txt "$VERIFY_DIR/sha256sums.txt"
(cd "$VERIFY_DIR" && sha256sum --check sha256sums.txt)
diff <(find artifacts -maxdepth 1 -type f -printf '%f\n' | sort) \
     <(find "$VERIFY_DIR" -maxdepth 1 -type f -printf '%f\n' | sort)
# Publication must only proceed if the draft is still exactly what we staged.
DRAFT_STATE="$(gh api "repos/$GITHUB_REPOSITORY/releases/$DRAFT_ID")"
if [[ "$(jq -r '.draft' <<<"$DRAFT_STATE")" != "true" ]] \
  || [[ "$(jq -r '.prerelease' <<<"$DRAFT_STATE")" != "$IS_PRERELEASE" ]] \
  || [[ "$(jq -r '.tag_name' <<<"$DRAFT_STATE")" != "$TAG" ]] \
  || [[ "$(jq -r '.name' <<<"$DRAFT_STATE")" != "$TAG" ]] \
  || [[ "$(jq -r '.target_commitish' <<<"$DRAFT_STATE")" != "$SOURCE_SHA" ]]; then
  echo "::error::Draft identity changed before publication; refusing to publish." >&2
  exit 1
fi
LATEST=true
[[ "$IS_PRERELEASE" == false ]] || LATEST=false
# The final write is the publication and reaffirms the staged identity.
gh api --method PATCH "repos/$GITHUB_REPOSITORY/releases/$DRAFT_ID" \
  -f tag_name="$TAG" -f target_commitish="$SOURCE_SHA" -f name="$TAG" \
  -F draft=false -F prerelease="$IS_PRERELEASE" -f make_latest="$LATEST" >/dev/null
