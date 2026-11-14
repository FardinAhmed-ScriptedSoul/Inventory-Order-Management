# StockFlow --- System Architecture & Design Document

**Project:** Inventory & Order Management System\
**Repository:**
https://github.com/FardinAhmed-ScriptedSoul/Inventory-Order-Management\
**Status:** Initial design / MVP\
**Primary goal:** A practical learning project demonstrating Go backend
engineering, PostgreSQL transactions, Redis-based concurrency
coordination, the transactional outbox pattern, webhook processing,
Docker, Nginx, Git, and GitHub Actions.

------------------------------------------------------------------------

## 1. Executive Summary

StockFlow is a small full-stack inventory and order management
application. Customers browse products and place orders. Administrators
manage products, inventory, orders, and notifications.

The system is intentionally deployed as independently containerized
frontend, backend, database, cache/coordination, and reverse-proxy
components. The backend remains a single Go application (a simple
application/service, not a collection of microservices). A Go background
worker runs inside the backend process as a goroutine.

The main engineering challenges are:

1.  Preventing overselling when multiple customers attempt to reserve
    scarce stock concurrently.
2.  Keeping order, inventory reservation, and event recording
    consistent.
3.  Handling payment webhooks safely, including duplicate delivery.
4.  Reliably generating notifications and invoice records from events.
5.  Building and running the entire application reproducibly with Docker
    Compose and CI.

### 1.1 Design principles

-   PostgreSQL is the source of truth for business data and stock.
-   Redis coordinates concurrent checkout attempts but does not replace
    database correctness.
-   A database transaction commits business changes and the
    corresponding outbox event atomically.
-   Webhooks and event processing are idempotent.
-   External or simulated payment processing is never performed while
    holding a stock lock or database transaction open.
-   Keep the MVP small; avoid introducing Kafka, RabbitMQ, Kubernetes,
    or multiple backend services.
-   Prefer explicit states, constraints, observable failures, and
    automated tests.

------------------------------------------------------------------------

## 2. Scope

### 2.1 MVP features

**Customer** - Sign in using a seeded customer account. - Browse
products and view price and availability. - Create an order for one or
more products. - Reserve inventory during checkout. - See order and
payment status. - Trigger a simulated payment. - View a printable
invoice after successful payment. - View personal notifications and
order history.

**Administrator** - Sign in using a seeded admin account. - Create and
update products. - Set and monitor inventory quantities. - View
products, orders, and payment status. - View notifications for new
orders and successful payments. - View low-stock products.

**System** - Redis-based per-product checkout lock. - PostgreSQL
conditional inventory updates and transactions. - Time-limited inventory
reservations. - Simulated payment provider and signed webhook. -
Transactional outbox and retrying background worker. - In-app
notifications for customer and admin. - Invoice record and printable
invoice view. - Docker Compose, Nginx reverse proxy, GitHub Actions CI.

### 2.2 Explicitly out of scope

-   Real payment gateway or movement of real money.
-   Real email, SMS, push notifications.
-   Kafka, RabbitMQ, or another message broker.
-   Microservices or service mesh.
-   Kubernetes.
-   Multiple warehouses, shipping, tax calculation, discounts, refunds,
    reviews, and advanced analytics.
-   Production-grade identity provider or complex permission system.
-   High-availability Redis/PostgreSQL clusters.

These can be future extensions, not MVP requirements.

------------------------------------------------------------------------

## 3. High-Level Architecture

### 3.1 Components

  -----------------------------------------------------------------------
  Component               Technology              Responsibility
  ----------------------- ----------------------- -----------------------
  Web frontend            React + Vite            Customer and admin user
                                                  interface

  Reverse proxy           Nginx                   Public entry point;
                                                  routes frontend and API
                                                  traffic

  Application backend     Go                      REST API,
                                                  authentication,
                                                  business rules, webhook
                                                  endpoint

  Relational database     PostgreSQL              Users, products,
                                                  inventory, orders,
                                                  payments, outbox,
                                                  notifications, invoices

  Coordination/cache      Redis                   Short-lived checkout
                                                  locks; optional
                                                  temporary data

  CI                      GitHub Actions          Tests and build
                                                  validation

  Local orchestration     Docker Compose          Builds, networks,
                                                  volumes, health checks,
                                                  container lifecycle
  -----------------------------------------------------------------------

