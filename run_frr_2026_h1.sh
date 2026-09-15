#!/usr/bin/env bash
# H1 data run for FRRouting/frr, per docs/h1-pre-registration.md §3 and the
# frozen arguments recorded here. Idempotent by cache: rerunning after an
# interruption resumes; completed batches are verified and skipped.
#
# Requires GITHUB_TOKEN (via .env in the repo root or the environment) for the
# collect and for contamination-keys; every later stage is offline.
set -Eeuo pipefail

if [[ -f .env ]]; then
  set -a
  source .env
  set +a
fi

RUN_ROOT="${RUN_ROOT:-/home/alex/data/FRR2026H1}"
CONFIG="$RUN_ROOT/config/frr_h1_2026.yaml"
MIRROR="$RUN_ROOT/data/repos/FRRouting/frr.git"
RAW="$RUN_ROOT/data/raw_prs.jsonl"
BATCHES="$RUN_ROOT/data/correlation_batches"
CORRELATED="$RUN_ROOT/data/correlated.jsonl"
CANDIDATES="$RUN_ROOT/data/candidates.jsonl"
OUTPUT="$RUN_ROOT/output"
COHORT_NAME="frr-2026-h1-v1"
FROM="2026-01-01"
TO="2026-06-30"
MIN_FIX_DATE="2026-06-01"

mkdir -p "$RUN_ROOT/logs" "$OUTPUT" "$RUN_ROOT/data"
exec > >(tee -a "$RUN_ROOT/logs/pipeline.log") 2>&1
trap 'status=$?; echo "PIPELINE FAILED: exit=$status line=$LINENO at $(date -u --iso-8601=seconds)"; exit "$status"' ERR

if [[ -z "${GITHUB_TOKEN:-}" ]]; then
  echo "ERROR: GITHUB_TOKEN is not set (put it in .env at the repo root, which is gitignored)"
  exit 1
fi
if [[ ! -f "$RUN_ROOT/logs/mirror-clone-finished.txt" ]]; then
  echo "ERROR: mirror clone has not finished ($RUN_ROOT/logs/mirror-clone-finished.txt missing)"
  exit 1
fi

# observation_end is the mirror's fetch time, recorded when the clone finished.
OBSERVATION_END="$(cat "$RUN_ROOT/logs/mirror-clone-finished.txt")"
MIRROR_HEAD="$(git --git-dir="$MIRROR" rev-parse HEAD)"

echo "Pipeline started: $(date -u --iso-8601=seconds)"
git rev-parse HEAD > "$RUN_ROOT/miner_commit.txt"
echo "Miner commit: $(cat "$RUN_ROOT/miner_commit.txt")"
echo "Merge window: $FROM..$TO  observation_end: $OBSERVATION_END  mirror HEAD: $MIRROR_HEAD"

mkdir -p bin
go build -o bin/miner ./cmd/miner

echo "===== COLLECT ====="
./bin/miner collect \
  --config "$CONFIG" \
  --repo FRRouting/frr \
  --from "$FROM" \
  --to "$TO" \
  --observation-end "$OBSERVATION_END" \
  --cache-dir "$RUN_ROOT/cache" \
  --git-dir "$MIRROR" \
  --out "$RAW"

echo "===== BUNDLE COMPLETENESS (reported before anything else is built) ====="
python3 - "$RUN_ROOT/cache/github/FRRouting_frr/prs" <<'PY' | tee "$OUTPUT/bundle_completeness.txt"
import glob, json, sys
n = be = ic = rc = ie = 0
for p in glob.glob(sys.argv[1] + "/pr_*.json"):
    d = json.load(open(p)); n += 1
    be += bool(d.get("body_edits_complete")); ic += bool(d.get("issue_comments_complete"))
    rc += bool(d.get("review_comments_complete")); ie += bool(d.get("issue_events_complete"))
print(f"bundles={n} body_edits_complete={be} issue_comments_complete={ic} review_comments_complete={rc} issue_events_complete={ie}")
PY

echo "===== CORRELATE IN BATCHES ====="
./bin/miner correlate \
  --config "$CONFIG" \
  --repo FRRouting/frr \
  --in "$RAW" \
  --git-dir "$MIRROR" \
  --observation-end "$OBSERVATION_END" \
  --batch-dir "$BATCHES" \
  --batch-size 50 \
  --workers 4

