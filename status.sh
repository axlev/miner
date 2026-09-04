watch -n 5 '
R="rebuilt_20260821"

echo "=== TIME ==="
date -u

echo
echo "=== CURRENT STAGE ==="
grep "^===== " "$R/logs/pipeline.log" 2>/dev/null | tail -1

echo
echo "=== MINER PROCESS ==="
ps -C miner -o pid,etime,%cpu,%mem,rss,vsz,cmd --sort=-%cpu

echo
echo "=== COLLECTION ==="
if [ -f "$R/data/raw_prs.jsonl" ]; then
  echo "Raw PRs: $(wc -l < "$R/data/raw_prs.jsonl")"
else
  echo "Raw output not finalized yet"
fi
echo "Cached files: $(find "$R/cache" -type f 2>/dev/null | wc -l)"
du -sh "$R/cache" "$R/data/repos" 2>/dev/null

echo
echo "=== CORRELATION BATCHES ==="
if [ -f "$R/data/raw_prs.jsonl" ]; then
  RAW=$(wc -l < "$R/data/raw_prs.jsonl")
  TOTAL=$(( (RAW + 49) / 50 ))
  DONE=$(find "$R/data/correlation_batches" -maxdepth 1 -name "*.jsonl" 2>/dev/null | wc -l)
  echo "Completed batches: $DONE / $TOTAL"
  echo "Covered PR capacity: $((DONE * 50)) / $RAW"
else
  echo "Waiting for collection"
fi

echo
echo "=== OUTPUT COUNTS ==="
for F in \
  "$R/data/correlated.jsonl" \
  "$R/data/candidates.jsonl" \
  "$R/output/frr_2024_benchmark_candidates.jsonl"
do
  if [ -f "$F" ]; then
    printf "%-65s %s records\n" "$F" "$(wc -l < "$F")"
  fi
done

echo
echo "=== LAST 15 LOG LINES ==="
tail -n 15 "$R/logs/pipeline.log" 2>/dev/null
'
