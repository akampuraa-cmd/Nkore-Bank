<div align="center">

# 🏦 Nkore Bank

### **Premium Core Banking System**

*Enterprise-grade, cloud-native financial platform built with security-first principles*

[![Go](https://img.shields.io/badge/Go-1.24-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://golang.org)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-336791?style=for-the-badge&logo=postgresql&logoColor=white)](https://postgresql.org)
[![Redis](https://img.shields.io/badge/Redis-7-DC382D?style=for-the-badge&logo=redis&logoColor=white)](https://redis.io)
[![Docker](https://img.shields.io/badge/Docker-Ready-2496ED?style=for-the-badge&logo=docker&logoColor=white)](https://docker.com)

---

**Secure · Reliable · Scalable** — *Designed for 99.999% availability with zero-trust security*

</div>

---

## ✨ Overview

**Nkore Bank** is a production-ready core banking system (CBS) built in Go, implementing double-entry accounting, event sourcing, and strict ACID compliance.

### 🎨 Brand — Premium Gold & Deep Navy

| Primary | Accent | Secondary |
|---------|--------|-----------|
| `#1a1a2e` Deep Navy | `#c9a84c` Premium Gold | `#16213e` Midnight Blue |

## 🏗️ Architecture

| Module | Description |
|--------|-------------|
| **Account Management** | DDA, Savings, Loan lifecycle with KYC |
| **Transaction Engine** | Double-entry (Dr/Cr) with ACID enforcement |
| **General Ledger** | `Assets = Liabilities + Equity` |
| **Compliance & AML** | CTR ($10K+), SAR, velocity detection |
| **Interest & Accrual** | Actual/365 & 30/360 batch calculations |
| **Outbox Publisher** | Reliable at-least-once event delivery |

## 🛡️ Banking Principles (Non-Negotiable)

1. **No Floating Point** — `DECIMAL(19,4)` / `shopspring/decimal` everywhere
2. **Immutable Ledger** — INSERT-ONLY entries; balances via `SUM()`
3. **Double-Entry** — Balanced debit/credit on every transaction
4. **Idempotency** — `Idempotency-Key` header required on all mutations
5. **Concurrency Control** — Optimistic + pessimistic locking
6. **Audit Trail** — Every state change logged with trace ID

## 🚀 Quick Start

```bash
# Docker Compose
make docker-up

# Or run locally
export DATABASE_URL="postgres://nkorebank:secret@localhost:5432/nkorebank?sslmode=disable"
export REDIS_URL="redis://localhost:6379/0"
export JWT_SECRET="your-secret" AES_ENCRYPTION_KEY="0123456789abcdef0123456789abcdef"
make build && make run
```

## 📡 API Endpoints (`/api/v1`, JWT required)

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/accounts` | Create account |
| GET | `/accounts/{id}/balance` | Get balance |
| POST | `/transactions/deposit` | Deposit |
| POST | `/transactions/withdraw` | Withdraw |
| POST | `/transactions/transfer` | Transfer |
| GET | `/transactions/{id}` | Get transaction |
| POST | `/gl/journal-entries` | Post GL entry |
| GET | `/gl/trial-balance` | Trial balance |
| GET | `/compliance/alerts` | AML alerts |
| POST | `/interest/accrue` | Daily accrual |

> Full spec: [`api/openapi/nkorebank.yaml`](api/openapi/nkorebank.yaml)

## 📁 Structure

```
cmd/nkorebank/main.go        — Entry point
internal/domain/account/      — Account management
internal/domain/transaction/  — Transaction engine
internal/domain/ledger/       — General ledger
internal/domain/compliance/   — AML/KYC
internal/domain/interest/     — Interest accrual
internal/middleware/           — Auth, idempotency, audit, rate-limit
internal/platform/            — Database, cache, telemetry
migrations/                   — PostgreSQL schemas
deployments/k8s/              — Kubernetes manifests
```

## 🛠️ Tech Stack

Go 1.24 · PostgreSQL 16 · Redis 7 · Kafka · JWT/OAuth2 · AES-256 · OpenTelemetry · Docker/K8s

---

<div align="center">

*Nkore Bank — Where Security Meets Innovation*

</div>
