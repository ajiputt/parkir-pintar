# System Overview Diagrams

> Mermaid diagrams yang juga ada inline di README.md, di-pisah sini untuk maintenance.

## C4 Context

```mermaid
flowchart LR
    subgraph User
      D[Driver / Mobile]
    end
    subgraph PG[Payment Gateway]
      MT[Midtrans QRIS]
    end
    subgraph PP[ParkirPintar]
      GW[Gateway]
      RES[Reservation]
      BIL[Billing]
      PAY[Payment]
    end
    D -- REST --> GW
    GW -- gRPC --> RES & BIL & PAY
    PAY <--> MT
    MT -- Webhook --> GW
```

## Sequence: Booking → Billing → Payment

(Lihat README.md §2.3)

## Sequence: No-show Auto-Expiry

(Lihat README.md §2.4)

## Data Flow

```mermaid
flowchart LR
    R[Reservation Service] -- writes --> RDB[(reservation.*)]
    R -- publish --> N1[NATS reservation.*]
    N1 -- subscribe --> B[Billing Service]
    B -- writes --> BDB[(billing.*)]
    B -- publish --> N2[NATS billing.invoice.*]
    N2 -- subscribe --> P[Payment Service]
    P <--> MT[Midtrans]
    MT -- webhook --> P
    P -- writes --> PDB[(payment.*)]
    P -- publish --> N3[NATS payment.*]
    N3 -- subscribe --> B
    N1 & N2 & N3 -- subscribe --> NOT[Notification Service]
```