### 3.2 Container topology

The MVP uses five containers:

1.  `nginx`
2.  `frontend`
3.  `backend`
4.  `postgres`
5.  `redis`

The Go backend process also runs the outbox worker and
reservation-expiration worker as goroutines. These are not separate
containers in the MVP.

``` text
                         Customer / Admin Browser
                                   |
                                   v
                             Nginx :80
                           /             \
                  frontend route       /api, /webhooks
                        |                    |
                        v                    v
                 React / Vite             Go API
                                           |
                                +----------+----------+
                                |                     |
                                v                     v
                           PostgreSQL              Redis
                                ^
                                |
                     Go background workers
                    - outbox event processor
                    - expired reservation cleanup
```

### 3.3 Network and exposure

-   Only Nginx publishes a host port (initially port 8080 or 80).
-   Frontend, backend, PostgreSQL, and Redis communicate over a private
    Docker Compose network.
-   PostgreSQL and Redis ports are not exposed publicly.
-   The browser calls relative paths such as `/api/v1/products`; Nginx
    forwards API requests to the Go backend.
-   The frontend container serves the built static React assets. It does
    not need direct access to PostgreSQL or Redis.

### 3.4 Request routing

Example Nginx routing:

-   `/` → frontend service
-   `/api/` → backend service
-   `/webhooks/` → backend service

The browser should use the same origin for frontend and API in the MVP,
simplifying CORS. If cross-origin access is introduced later, configure
CORS deliberately.

------------------------------------------------------------------------

## 4. Core Domain and Data Model

The initial schema contains these tables:

  -----------------------------------------------------------------------
  Table                               Purpose
  ----------------------------------- -----------------------------------
  `users`                             Customer and administrator accounts

  `products`                          Product name, description, price,
                                      active state

  `inventory`                         Physical on-hand quantity and
                                      currently reserved quantity

  `orders`                            Customer order and lifecycle status

  `order_items`                       Products, quantities, and price
                                      snapshots for an order

  `payments`                          Payment attempts and final payment
                                      state

  `payment_webhook_events`            Provider event IDs used to
                                      deduplicate webhook deliveries

  `outbox_events`                     Events waiting to be processed

  `notifications`                     In-app notifications for customers
                                      and administrators

  `invoices`                          Invoice number and order/payment
                                      summary
  -----------------------------------------------------------------------

### 4.1 Important relationships

-   A user can have many orders.
-   An order belongs to one customer.
-   An order has one or more order items.
-   Each order item references a product and stores the unit-price
    snapshot at order time.
-   A product has one inventory record.
-   An order may have payment records/attempts.
-   An order has at most one successful invoice in this MVP.
-   An outbox event can produce multiple notifications.
-   Notifications belong to a recipient user; admin notifications are
    addressed to the seeded admin account.

### 4.2 Inventory invariant

PostgreSQL is the authority for stock.

For each product:

``` text
available_quantity = on_hand_quantity - reserved_quantity

on_hand_quantity >= 0
reserved_quantity >= 0
reserved_quantity <= on_hand_quantity
```

When an order is reserved, `reserved_quantity` increases. When payment
succeeds, both `on_hand_quantity` and `reserved_quantity` decrease by
the purchased quantity. When a reservation expires or is cancelled,
`reserved_quantity` decreases but `on_hand_quantity` remains unchanged.

The application and database constraints must prevent negative
quantities.

### 4.3 Price integrity

The product's current price can change after an order is placed.
Therefore, `order_items` stores the unit price and quantity used for
that order. Payment validation uses the order's stored total, not a
newly fetched product price.

