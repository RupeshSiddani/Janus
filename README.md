# janus

a distributed lock manager built on raft consensus. provides strongly consistent locks with lease-based expiry and fencing tokens to prevent stale writes.

## what it does

janus coordinates access to shared resources across distributed systems. clients create leases (time-bound sessions), acquire locks tied to those leases, and receive monotonically increasing fencing tokens that downstream systems can use to reject stale operations.

use cases:
- distributed cron jobs (only one instance executes)
- database migrations (ensure single execution)
- leader election
- critical section protection across processes

core guarantees:
- strong consistency via raft (cp in cap theorem)
- fencing tokens prevent stale writes after lease expiry
- lease-based locks auto-release on client failure

## how it works

```
client --> http api --> raft leader --> fsm (lock state)
                            |
                        replicates
                        to followers
```

1. client creates a lease with a ttl (e.g. 60 seconds)
2. client acquires a lock, gets back a fencing token
3. client uses the fencing token when writing to protected resources
4. client renews the lease periodically to keep it alive
5. if the client crashes, the lease expires and all its locks are released

fencing tokens are globally monotonic. if client a gets token 5 and its lease expires, client b gets token 6. the protected resource checks `new_token > stored_token` and rejects client a's stale writes.

## architecture

```
janus/
├── cmd/janus/             entrypoint, flag config, wiring
├── internal/
│   ├── lock/              core fsm: leases, locks, fencing tokens (tested)
│   ├── raft/              hashicorp/raft wrapper, boltdb storage
│   └── server/            http rest api
├── go.mod
└── go.sum
```

| layer | responsibility |
|-------|---------------|
| lock (fsm) | all lock/lease state, fencing token counter, snapshot/restore |
| raft | consensus, log replication, leader election, lease expiry loop |
| server | http endpoints, leader check, request validation |

## quick start

### build

```bash
go build -o janus ./cmd/janus/
```

### run (single node)

```bash
./janus --bootstrap --data-dir ./data
```

### run (3 node cluster)

```bash
# node 1 (bootstrap)
./janus --node-id node-1 --raft-addr 10.0.1.1:7000 --http-addr :8080 --data-dir ./data1 --bootstrap

# node 2
./janus --node-id node-2 --raft-addr 10.0.1.2:7000 --http-addr :8080 --data-dir ./data2

# node 3
./janus --node-id node-3 --raft-addr 10.0.1.3:7000 --http-addr :8080 --data-dir ./data3
```

### configuration

| flag | default | description |
|------|---------|-------------|
| `--node-id` | `node-1` | unique node identifier |
| `--raft-addr` | `127.0.0.1:7000` | raft consensus bind address |
| `--http-addr` | `:8080` | http server listen address |
| `--data-dir` | `./data` | data directory for raft logs and snapshots |
| `--bootstrap` | `false` | bootstrap a new cluster (first node only) |

## api

### create lease

```bash
curl -X POST http://localhost:8080/v1/lease \
  -d '{"owner_id":"client-1","ttl_seconds":60}'
# {"lease_id":1}
```

### renew lease

```bash
curl -X POST http://localhost:8080/v1/lease/renew \
  -d '{"lease_id":1}'
# {"ttl_seconds":60}
```

### acquire lock

```bash
curl -X POST http://localhost:8080/v1/lock/acquire \
  -d '{"lock_name":"my-job","owner_id":"client-1","lease_id":1}'
# {"fencing_token":1}
```

### release lock

```bash
curl -X POST http://localhost:8080/v1/lock/release \
  -d '{"lock_name":"my-job","lease_id":1}'
# {"released":true}
```

### cluster status

```bash
curl http://localhost:8080/v1/status
# {"node_id":"node-1","is_leader":true,"cluster_size":1,...}
```

## using fencing tokens

the fencing token is useless unless the protected resource validates it. pattern:

```
1. acquire lock, get token=5
2. write to resource with token=5
3. resource stores last_seen_token=5
4. if a stale client tries to write with token=3, resource rejects (3 < 5)
```

example with a database:

```sql
update jobs
set last_run = now(), last_token = $new_token
where name = $job_name and last_token < $new_token;
```

if zero rows affected, the token was stale.

## testing

tests cover only the lock manager (fsm). 45 test cases across 8 groups:

```bash
# run tests
go test ./internal/lock/ -v -count=1

# with race detector
go test ./internal/lock/ -v -race -count=1

# coverage (currently 89.4%)
go test ./internal/lock/ -coverprofile=coverage.out
go tool cover -func=coverage.out
```

| group | count | what it covers |
|-------|-------|---------------|
| create lease | 5 | happy path, id monotonicity, invalid ttl, multi-owner |
| renew lease | 5 | success, not found, expired, repeated, field preservation |
| acquire lock | 10 | success, fencing increment, re-entrancy, contention, expired lease, owner mismatch, multi-lock, post-release, post-expiry |
| release lock | 5 | success, not owner, not found, double release, isolation |
| expire lease | 5 | no locks, with locks, not found, cross-lease isolation, reclaimability |
| fencing token | 4 | strict monotonicity, survive release, global counter, re-entrant no-increment |
| snapshot/restore | 4 | empty round-trip, stateful round-trip, operational after restore, independence |
| integration | 6 | full lifecycle, cascading expiry, concurrent clients, stale lease, fencing order, rapid cycle |

## built with

- [hashicorp/raft](https://github.com/hashicorp/raft) for consensus
- [raft-boltdb](https://github.com/hashicorp/raft-boltdb) for persistent log and stable storage
- go standard library for http server and json serialization

## future plan

### v1.1 - observability
- prometheus metrics (lock acquire/release rates, latency histograms, active locks/leases, raft health)
- structured logging with log levels
- /metrics endpoint

### v1.2 - grpc api
- protobuf service definition
- grpc server alongside http
- grpc-gateway for automatic http/grpc translation
- bidirectional streaming for lease heartbeats (replaces polling)

### v1.3 - client sdk
- go client library with automatic lease renewal
- connection pooling and leader discovery
- retry with exponential backoff and jitter

### v2.0 - advanced lock features
- wait queues (block until lock available instead of immediate reject)
- priority queues for waiters
- lock metadata (key-value pairs attached to locks)
- read/write locks (shared readers, exclusive writers)
- lock groups (acquire multiple locks atomically)

### operational improvements
- dynamic cluster membership via admin rpc
- graceful leader transfer
- snapshot tuning and compaction policies
- docker compose for multi-node local development
- chaos testing (network partitions, node crashes)

## references

- [how to do distributed locking](https://martin.kleppmann.com/2016/02/08/how-to-do-distributed-locking.html) by martin kleppmann
- [the chubby lock service](https://research.google/pubs/pub27897/) by google research
- [in search of an understandable consensus algorithm](https://raft.github.io/raft.pdf) (raft paper)

## license

mit
