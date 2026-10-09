#!/bin/bash
# ha-failover-harness.sh — HA controller failover evidence harness (docs/HA_RUN_CONTINUITY_PLAN.md).
#
# Runs, as local processes: a throwaway Postgres container (127.0.0.1:$PG_PORT),
# a Python HTTP target (18999), two generators (19091/19092) and two HA
# controllers (18081/18082) sharing that Postgres. It starts a 10-VUser
# distributed run, kill -9s the execution leader at KILL_AT seconds, then
# records the survivor's view of the run and the per-second request rate the
# target actually received (the ground truth for "did load continue?").
#
# Usage:   scripts/ha-failover-harness.sh            (from the repo root)
# Env:     KILL_AT (45)  DURATION (150s)  MAX_WAIT (240)  HARNESS_DIR (mktemp)
#          PG_PORT (55432; change it when another local Postgres already uses that port)
#          FAILOVER_POLICY (continue | fail; default continue)
#          RESTART_KILLED_AFTER (seconds after the kill to relaunch the killed
#            controller as a follower; 0 = never)
#          SECOND_KILL_AT (seconds after start to kill -9 the survivor too; needs
#            RESTART_KILLED_AFTER so a third controller term exists; 0 = never)
#          STOP_AT (seconds after start to POST /stop to the active controller; 0 = never;
#            equal to KILL_AT kills the leader right after the stop, with it in flight)
#          STAGES_JSON (per-group stage array, e.g. '[{"type":"hold","duration":"40s","target_vusers":2}]';
#            replaces the default 5-VUser ramp, exercising a controller-driven shape)
#          PEAK_VUSERS (group vusers when STAGES_JSON is set; default 5)
#          SUSPEND_LEADER=1 (SIGSTOP the leader at KILL_AT instead of kill -9: a
#            paused, not dead, leader) with RESUME_AFTER (seconds later, SIGCONT it)
#          KILL_SIGNAL (KILL | TERM; default KILL). TERM stops the leader gracefully,
#            as a rolling update, node drain or eviction does: it hands
#            continue-policy runs to the successor and releases its lease.
#          TOLERATE_LOSS=1 (also set assignment.tolerate_generator_loss)
#          KILL_GENERATOR_AT (seconds after start to kill -9 generator g2; 0 = never)
#          ARCHIVE_WAIT (seconds to keep polling for the run archive after the
#            terminal status; 0 = don't. An interrupted run's archive is collected
#            afterwards by the once-a-minute archive collection retry worker)
#          GENERATOR_BIN (generator binary to run instead of this checkout's, e.g. an
#            older release to exercise a mixed fleet)
# Output:  $HARNESS_DIR/run-*/{timeline,status,c1,c2,g1,g2}.log, rps.txt, final_test.json
#
# Every child process is tracked and killed by the EXIT trap and the
# container is removed; the script reports leftover processes on exit.
# Requires: go, docker, python3, curl. Does not touch any cluster or compose stack,
# and keeps every archive under $HARNESS_DIR (never the user cache directory).
set -uo pipefail
REPO="$(cd "$(dirname "$0")/.." && pwd)"
P0="${HARNESS_DIR:-$(mktemp -d -t apexload-ha-harness)}"
mkdir -p "$P0/bin"
(cd "$REPO" && go build -o "$P0/bin/controller" ./cmd/controller && go build -o "$P0/bin/generator" ./cmd/generator) || { echo "build failed" >&2; exit 1; }
RUN="$P0/run-$(date +%H%M%S)"
mkdir -p "$RUN"
PG="apexload-ha-$$-$(date +%s)"
PG_CREATED=0
PIDS=()
PG_PORT=${PG_PORT:-55432}
KILL_AT=${KILL_AT:-45}         # seconds after start
DURATION=${DURATION:-150s}
MAX_WAIT=${MAX_WAIT:-240}
FAILOVER_POLICY=${FAILOVER_POLICY:-continue}
RESTART_KILLED_AFTER=${RESTART_KILLED_AFTER:-0}
SECOND_KILL_AT=${SECOND_KILL_AT:-0}
STOP_AT=${STOP_AT:-0}
SUSPEND_LEADER=${SUSPEND_LEADER:-0}
KILL_SIGNAL=${KILL_SIGNAL:-KILL}
ARCHIVE_WAIT=${ARCHIVE_WAIT:-0}
TOLERATE_LOSS=${TOLERATE_LOSS:-0}
KILL_GENERATOR_AT=${KILL_GENERATOR_AT:-0}
RESUME_AFTER=${RESUME_AFTER:-0}
STAGES_JSON=${STAGES_JSON:-}
PEAK_VUSERS=${PEAK_VUSERS:-5}
# One wrapping key shared by both controllers: a successor must be able to
# unseal the adoption checkpoint the failed leader wrote.
WRAP_KEY=$(python3 -c 'import os,base64;print(base64.b64encode(os.urandom(32)).decode())')