echo "===== FINALIZE BATCHES ====="
./bin/miner finalize-batches \
  --config "$CONFIG" \
  --in "$RAW" \
  --batch-dir "$BATCHES" \
  --out "$CORRELATED"

echo "===== SCORE ====="
./bin/miner score \
  --config "$CONFIG" \
  --in "$CORRELATED" \
  --out "$CANDIDATES"

echo "===== STATS AND SELECTION REPORT ====="
./bin/miner stats --in "$CANDIDATES" | tee "$OUTPUT/stats.txt"
./bin/miner cohort-report --input "$CANDIDATES" --repo "$MIRROR" --out "$OUTPUT/cohort-report.md"
./bin/miner cohort-report --input "$CANDIDATES" --format csv --out "$OUTPUT/cohort-report.csv"

echo "===== PROVISIONAL COHORT (sampler-selected positives; the freeze needs --positives) ====="
./bin/miner cohort-export \
  --input "$CANDIDATES" \
  --cache-dir "$RUN_ROOT/cache" \
  --name "$COHORT_NAME-provisional" \
  --positive-signals strong \
  --min-score 0 \
  --min-exposure-days 0 \
  --min-fix-date "$MIN_FIX_DATE" \
  --out "$OUTPUT/$COHORT_NAME-provisional"

echo "===== YIELD ====="
python3 - "$OUTPUT/$COHORT_NAME-provisional/cohort-manifest.json" <<'PY' | tee "$OUTPUT/yield.txt"
import json, sys, collections
m = json.load(open(sys.argv[1]))
print("pairs", len(m["pairs"]), "unmatched_positives", len(m["unmatched_positives"]))
print("exclusions", json.dumps(m["exclusions"]))
print("admission", json.dumps(m.get("admission")))
by = collections.Counter(p["cell"]["subsystem"] for p in m["pairs"])
print("pairs by subsystem", dict(sorted(by.items())))
PY

echo "Pipeline finished: $(date -u --iso-8601=seconds)"
cat <<EOT
Next, evaluator-side (exact commands, also in $RUN_ROOT/evaluator/COMMANDS.md):
  1. Read the positives (docs/h1-fitness-review-miner.md M4); write accepted PR numbers one per line to $RUN_ROOT/evaluator/positives.txt
  2. Freeze:
     ./bin/miner cohort-export --input $CANDIDATES --cache-dir $RUN_ROOT/cache --name $COHORT_NAME --positive-signals strong --min-score 0 --min-exposure-days 0 --min-fix-date $MIN_FIX_DATE --positives "\$(grep -vE '^\s*(#|\$)' $RUN_ROOT/evaluator/positives.txt | paste -sd,)" --out $RUN_ROOT/evaluator/$COHORT_NAME
  3. Verify (engine from a clean clone of a named commit, never the engine checkout):
     ./bin/miner cohort-verify --input $CANDIDATES --repo $MIRROR --cache-dir $RUN_ROOT/cache --prs "\$(python3 -c "import json; m=json.load(open('$RUN_ROOT/evaluator/$COHORT_NAME/cohort-manifest.json')); print(','.join(str(n) for p in m['pairs'] for n in (p['positive'], p['negative'])))")" --metadata-version v2 --out $RUN_ROOT/evaluator/$COHORT_NAME-bundles --bench-repo <clean clone of engine-runner at the agreed SHA>
  4. Publish for engine-coder (copy, never symlink; bundles only):
     rsync -a --exclude='case-*-evaluator-only/' --include='case-*/' --include='case-*/**' --exclude='*' $RUN_ROOT/evaluator/$COHORT_NAME-bundles/ /home/alex/repos/miner/output/frr-h1-cohort/
  5. Keys (oracle-grade):
     ./bin/miner contamination-keys --input $RUN_ROOT/evaluator/$COHORT_NAME/cohort.jsonl --cache-dir $RUN_ROOT/cache --git-dir $MIRROR --out $RUN_ROOT/evaluator/$COHORT_NAME-keys
  6. Arm H:
     ./bin/miner history-baseline --input $RUN_ROOT/evaluator/$COHORT_NAME/cohort.jsonl --git-dir $MIRROR --out $RUN_ROOT/evaluator/$COHORT_NAME-history
EOT