Use an exact decimal representation in PostgreSQL (for example
`NUMERIC`) and avoid floating-point arithmetic for money.

------------------------------------------------------------------------

## 5. Redis Locking and Concurrent Stock Reservation

### 5.1 Problem

Suppose a product has one available unit. Two customers attempt checkout
at nearly the same time. Without concurrency control, both requests
might read stock as one and both attempt to buy it.

### 5.2 Lock strategy

Use a short-lived Redis lock per product, acquired atomically with the
equivalent of:

``` redis
SET stockflow:product:<product_id>:lock <unique-token> NX PX 10000
```

-   `NX` means acquire only if the key does not already exist.
-   `PX` sets a finite lease in milliseconds.
-   The unique token identifies the lock owner.
-   A failed acquisition means another checkout currently owns the lock;
    return a conflict/retry response.

The lock is held only around the short stock-reservation critical
section. It is never held while waiting for payment.

### 5.3 Safe release

Do not blindly `DEL` the lock key. The lease may have expired and
another request may have acquired the lock. Release only if the stored
token still matches the caller's token, using an atomic Redis Lua
compare-and-delete operation.

The lease must be long enough for the short database operation. If the
application later performs longer critical sections, it must consider
lease renewal and ownership/fencing concerns. The MVP should keep the
critical section short rather than adding unnecessary complexity.

### 5.4 Multiple products and deadlock avoidance

An order may contain multiple products. Acquire product locks in a
deterministic order (for example, ascending product ID), and release
acquired locks in reverse order. If any lock cannot be acquired, release
the locks already acquired and fail/retry the checkout.

### 5.5 PostgreSQL remains the final guard

Redis is coordination, not the source of truth. Inside a PostgreSQL
transaction, reserve each product using a conditional update equivalent
to:

``` sql
UPDATE inventory
SET reserved_quantity = reserved_quantity + $1
WHERE product_id = $2
  AND on_hand_quantity - reserved_quantity >= $1;
```

Check that exactly one row was updated for every requested product. If
any update affects zero rows, roll back the entire transaction.

This protects inventory even if Redis is unavailable, a lock expires, or
another code path bypasses the lock.

### 5.6 Checkout transaction

Within one database transaction:

1.  Validate the customer and requested products.
2.  Read current product prices and calculate the order total.
3.  Conditionally reserve inventory for every order item.
4.  Insert the order with `PENDING_PAYMENT`.
5.  Insert order items with price snapshots.
6.  Insert an `ORDER_PLACED` outbox event.
7.  Commit.

If any step fails, roll back all database changes. Release Redis locks
after the database operation finishes, including on errors.

### 5.7 Important lock/payment distinction

Do not keep a Redis lock for the entire payment window. A customer may
take minutes to pay. The database reservation is what protects the stock
during that period; the Redis lock only serializes the short reservation
operation.

------------------------------------------------------------------------

## 6. Order and Reservation Lifecycle

### 6.1 Order states

Initial order states:

-   `PENDING_PAYMENT`
-   `PAID`
-   `CANCELLED`
-   `EXPIRED`

Payment states:

-   `PENDING`
-   `SUCCEEDED`
-   `FAILED`

These are separate concepts: an order can be pending payment while its
payment record is pending.

### 6.2 Reservation lifecycle

When checkout succeeds:

-   Increase `reserved_quantity`.
-   Set the order to `PENDING_PAYMENT`.
-   Store `reservation_expires_at`.
-   Create a pending payment record.
-   Insert the `ORDER_PLACED` outbox event.

When payment succeeds before expiry:

-   Validate the payment amount and currency.
-   Mark the payment as successful.
-   Mark the order as `PAID`.
-   Decrease both `on_hand_quantity` and `reserved_quantity`.
-   Insert a `PAYMENT_SUCCEEDED` outbox event.
-   Commit all changes together.

When payment fails or the reservation expires:

