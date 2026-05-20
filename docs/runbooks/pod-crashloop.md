# Runbook: Pod CrashLoopBackOff

| Severity | SEV2 |
|---|---|
| Alert source | Grafana → `Pod Restarting - Core Services` |
| Threshold | `kube_pod_container_status_restarts_total > 2 dalam 15 menit` |
| Estimated MTTR | 10–30 menit |

## Symptom

- Kubernetes shows pod state `CrashLoopBackOff`
- Service requests fail intermittently (depending on healthy replica count)
- Grafana alert fires:
  ```
  🚨 [FIRING:N] Pod Restarting - Core Services
  Pod <pod-name> restart N× dalam 15m terakhir
  ```

## Quick Triage

| Question | Yes | No |
|---|---|---|
| 1 pod crashing atau semua replicas? | 1 pod → node issue / random | Semua → bad config / deploy regression |
| Recent deploy? | Yes → rollback first | No → resource / dep issue |
| OOMKilled di pod events? | Yes → memory issue | No → check exit code |

## Diagnostic Steps

### 1. Get pod status + events

```bash
# Status overview
kubectl get pods -n parkir -l app=<service> -o wide

# Detailed events
kubectl describe pod <pod-name> -n parkir | head -80
```

Look for `Last State` section:
- `Reason: OOMKilled` → memory limit exceeded
- `Reason: Error, ExitCode: 1` → app crashed
- `Reason: Error, ExitCode: 137` → SIGKILL (often OOM or eviction)
- `Reason: Completed, ExitCode: 0` → graceful exit (unusual for long-running service)

### 2. Get crash logs

```bash
# Current container (might be in restart)
kubectl logs <pod-name> -n parkir --tail=200

# Previous container (the one that crashed)
kubectl logs <pod-name> -n parkir --previous --tail=200
```

Tail mode untuk follow:
```bash
kubectl logs -f -n parkir -l app=<service>
```

### 3. Check resource usage history

```promql
# Memory before crash
container_memory_working_set_bytes{namespace="parkir", pod="<pod>"}

# CPU spike?
rate(container_cpu_usage_seconds_total{namespace="parkir", pod="<pod>"}[1m])

# OOMKilled events
kube_pod_container_status_last_terminated_reason{reason="OOMKilled"}
```

### 4. Check probes

```bash
kubectl describe pod <pod-name> -n parkir | grep -E "Liveness|Readiness"
```

Look for:
- `Liveness probe failed: HTTP probe failed with statuscode: 503`
- `Readiness probe failed: dial tcp: connection refused`

## Resolution Paths

### Path A — OOMKilled

Sign: `OOMKilled` reason, `ExitCode: 137`.

```bash
# Quick: increase memory limit
kubectl set resources deployment/<service> -n parkir \
  --limits=memory=512Mi --requests=memory=256Mi
```

Long-term di Helm values:
```yaml
# deploy/helm/parkir-pintar/values.yaml
services:
  <service>:
    resources:
      limits:
        memory: "512Mi"
      requests:
        memory: "256Mi"
```

Investigate cause:
- Memory leak? Use pprof: `curl <pod>:8080/debug/pprof/heap > heap.pprof`
- Large payload not streamed? Add pagination
- Goroutine leak? Check `runtime.NumGoroutine()` metric

### Path B — App crash (ExitCode 1)

Look at logs untuk panic / fatal error:

```bash
kubectl logs <pod-name> -n parkir --previous | grep -A 30 "panic\|fatal\|FATAL"
```

Common patterns:
- `nil pointer dereference` → missing nil check
- `connection refused: postgres` → DB not ready (race condition)
- `JWT_SECRET not set` → missing env var
- `DB_URL not set` → fail-fast on startup (intentional per security ADR)

Fix:
- For missing env: update Helm values or K8s Secret
- For nil pointer: rollback + fix code
- For race condition: add init container atau retry loop

### Path C — Liveness probe failing

Sign: pod restart loop, `Liveness probe failed` di events.

Possible causes:
1. **Probe too aggressive**: timeout / failureThreshold too low
2. **Real health issue**: app hangs / deadlock
3. **Slow startup**: probe starts before app ready

Fix probe config:
```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: 8080
  initialDelaySeconds: 30   # ← give time to start
  periodSeconds: 10
  timeoutSeconds: 5
  failureThreshold: 3
```

For startup issue, use `startupProbe`:
```yaml
startupProbe:
  httpGet:
    path: /healthz
    port: 8080
  failureThreshold: 30
  periodSeconds: 10
```

### Path D — Readiness probe failing

Pod tidak masuk Service endpoint kalau readiness fail. Sign: pod Running tapi tidak receive traffic.

```bash
kubectl get endpoints -n parkir
# Pod yang readiness fail tidak akan muncul di endpoints
```

Check `/readyz` directly:
```bash
kubectl port-forward <pod> 9191:9191 -n parkir
curl http://localhost:9191/readyz
```

Expected: 200 dengan JSON status semua deps healthy. Kalau ada `"status": "down"` di salah satu dep, fix dep tersebut.

### Path E — Image pull error

Sign: `ErrImagePull` atau `ImagePullBackOff` di status.

```bash
kubectl describe pod <pod-name> -n parkir | grep -A5 "Failed"
```

Common:
- Image tag typo → cek `kubectl get deploy <service> -o yaml | grep image:`
- Registry auth → cek `imagePullSecrets`
- Registry down → cek GHCR status

### Path F — Bad config / Secret

Sign: pod start lalu langsung crash dengan config-related panic.

```bash
# Verify Secret + ConfigMap mounted
kubectl get secrets -n parkir
kubectl describe pod <pod-name> -n parkir | grep -A10 "Environment\|Mounts"

# Check actual env values (be careful with secrets!)
kubectl exec <pod-name> -n parkir -- env | grep -v PASSWORD
```

External Secrets sync issue:
```bash
kubectl get externalsecret -n parkir
kubectl describe externalsecret parkir-secrets -n parkir
```

## Verification

```bash
# Pod stable (no restarts in last 5 min)
kubectl get pod <pod-name> -n parkir
# RESTARTS should not increment

# Service receiving traffic
kubectl get endpoints -n parkir | grep <service>
# Should show pod IP

# Grafana alert auto-resolve
```

## Prevention

1. **Graceful shutdown handling** di app:
   ```go
   sigCh := make(chan os.Signal, 1)
   signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
   <-sigCh
   // Drain + cleanup
   ```

2. **Health checks meaningful** — `/readyz` actually checks deps, not just `200 OK`

3. **Resource limits realistic** — based on load test data, not guess

4. **Startup probe** untuk slow-starting services (DB migration, etc.)

5. **Init containers** untuk dependency wait:
   ```yaml
   initContainers:
   - name: wait-for-postgres
     image: busybox
     command: ['sh', '-c', 'until nc -z postgres 5432; do sleep 1; done']
   ```

## Related

- [ADR-0017](../architecture/adr/0017-deployment-eks.md) — deployment + probes config
- [pkg/health/](../../../pkg/health/) — readiness check implementation
- [secret-rotation.md](./secret-rotation.md) — kalau Secret-related crash