cleanup() {
  for p in "${PIDS[@]}"; do kill -CONT "$p" 2>/dev/null; kill -9 "$p" 2>/dev/null; done
  if [ "$PG_CREATED" -eq 1 ]; then
    docker rm -f "$PG" >/dev/null 2>&1
  fi
  sleep 1
  left=$(pgrep -f "$P0/bin/|$RUN/target" | wc -l | tr -d ' ')
  echo "cleanup: leftover processes=$left" | tee -a "$RUN/timeline.log"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

ts() { python3 -c 'import time;print(f"{time.time():.1f}")'; }
log() { echo "$(ts) $*" | tee -a "$RUN/timeline.log"; }

# --- postgres
docker run -d --rm --name "$PG" -e POSTGRES_PASSWORD=p0 -e POSTGRES_DB=apexload -p 127.0.0.1:$PG_PORT:5432 postgres:16-alpine >/dev/null || { log "PostgreSQL container startup failed"; exit 1; }
PG_CREATED=1
for i in $(seq 1 30); do docker exec "$PG" pg_isready -U postgres >/dev/null 2>&1 && break; sleep 1; done
sleep 2
DBURL="postgres://postgres:p0@127.0.0.1:$PG_PORT/apexload?sslmode=disable"

# --- target: counts requests per second via access log
mkdir -p "$RUN/target"; echo ok > "$RUN/target/get"
(cd "$RUN/target" && exec python3 -u -m http.server 18999 --bind 127.0.0.1) 2> "$RUN/target.log" >/dev/null &
PIDS+=($!)

# --- generators
GPID=()
for n in 1 2; do
  mkdir -p "$RUN/g$n/state"
  (cd "$RUN/g$n" && APEXLOAD_ENV=development APEXLOAD_GENERATOR_ID=p0-g$n \
    APEXLOAD_GENERATOR_LISTEN_ADDR=127.0.0.1:1909$n APEXLOAD_GENERATOR_HEALTH_ADDR=127.0.0.1:1919$n \
    APEXLOAD_GENERATOR_STATE_DIR="$RUN/g$n/state" APEXLOAD_ARCHIVE_DIR="$RUN/g$n/archives" exec "${GENERATOR_BIN:-$P0/bin/generator}") > "$RUN/g$n.log" 2>&1 &
  GPID[$n]=$!; PIDS+=($!)
done
sleep 2

# --- controllers (HA, shared postgres)
CPID=()
start_controller() {
  local n=$1
  mkdir -p "$RUN/c$n"
  (cd "$RUN/c$n" && APEXLOAD_ENV=development APEXLOAD_CONTROLLER_HA=true APEXLOAD_STORE=postgres APEXLOAD_WRAPPING_MASTER_KEY="$WRAP_KEY" APEXLOAD_ADDR=127.0.0.1:1808$n APEXLOAD_ARCHIVE_DIR="$RUN/c$n/archives" \
    APEXLOAD_DATABASE_URL="$DBURL" APEXLOAD_GENERATORS=127.0.0.1:19091,127.0.0.1:19092 \
    exec "$P0/bin/controller") >> "$RUN/c$n.log" 2>&1 &
  CPID[$n]=$!; PIDS+=($!)
}
for n in 1 2; do
  start_controller $n
  sleep 4   # c1 boots first and should win the lease
done
for n in 1 2; do
  for i in $(seq 1 40); do curl -sf "http://127.0.0.1:1808$n/api/v1/health" >/dev/null 2>&1 && break; sleep 1; done
done
sleep 5
LEADER=""
for n in 1 2; do grep -q "execution leadership acquired" "$RUN/c$n.log" && LEADER=$n; done
[ -z "$LEADER" ] && { log "no leader acquired"; exit 1; }
FOLLOWER=$((3 - LEADER)); L="http://127.0.0.1:1808$LEADER"; F="http://127.0.0.1:1808$FOLLOWER"
log "leader=c$LEADER follower=c$FOLLOWER"

# --- script + test
python3 - "$REPO/examples/k8s-distributed-smoke.json" > "$RUN/script.json" <<'EOF'
import json,sys
d=json.load(open(sys.argv[1])); s=d["script"]
s["name"]="P0_Failover"; s["config"]["base_url"]="http://127.0.0.1:18999"
json.dump(d,sys.stdout)
EOF
SR=$(curl -s -X POST "$L/api/v1/scripts" -H 'Content-Type: application/json' --data-binary @"$RUN/script.json")
SID=$(echo "$SR" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("id",""))' 2>/dev/null)
[ -z "$SID" ] && { log "script create failed: $SR"; exit 1; }
if [ -n "$STAGES_JSON" ]; then
  SHAPE="\"vusers\":$PEAK_VUSERS,\"stages\":$STAGES_JSON"
else
  SHAPE='"vusers":5,"ramp_up":"5s"'
fi
LOSS=""
[ "$TOLERATE_LOSS" = "1" ] && LOSS=',"tolerate_generator_loss":true'
TP=$(cat <<JSON
{"name":"p0-failover","duration":"$DURATION","assignment":{"failover_policy":"$FAILOVER_POLICY"$LOSS},"groups":[
 {"name":"ga","script_id":"$SID",$SHAPE},
 {"name":"gb","script_id":"$SID",$SHAPE}]}
JSON
)
TR=$(curl -s -X POST "$L/api/v1/tests" -H 'Content-Type: application/json' --data "$TP")
TID=$(echo "$TR" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("id",""))' 2>/dev/null)
[ -z "$TID" ] && { log "test create failed: $TR"; exit 1; }
curl -s -X POST "$L/api/v1/tests/$TID/start" -H 'Content-Type: application/json' --data '{}' > "$RUN/start.json"
T0=$(ts); log "started test=$TID"
sleep 5
ARMED=$(docker exec "$PG" psql -U postgres -d apexload -qAt -c "SELECT COALESCE(length(continuity_checkpoint_json),0) FROM tests WHERE id='$TID'")
log "policy=$FAILOVER_POLICY continuity checkpoint bytes=${ARMED:-0} (arm log: $(grep -ho 'run continuity [a-z ]*armed[^"]*' "$RUN"/c*.log | head -1))"

