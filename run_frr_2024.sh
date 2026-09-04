#!/usr/bin/env bash
set -Eeuo pipefail

if [[ -f .env ]]; then
  set -a
  source .env
  set +a
fi

RUN_ROOT="$PWD/rebuilt_20260821"
CONFIG="$RUN_ROOT/config/frr_heuristics.yaml"
RAW="$RUN_ROOT/data/raw_prs.jsonl"
BATCHES="$RUN_ROOT/data/correlation_batches"
CORRELATED="$RUN_ROOT/data/correlated.jsonl"
CANDIDATES="$RUN_ROOT/data/candidates.jsonl"
OUTPUT="$RUN_ROOT/output"
OBSERVATION_END="2026-08-21T00:00:00Z"

mkdir -p \
  "$RUN_ROOT/config" \
  "$RUN_ROOT/cache" \
  "$RUN_ROOT/data/repos/FRRouting" \
  "$RUN_ROOT/logs" \
  "$OUTPUT"

exec > >(tee -a "$RUN_ROOT/logs/pipeline.log") 2>&1

trap 'status=$?; echo "PIPELINE FAILED: exit=$status line=$LINENO at $(date -u --iso-8601=seconds)"; exit "$status"' ERR

if [[ -z "${GITHUB_TOKEN:-}" ]]; then
  echo "ERROR: GITHUB_TOKEN is not loaded in this tmux session"
  exit 1
fi

if [[ ! -f "$CONFIG" ]]; then
  cp configs/frr_heuristics.yaml "$CONFIG"
fi

git rev-parse HEAD > "$RUN_ROOT/miner_commit.txt"

echo "Pipeline started: $(date -u --iso-8601=seconds)"
echo "Miner commit: $(cat "$RUN_ROOT/miner_commit.txt")"
echo "Observation cutoff: $OBSERVATION_END"

mkdir -p bin
go build -o bin/miner .

echo "===== COLLECT ====="
./bin/miner collect \
  --config "$CONFIG" \
  --repo FRRouting/frr \
  --from 2024-01-01 \
  --to 2024-12-31 \
  --observation-end "$OBSERVATION_END" \
  --cache-dir "$RUN_ROOT/cache" \
  --git-dir "$RUN_ROOT/data/repos/FRRouting/frr.git" \
  --out "$RAW"

echo "===== CORRELATE IN BATCHES ====="
./bin/miner correlate \
  --config "$CONFIG" \
  --repo FRRouting/frr \
  --in "$RAW" \
  --git-dir "$RUN_ROOT/data/repos/FRRouting/frr.git" \
  --observation-end "$OBSERVATION_END" \
  --batch-dir "$BATCHES" \
  --batch-size 50 \
  --workers 4

echo "===== VALIDATE AND FINALIZE BATCHES ====="
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

echo "===== EXPORT 17-PR RESEARCH COHORT ====="
./bin/miner export \
  --config "$CONFIG" \
  --in "$CANDIDATES" \
  --format jsonl \
  --min-score 0.30 \
  --require-fix-signal \
  --out "$OUTPUT/frr_2024_benchmark_candidates.jsonl"

./bin/miner export \
  --config "$CONFIG" \
  --in "$CANDIDATES" \
  --format markdown \
  --min-score 0.30 \
  --require-fix-signal \
  --out "$OUTPUT/frr_2024_benchmark_candidates.md"

./bin/miner export \
  --config "$CONFIG" \
  --in "$CANDIDATES" \
  --format csv \
  --min-score 0.30 \
  --require-fix-signal \
  --out "$OUTPUT/frr_2024_benchmark_candidates.csv"

echo "===== VALIDATION SUMMARY ====="
./bin/miner stats \
  --in "$CANDIDATES" |
  tee "$OUTPUT/stats.txt"

./bin/miner inspect \
  --in "$CANDIDATES" \
  --pr 16194 |
  tee "$OUTPUT/pr_16194.txt"

wc -l \
  "$RAW" \
  "$CORRELATED" \
  "$CANDIDATES" \
  "$OUTPUT/frr_2024_benchmark_candidates.jsonl"

sha256sum \
  "$RAW" \
  "$CORRELATED" \
  "$CANDIDATES" \
  "$OUTPUT/frr_2024_benchmark_candidates.jsonl" \
  > "$OUTPUT/SHA256SUMS"

echo "Pipeline completed: $(date -u --iso-8601=seconds)"