-   Do not reduce physical on-hand stock.
-   Decrease `reserved_quantity`.
-   Mark the order `CANCELLED` or `EXPIRED`, as appropriate.
-   Mark the payment attempt failed/expired as appropriate.
-   Commit the state changes atomically.

### 6.3 Expiration worker

A Go background goroutine periodically finds expired pending orders. It
must claim/process them safely and use a database transaction to release
reservations and update statuses.

Payment success and expiration can race. Both paths must lock/update the
order in a consistent manner and check its current state inside the
transaction. Only one transition may win:

-   If payment commits first, expiration must not release stock.
-   If expiration commits first, a late payment must not mark the order
    paid or consume stock.

For this simulated project, a late successful payment event should be
rejected/recorded for investigation rather than silently
charging/fulfilling an expired order. A real payment system would need a
refund/void policy.

------------------------------------------------------------------------

## 7. Simulated Payment and Webhook Processing

### 7.1 Payment simulation

No real money is transferred. The customer clicks a Pay button, and the
application simulates a provider response.

The simulator should behave like an external provider: it sends a
separate HTTP request to the backend webhook endpoint rather than
directly changing the order from the browser.

### 7.2 Webhook endpoint

Example:

``` http
POST /webhooks/payment
Content-Type: application/json
X-Payment-Signature: <signature>
```

Example payload:

``` json
{
  "event_id": "evt_123",
  "event_type": "payment.succeeded",
  "order_id": "order_123",
  "amount": "50.00",
  "currency": "USD"
}
```

### 7.3 Webhook verification

-   Read the raw request body.
-   Compute HMAC-SHA256 using a server-side webhook secret.
-   Compare signatures using a constant-time comparison.
-   Reject invalid signatures.
-   Validate event type, order ID, amount, and currency.
-   Never trust a payment-success claim supplied directly by the
    browser.
-   Keep the webhook secret outside source control.

The dummy provider and backend can share a development-only secret. This
is a learning simulation, not a production payment integration.

### 7.4 Idempotency

Providers may deliver the same webhook more than once.

Use `payment_webhook_events` with a unique constraint on provider
`event_id`. In a transaction:

1.  Attempt to record the event ID.
2.  If it already exists, return a successful acknowledgement without
    applying the payment again.
3.  Lock/read the order and payment.
4.  Verify the order is still eligible for payment.
5.  Verify amount and currency.
6.  Update payment, order, and inventory.
7.  Insert `PAYMENT_SUCCEEDED` into the outbox.
8.  Commit.

The unique event ID and order/payment state checks prevent duplicate
payment effects.

------------------------------------------------------------------------

## 8. Transactional Outbox Pattern

### 8.1 Why it is needed

An order transaction must save business data and record the event that
downstream work must process. Directly writing to PostgreSQL and
separately sending a message can fail halfway through.

StockFlow records the event in the same PostgreSQL transaction as the
business change. A worker later processes it.

### 8.2 Events

MVP event types:

-   `ORDER_PLACED`
-   `PAYMENT_SUCCEEDED`

An order event generates an admin notification. A payment-success event
generates an admin notification, a customer notification, and an invoice
record.

### 8.3 Suggested outbox columns

  Column                          Purpose
  ------------------------------- ----------------------------------------------
  `id`                            Unique event ID
  `aggregate_type`                For example, `ORDER`
  `aggregate_id`                  Related order ID
  `event_type`                    Event name
  `payload`                       JSONB event payload
  `status`                        `PENDING`, `PROCESSING`, `COMPLETED`, `DEAD`
  `attempts`                      Number of processing attempts
  `next_attempt_at`               Earliest retry time
  `locked_at` / `locked_until`    Worker lease metadata
  `last_error`                    Most recent processing error
  `created_at` / `processed_at`   Timestamps

### 8.4 Worker flow

1.  A Go goroutine polls the outbox at a modest interval.
2.  It claims a small batch of due events in a short transaction, using
    row locking such as `FOR UPDATE SKIP LOCKED`.
