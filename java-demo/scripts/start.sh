#!/bin/sh
set -e
echo "[start] Java demo: launching with OpenTelemetry javaagent (zero-code instrumentation)"

# Background CPU profiler — uses async-profiler, attaches via JVM TI (no app code).
/profile-pusher.sh &

# Foreground: Spring Boot app with the OTel agent injected at JVM startup.
# Everything (traces, metrics, logs) is auto-emitted via -javaagent.
# Stack the Pyroscope agent FIRST (it attaches async-profiler under the
# hood; the OTel agent doesn't conflict because they only race on the
# *event-bus* sampler, not the JVMTI hooks). Pyroscope agent is a no-op
# if PYROSCOPE_SERVER_ADDRESS isn't set, so this is safe in environments
# without Pyroscope wired in.
exec java \
  -javaagent:/agent/pyroscope.jar \
  -javaagent:/agent/opentelemetry-javaagent.jar \
  -XX:+UnlockDiagnosticVMOptions \
  -XX:+DebugNonSafepoints \
  -jar /app/demo.jar
