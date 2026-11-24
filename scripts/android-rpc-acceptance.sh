#!/usr/bin/env bash
# ADB Android RPC Guest acceptance capture. Explicitly run on a physical phone.
# Does not access app-private state, pair tokens, Host addresses or model content.
set -euo pipefail

usage() {
  echo "Usage: $0 --output DIRECTORY [--serial ADB_SERIAL] [--screen-off-minutes 30|60] [--doze] [--resource-samples N] [--resource-interval SECONDS]" >&2
  exit 2
}
output=""
serial=""
minutes=0
doze=false
samples=1
interval=5
while (( $# > 0 )); do
  case "$1" in
    --output) (( $# >= 2 )) || usage; output="$2"; shift 2 ;;
    --serial) (( $# >= 2 )) || usage; serial="$2"; shift 2 ;;
    --screen-off-minutes) (( $# >= 2 )) || usage; minutes="$2"; shift 2 ;;
    --doze) doze=true; shift ;;
    --resource-samples) (( $# >= 2 )) || usage; samples="$2"; shift 2 ;;
    --resource-interval) (( $# >= 2 )) || usage; interval="$2"; shift 2 ;;
    *) usage ;;
  esac
done
[[ -n "$output" ]] || usage
[[ "$samples" =~ ^[1-9][0-9]{0,2}$ && "$interval" =~ ^[1-9][0-9]{0,2}$ ]] || usage
(( samples <= 120 && interval <= 60 )) || usage
[[ "$minutes" == 0 || "$minutes" == 30 || "$minutes" == 60 ]] || usage
[[ ! -e "$output" ]] || { echo "Refusing to overwrite physical evidence: $output" >&2; exit 1; }
command -v adb >/dev/null || { echo "adb is required" >&2; exit 1; }
args=()
[[ -z "$serial" ]] || args=(-s "$serial")
adb "${args[@]}" get-state | grep -qx device || { echo "ADB phone unavailable" >&2; exit 1; }
umask 077
mkdir -p "$output"
doze_active=false
cleanup() {
  if "$doze_active"; then
    adb "${args[@]}" shell dumpsys deviceidle unforce >/dev/null 2>&1 || :
  fi
}
trap cleanup EXIT
property() { adb "${args[@]}" shell getprop "$1" | tr -d '\r' | head -c 180; }
snapshot() {
  local name="$1"
  {
    printf 'utc=%s\n' "$(date -u +%FT%TZ)"
    printf 'rpc_process_pids='
    adb "${args[@]}" shell pidof dev.nodexlm.app:rpc 2>/dev/null | tr -d '\r' || :
    printf '\n'
    printf 'rpc_process_memory:\n'
    adb "${args[@]}" shell dumpsys meminfo dev.nodexlm.app:rpc 2>/dev/null | grep -E 'TOTAL PSS:|TOTAL RSS:|TOTAL SWAP PSS:|Native Heap|Dalvik Heap|Graphics|TOTAL[[:space:]]+[0-9]' || :
    printf 'ui_process_memory:\n'
    adb "${args[@]}" shell dumpsys meminfo dev.nodexlm.app 2>/dev/null | grep -E '\*\* MEMINFO in pid|TOTAL PSS:|TOTAL RSS:|TOTAL SWAP PSS:|Native Heap|Dalvik Heap|Graphics|TOTAL[[:space:]]+[0-9]' || :
    printf 'process_cpu:\n'
    adb "${args[@]}" shell dumpsys cpuinfo 2>/dev/null | grep -E '[0-9]+/dev\.nodexlm\.app(:rpc)?:' || :
    printf 'rpc_threads:\n'
    local pids pid
    pids="$(adb "${args[@]}" shell pidof dev.nodexlm.app:rpc 2>/dev/null | tr -d '\r' || :)"
    for pid in $pids; do
      [[ "$pid" =~ ^[0-9]+$ ]] || continue
      adb "${args[@]}" shell ps -T -p "$pid" 2>/dev/null | wc -l || :
    done
    printf 'meminfo:\n'
    adb "${args[@]}" shell cat /proc/meminfo | sed -n '1,6p'
    printf 'battery:\n'
    adb "${args[@]}" shell dumpsys battery | grep -E 'level:|status:|temperature:|AC powered:|USB powered:|Wireless powered:' || :
    printf 'thermal:\n'
    adb "${args[@]}" shell dumpsys thermalservice | grep -E 'Thermal Status:|mStatus =|ThermalStatus:' | head -n 5 || :
    printf 'idle:\n'
    adb "${args[@]}" shell dumpsys deviceidle get deep 2>/dev/null || :
  } > "$output/$name.txt"
}
{
  printf 'capture_utc=%s\n' "$(date -u +%FT%TZ)"
  for key in ro.product.manufacturer ro.product.model ro.product.cpu.abi ro.build.version.sdk ro.build.version.release ro.build.fingerprint; do
    printf '%s=' "$key"
    property "$key"
    printf '\n'
  done
  printf 'screen_off_minutes=%s\n' "$minutes"
  printf 'resource_samples=%s\nresource_interval_seconds=%s\n' "$samples" "$interval"
  printf 'doze_forced=%s\n' "$doze"
  printf 'physical_acceptance=operator_review_required\n'
  printf 'note=ADB snapshots do not measure tokens/s or prove background survivability across OEMs\n'
} > "$output/device.txt"
snapshot before
for ((i=1; i<samples; i++)); do
  sleep "$interval"
  snapshot "resource_$i"
done
if (( minutes > 0 )); then
  echo "Turning off the screen; keep the RPC Guest active. Do not disconnect ADB."
  adb "${args[@]}" shell input keyevent KEYCODE_SLEEP
  if "$doze"; then
    adb "${args[@]}" shell dumpsys deviceidle force-idle
    doze_active=true
  fi
  sleep "$((minutes * 60))"
  snapshot after_screen_off
fi
if "$doze" && (( minutes == 0 )); then
  adb "${args[@]}" shell dumpsys deviceidle force-idle
  doze_active=true
  sleep 30
  snapshot after_doze
fi
snapshot final
echo "ADB evidence saved to $output. Check per-snapshot PID and thermal/battery data."
echo "Run scripts/hardware-acceptance.py separately for controlled Host/Guest tokens/s; no automated physical acceptance is implied."