3.  It marks the events as `PROCESSING` with a lease and commits the
    claim.
4.  It processes each event.
5.  In a database transaction, it inserts notifications and invoice
    records as required and marks the event `COMPLETED`.
6.  If processing fails, it records the error, increments attempts, and
    schedules a retry with backoff.
7.  After a configured maximum number of attempts, it marks the event
    `DEAD` for inspection.

A lease allows another worker/run to recover events left in `PROCESSING`
after a crash.

### 8.5 Idempotent side effects

The worker can crash after performing a side effect but before
acknowledging completion. Therefore, it must be safe to process the same
event again.

Use database uniqueness constraints, for example:

-   Unique notification key based on event, recipient, and notification
    type.
-   Unique invoice per order.
-   Unique outbox event ID.

Create notifications/invoice records and mark the outbox event completed
in the same database transaction. This prevents duplicate records when
processing is retried.

### 8.6 Delivery guarantee

The outbox gives atomic business-change-plus-event-recording. The worker
provides at-least-once processing attempts. Idempotent database side
effects make the resulting notifications and invoice records effectively
once within this application.

Do not claim generic exactly-once message delivery.

------------------------------------------------------------------------

## 9. Notifications and Invoice

### 9.1 Notification behavior

**On `ORDER_PLACED`:** - Notify the admin that a new order awaits
payment.

**On `PAYMENT_SUCCEEDED`:** - Notify the admin that payment succeeded. -
Notify the customer that payment succeeded. - Create an invoice record
for the customer.

Notifications are in-app records stored in PostgreSQL and retrieved
through authenticated API endpoints.

Suggested notification fields:

-   `id`
-   `recipient_user_id`
-   `event_id`
-   `type`
-   `title`
-   `message`
-   `read_at`
-   `created_at`

Use a unique key to prevent duplicates.

### 9.2 Invoice behavior

For the MVP, create an invoice record and a printable invoice page in
React. Include:

-   Invoice number
-   Order number
-   Customer details
-   Product lines
-   Quantity
-   Unit price
-   Line total
-   Grand total
-   Currency
-   Payment status and date

A PDF generator is optional future work. A printable HTML invoice is
sufficient to demonstrate the workflow without expanding scope.

------------------------------------------------------------------------

## 10. API Design

Use a versioned REST API under `/api/v1`.

### 10.1 Authentication

  Method   Endpoint               Description
  -------- ---------------------- -----------------------
  `POST`   `/api/v1/auth/login`   Login and receive JWT
  `GET`    `/api/v1/auth/me`      Current user

Seeded admin and customer accounts will be created for local
development. Passwords must be hashed; never store plaintext passwords.

### 10.2 Products and inventory

  ----------------------------------------------------------------------------------------------
  Method            Endpoint                                 Access            Description
  ----------------- ---------------------------------------- ----------------- -----------------
  `GET`             `/api/v1/products`                       Public/customer   List active
                                                                               products

  `GET`             `/api/v1/products/:id`                   Public/customer   Product details

  `POST`            `/api/v1/admin/products`                 Admin             Create product

  `PATCH`           `/api/v1/admin/products/:id`             Admin             Update product

  `PUT`             `/api/v1/admin/products/:id/inventory`   Admin             Set/adjust stock
                                                                               under defined
                                                                               rules

  `GET`             `/api/v1/admin/inventory`                Admin             Inventory
                                                                               overview
  ----------------------------------------------------------------------------------------------

### 10.3 Orders

  -----------------------------------------------------------------------------------
  Method            Endpoint                      Access            Description
  ----------------- ----------------------------- ----------------- -----------------
  `POST`            `/api/v1/orders`              Customer          Create order and
                                                                    reserve stock

  `GET`             `/api/v1/orders`              Customer/admin    List own orders
                                                                    or admin orders

  `GET`             `/api/v1/orders/:id`          Owner/admin       Order details

  `POST`            `/api/v1/orders/:id/cancel`   Customer/admin    Cancel eligible
                                                                    order and release
                                                                    reservation
  -----------------------------------------------------------------------------------