status() { curl -s -m 2 "$1/api/v1/tests/$TID" | python3 -c 'import json,sys
try:
  d=json.load(sys.stdin); print(d.get("status"), "vusers=%s"%d.get("active_vusers", d.get("vusers")), "err=%s"%(d.get("error") or ""))
except Exception as e: print("unreachable")' 2>/dev/null; }

killed=0; kill_el=0; restarted=0; second=0; stopped=0
ACTIVE="$L"   # the controller expected to lead right now
while :; do
  now=$(ts); el=$(python3 -c "print(int($now-$T0))")
  # Stop is evaluated before the kill so STOP_AT == KILL_AT leaves the stop
  # in flight when the leader dies.
  if [ $stopped -eq 0 ] && [ "$STOP_AT" -gt 0 ] && [ "$el" -ge "$STOP_AT" ]; then
    code=$(curl -s -o "$RUN/stop.json" -w '%{http_code}' -X POST "$ACTIVE/api/v1/tests/$TID/stop" -H 'Content-Type: application/json' --data '{}')
    stopped=1; log "STOP requested via $ACTIVE at +${el}s -> HTTP $code"
  fi
  if [ $killed -eq 0 ] && [ "$el" -ge "$KILL_AT" ]; then
    if [ "$SUSPEND_LEADER" = "1" ]; then
      kill -STOP "${CPID[$LEADER]}"; log "SUSPENDED leader c$LEADER (SIGSTOP) at +${el}s"
    elif [ "$KILL_SIGNAL" = "TERM" ]; then
      kill -TERM "${CPID[$LEADER]}"; term_watch=1; log "TERMINATED leader c$LEADER (SIGTERM, graceful shutdown) at +${el}s"
    else
      kill -9 "${CPID[$LEADER]}"; log "KILLED leader c$LEADER (kill -9) at +${el}s"
    fi
    killed=1; kill_el=$el; ACTIVE="$F"
  fi
  if [ "${term_watch:-0}" -eq 1 ] && ! kill -0 "${CPID[$LEADER]}" 2>/dev/null; then
    term_watch=0; log "old leader c$LEADER exited at +${el}s; $(grep -ho 'handing continue-policy runs[^}]*' "$RUN/c$LEADER.log" | tail -1)"
  fi
  if [ "$SUSPEND_LEADER" = "1" ] && [ $killed -eq 1 ] && [ "$RESUME_AFTER" -gt 0 ] && [ "$el" -ge $((kill_el + RESUME_AFTER)) ] && [ "${resumed:-0}" -eq 0 ]; then
    kill -CONT "${CPID[$LEADER]}"; resumed=1; log "RESUMED old leader c$LEADER (SIGCONT) at +${el}s"
  fi
  if [ $killed -eq 1 ] && [ $restarted -eq 0 ] && [ "$RESTART_KILLED_AFTER" -gt 0 ] && [ "$el" -ge $((kill_el + RESTART_KILLED_AFTER)) ]; then
    start_controller "$LEADER"; restarted=1; log "RESTARTED c$LEADER as a follower at +${el}s"
  fi
  if [ "${gkilled:-0}" -eq 0 ] && [ "$KILL_GENERATOR_AT" -gt 0 ] && [ "$el" -ge "$KILL_GENERATOR_AT" ]; then
    kill -9 "${GPID[2]}"; gkilled=1; log "KILLED generator g2 (kill -9) at +${el}s"
  fi
  if [ $second -eq 0 ] && [ "$SECOND_KILL_AT" -gt 0 ] && [ "$el" -ge "$SECOND_KILL_AT" ]; then
    kill -9 "${CPID[$FOLLOWER]}"; second=1; ACTIVE="$L"; log "KILLED survivor c$FOLLOWER (kill -9) at +${el}s (second failover)"
  fi
  s=$(status "$ACTIVE")
  echo "$now +${el}s $s" >> "$RUN/status.log"
  case "$s" in COMPLETE*|FAILED*|ABORTED*|STOPPED*) [ $killed -eq 1 ] || [ $stopped -eq 1 ] && { log "terminal: $s"; break; } ;; esac
  [ "$el" -ge "$MAX_WAIT" ] && { log "max wait reached: $s"; break; }
  sleep 1
