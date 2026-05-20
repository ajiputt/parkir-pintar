# OWASP API Top 10 (2023) — Compliance Checklist

> Memetakan implementasi ParkirPintar terhadap OWASP API Security Top 10 (2023). Memenuhi kompetensi #5 (Software Security Level 4).

| # | Risk | Status | Implementasi di ParkirPintar |
|---|---|---|---|
| API1 | Broken Object Level Authorization (BOLA) | ✅ | Setiap endpoint reservation cek `driver_id` request match dengan token JWT subject. Test: `e2e/authorization_test.go`. |
| API2 | Broken Authentication | ✅ | JWT (RS256 prod), short TTL (15m access + 7d refresh). Refresh rotation. JWT signing secret di AWS Secrets Manager. Rate limit per IP & per token. |
| API3 | Broken Object Property Level Authorization | ✅ | Driver tidak boleh modifikasi `state`, `expires_at`, `spot_id` direct. Hanya allowed transitions via dedicated endpoints (`:checkin`, `:checkout`, `:cancel`). |
| API4 | Unrestricted Resource Consumption | ✅ | Rate limit 200 RPS per IP di gateway (`pkg/middleware/RateLimit`). Connection pool DB max 20. Timeout per request 5-10s. Request body limit 1MB. |
| API5 | Broken Function Level Authorization | ✅ | Admin endpoints (`/v1/admin/*`) require `role:operator` claim. Audit log semua mutasi via `events_log`. |
| API6 | Unrestricted Access to Sensitive Business Flows | ⚠️ | Booking limit per driver per hari (5 reservation aktif max). Anti-bot: reCAPTCHA di register/login. **Pending**: ML-based anomaly detection. |
| API7 | Server Side Request Forgery (SSRF) | ✅ | Tidak ada user-controlled URL fetch. Webhook dari Midtrans di-validate via signature, IP allowlist (Midtrans IP range). |
| API8 | Security Misconfiguration | ✅ | Distroless container (`gcr.io/distroless/static-debian12:nonroot`), read-only rootfs, `runAsNonRoot`, drop ALL capabilities. NetworkPolicy default-deny. CORS strict di prod. TLS termination di ALB. |
| API9 | Improper Inventory Management | ✅ | Versioned API (`/v1/`, proto `.v1`). Deprecation policy: minimum 6 bulan sunset notice. OpenAPI auto-generated, registered di internal API catalog. |
| API10 | Unsafe Consumption of APIs | ✅ | Midtrans response divalidasi schema sebelum diproses. Timeout + circuit breaker untuk semua external call. Webhook signature wajib verified sebelum proses. |

## Tools & Automation

| Tool | Tujuan | Trigger |
|---|---|---|
| `gosec` | SAST untuk Go | CI tiap PR |
| `govulncheck` | Known CVE in dependencies | CI + weekly schedule |
| `trivy` | Container image scan | CI + nightly |
| `checkov` | IaC misconfig (Terraform, Helm, Dockerfile) | CI tiap PR |
| `gitleaks` | Secret leakage di git history | CI tiap PR + pre-commit hook |
| `OWASP ZAP` | DAST | Manual + monthly scan |

## PCI DSS Considerations

ParkirPintar **tidak menyimpan card data** — semua payment via Midtrans (PCI-compliant gateway). PCI scope kita reduced ke level **SAQ A** (merchant outsourcing all card data handling).

Compliance checklist:
- [x] Tidak ada PAN/CVV/track data di logs, DB, atau cache
- [x] HTTPS-only komunikasi dengan Midtrans
- [x] Audit trail webhook (lihat `webhook_log` table)
- [x] Quarterly Vulnerability Scan (CI Trivy + manual ZAP)
- [x] Annual Penetration Test (planned external vendor)

## Threat Model — STRIDE

Lihat [`threat-model.md`](threat-model.md) untuk per-component STRIDE analysis.

## Incident Response

Lihat [`../runbooks/incident-response.md`](../runbooks/incident-response.md).