The backend must enforce ownership and role checks, not rely on frontend
visibility.

### 10.4 Payments and webhook

  --------------------------------------------------------------------------------
  Method            Endpoint                   Access            Description
  ----------------- -------------------------- ----------------- -----------------
  `POST`            `/api/v1/orders/:id/pay`   Customer          Trigger dummy
                                                                 payment
                                                                 simulation

  `POST`            `/webhooks/payment`        Simulated         Receive signed
                                               provider          payment event
  --------------------------------------------------------------------------------

The webhook uses signature verification rather than customer JWT
authentication.

### 10.5 Notifications and invoices

  ----------------------------------------------------------------------------------------
  Method            Endpoint                           Access            Description
  ----------------- ---------------------------------- ----------------- -----------------
  `GET`             `/api/v1/notifications`            Authenticated     List recipient's
                                                                         notifications

  `PATCH`           `/api/v1/notifications/:id/read`   Owner             Mark notification
                                                                         as read

  `GET`             `/api/v1/orders/:id/invoice`       Order owner/admin View printable
                                                                         invoice
  ----------------------------------------------------------------------------------------

Use consistent JSON responses and meaningful HTTP status codes. Validate
all inputs and avoid exposing internal errors or secrets.

------------------------------------------------------------------------

## 11. Frontend Design

Use React with Vite.

### Customer pages

-   Login
-   Product listing
-   Product detail
-   Checkout
-   Order history
-   Order detail/status
-   Notifications
-   Printable invoice

### Admin pages

-   Admin dashboard
-   Product management
-   Inventory management
-   Order management
-   Notifications

Keep the UI intentionally simple. The main objective is to exercise the
backend and infrastructure.

The frontend should display reservation/payment states clearly and
should not assume a payment succeeded until the backend confirms it.

------------------------------------------------------------------------

## 12. Go Backend Structure

Start with a straightforward, maintainable Go application. Avoid
overengineering or creating a package for every small type.

Proposed structure (to be adjusted as implementation develops):

``` text
backend/
├── cmd/
│   └── api/
│       └── main.go
├── internal/
│   ├── config/
│   ├── database/
│   ├── redis/
│   ├── auth/
│   ├── product/
│   ├── inventory/
│   ├── order/
│   ├── payment/
│   ├── outbox/
│   ├── notification/
│   └── invoice/
├── migrations/
├── tests/
├── go.mod
└── Dockerfile
```

Responsibilities:

-   `cmd/api`: application entry point and dependency wiring.
-   `config`: environment configuration and validation.
-   `database`: PostgreSQL connection and transaction support.
-   `redis`: Redis client and lock helper.
-   Domain packages: business rules and persistence operations for their
    area.
-   `outbox`: event claiming, processing, retry, and recovery.
-   `migrations`: versioned SQL schema changes.

The Go application starts the HTTP server and background workers with
controlled shutdown. Use contexts, cancellation, and graceful server
shutdown.

------------------------------------------------------------------------

## 13. Docker and Nginx

### 13.1 Dockerfiles

Create separate Dockerfiles for:

-   React frontend (build static assets and serve them through a
    lightweight web server, or use the frontend container as the static
    asset server).
-   Go backend (multi-stage build; small runtime image).

PostgreSQL and Redis should use official images with pinned major
versions or deliberate version tags. Nginx uses an explicit
configuration file mounted/copied into its container.

### 13.2 Docker Compose

Compose responsibilities:

-   Build frontend and backend images.
-   Start PostgreSQL and Redis.
-   Create a private application network.
-   Persist PostgreSQL data with a named volume.
-   Configure health checks.
-   Start backend after required dependencies are healthy.
-   Start Nginx as the public entry point.
-   Load configuration from environment variables.

`depends_on` ordering alone does not prove a service is ready; use
health checks and application-level retry behavior.

### 13.3 Nginx