done
terminal_at=$(date +%s)
sleep 8
if [ "$ARCHIVE_WAIT" -gt 0 ]; then
  while :; do
    archive_code=$(curl -s -m 10 -o /dev/null -w '%{http_code}' "$ACTIVE/api/v1/tests/$TID/archive/report?profile=engineer&format=csv")
    aw=$(( $(date +%s) - terminal_at ))
    [ "$archive_code" = "200" ] && { log "archive: available ${aw}s after the terminal status"; break; }
    [ "$aw" -ge "$ARCHIVE_WAIT" ] && { log "archive: still unavailable (HTTP $archive_code) ${aw}s after the terminal status"; break; }
    sleep 5
  done
fi
curl -s "$ACTIVE/api/v1/tests/$TID" > "$RUN/final_test.json"
curl -s "$ACTIVE/api/v1/tests/$TID/archive/report?profile=engineer&format=csv" > "$RUN/report_engineer.csv"
curl -s "$ACTIVE/api/v1/tests/$TID/report?profile=engineer" > "$RUN/report_engineer.html"
log "continuity: $(python3 -c 'import json,sys;print(json.dumps(json.load(open(sys.argv[1])).get("continuity")))' "$RUN/final_test.json" 2>/dev/null)"

# --- request rate at the target, per second relative to T0
python3 - "$RUN/target.log" "$T0" > "$RUN/rps.txt" <<'EOF'
import sys,re,time,collections,datetime
t0=float(sys.argv[2]); c=collections.Counter()
for line in open(sys.argv[1]):
  m=re.search(r'\[(\d+/\w+/\d+ \d+:\d+:\d+)\] "GET /get',line)
  if m:
    t=time.mktime(datetime.datetime.strptime(m.group(1),"%d/%b/%Y %H:%M:%S").timetuple())
    c[int(t-t0)]+=1
for k in sorted(c): print(f"+{k}s {c[k]}")
EOF
log "done; run dir $RUN"
echo "$RUN" > "$P0/last_run"