Nginx is the only public entry point. It serves frontend assets and
proxies API/webhook requests to Go.

For the local MVP, HTTP is acceptable. HTTPS/TLS can be a later
deployment exercise.

------------------------------------------------------------------------

## 14. Configuration and Secrets

Use environment variables for:

-   PostgreSQL connection settings.
-   Redis connection settings.
-   JWT signing secret.
-   Payment webhook HMAC secret.
-   Reservation duration.
-   Outbox polling interval and retry limits.
-   Server address and allowed origins.

Rules:

-   Commit `.env.example` with placeholders only.
-   Add `.env` and private keys to `.gitignore`.
-   Never commit real secrets.
-   Do not bake secrets into Docker images.
-   Use GitHub Actions secrets only if a later workflow needs
    credentials.
-   Use separate development and production configuration.

------------------------------------------------------------------------

## 15. Git and CI Pipeline

### 15.1 Git

Use Git from the first project setup.

Suggested commit progression:

``` text
chore: initialize repository structure
docs: add system architecture
feat: add database schema and migrations
feat: implement product and inventory APIs
feat: add Redis stock reservation
feat: implement order creation
feat: add transactional outbox worker
feat: add simulated payment webhook
feat: add notifications and invoice
feat: build React interface
chore: containerize application
feat: configure nginx reverse proxy
ci: add GitHub Actions workflow
test: add concurrency and integration tests
```

Small, focused commits make it easier to understand and demonstrate the
work.

### 15.2 Initial GitHub Actions CI

Trigger on pushes and pull requests.

Initial jobs:

1.  Check out repository.
2.  Set up the supported Go version.
3.  Run formatting checks (`gofmt`).
4.  Run Go tests.
5.  Build the Go backend.
6.  Set up Node.
7.  Install frontend dependencies using the lockfile.
8.  Run frontend build and available checks.
9.  Build Docker images.

A later extension can run integration tests against PostgreSQL and Redis
service containers, publish images to GHCR, and deploy to a Linux VPS.

CI validates code. CD/deployment is a separate later milestone.

------------------------------------------------------------------------

## 16. Reliability, Security, and Failure Handling

### 16.1 Concurrency

-   Redis lock acquisition must be atomic.
-   Lock release must compare ownership token.
-   Multiple product locks are acquired in deterministic order.
-   PostgreSQL conditional updates and transactions enforce stock
    invariants.
-   Test simultaneous checkouts against a one-unit product.

### 16.2 Payment

-   Verify webhook HMAC signature.
-   Deduplicate provider event IDs.
-   Validate order, amount, and currency.
-   Do not trust the browser's claim that payment succeeded.
-   Define behavior for late payment after reservation expiry.

### 16.3 Outbox

-   Claim events safely.
-   Use leases to recover abandoned processing.
-   Retry transient failures with backoff.
-   Make side effects idempotent.
-   Keep failed events inspectable.

### 16.4 Authentication and authorization

-   Hash passwords with a suitable password-hashing algorithm.
-   Sign JWTs with a secret stored outside source control.
-   Enforce admin/customer authorization in backend middleware/handlers.
-   Ensure customers can only read or mutate their own orders and
    notifications.
-   Validate request payloads and return safe error messages.

### 16.5 Operational behavior

-   Add health/readiness endpoints.
-   Log errors with request/order/event identifiers where practical.
-   Shut down HTTP server and workers gracefully.
-   Use database migrations rather than relying on ad hoc schema
    creation.

------------------------------------------------------------------------

## 17. Testing Strategy and Acceptance Criteria

The project is complete when the following scenarios work.

### Functional tests

-   Admin can create a product and set inventory.
-   Customer can browse products and create an order.
-   Customer can view only their own orders.
-   Admin can view all orders.
-   Successful payment changes the order to `PAID`.
-   Customer and admin can view their notifications.
-   Customer can view a printable invoice after payment.

### Concurrency tests

-   Given one unit in stock, two simultaneous checkout requests cannot
    both reserve it.
-   Exactly one request succeeds; the other receives an out-of-stock or
    conflict response.
-   Inventory never becomes negative.
-   A failed checkout transaction leaves no partial order or
    reservation.

### Payment tests

-   A valid signed webhook succeeds.
-   An invalid signature is rejected.
-   Replaying the same provider event does not apply payment twice.
-   Incorrect amount/currency is rejected.
-   A late payment after expiration does not fulfill the expired order.

### Outbox tests

-   Order creation and outbox event insertion commit together.
-   A rolled-back order has no committed outbox event.
-   A worker processes pending events.
-   A failed event is retried.
-   Reprocessing an event does not create duplicate notifications or
    invoices.
-   An abandoned processing lease can be recovered.

### Infrastructure tests

-   All five containers start successfully.
-   PostgreSQL data persists across container recreation.
-   Nginx serves the frontend.
-   Nginx forwards API and webhook requests to Go.
-   Backend reaches PostgreSQL and Redis through Compose DNS/service
    names.
-   CI passes on a clean push or pull request.

------------------------------------------------------------------------

## 18. Implementation Plan

Recommended order for the learning MVP:

  -----------------------------------------------------------------------
  Phase                   Work                    Main outcome
  ----------------------- ----------------------- -----------------------
  1                       Repository and          Clean Git baseline
                          documentation           

  2                       Go setup and PostgreSQL Running API and schema
                          migrations              

  3                       Product and inventory   Product management

  4                       Redis lock and          Safe concurrent
                          transactional           checkout
                          reservation             

  5                       Order workflow          Pending orders and
                                                  reservation expiry

  6                       Outbox worker           Reliable event
                                                  processing

  7                       Dummy payment and       Verified, idempotent
                          webhook                 payment simulation

  8                       Notifications and       Visible event-driven
                          invoice                 side effects

  9                       React frontend          Customer/admin
                                                  workflows

  10                      Docker and Compose      Reproducible
                                                  multi-container app

  11                      Nginx                   Single public entry
                                                  point

  12                      GitHub Actions          Automated CI

  13                      Integration tests and   Demonstrable project
                          README                  
  -----------------------------------------------------------------------

The 8--10 hour target is an intensive MVP target, not a guarantee. If
time is limited, prioritize correct database invariants, concurrency,
outbox, and payment idempotency over UI polish.

------------------------------------------------------------------------

## 19. Future Extensions

Only after the MVP works:

-   Run the outbox worker as a separate process/container.
-   Add a real message broker such as RabbitMQ or Kafka.
-   Add real email delivery.
-   Add a real payment provider and refund handling.
-   Add PDF invoice generation.
-   Add WebSocket-based live notifications.
-   Add rate limiting and more production-grade observability.
-   Deploy to a Linux VPS with HTTPS and automated CD.
-   Add Redis lease renewal/fencing or a more advanced coordination
    strategy if the use case warrants it.

------------------------------------------------------------------------

## 20. Key Engineering Decisions to Remember

1.  **PostgreSQL owns inventory truth.** Redis is not the stock
    database.
2.  **Redis lock is short-lived.** It protects the reservation critical
    section, not the entire payment period.
3.  **The database transaction is the consistency boundary.** Order,
    stock reservation, and outbox event are committed together.
4.  **Payment is asynchronous in the model.** The order remains pending
    until a verified webhook is processed.
5.  **Webhook delivery can repeat.** Provider event IDs and state checks
    make processing idempotent.
6.  **Outbox delivery is retryable, not magically exactly once.**
    Database uniqueness and transactions protect side effects.
7.  **The worker is initially a Go goroutine in the backend container.**
    A separate worker service is a future scaling option.
8.  **Docker Compose orchestrates containers; it is not a container
    itself.**
9.  **The project is a modular Go application, not a microservices
    system.** Independent containers do not automatically make an
    application microservices.
10. **The learning objective is to understand each trade-off and failure
    mode, not merely to make the happy path work.**
