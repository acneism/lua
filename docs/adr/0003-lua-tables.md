# 3. Lua table port

Status: proposed for A1 review.

## Decision

Port the table algorithms from Redis 7.2.16's Lua 5.1.5 ltable.c. Preserve array
and hash sizing, main positions, collision chains, backward free-slot search,
rehash insertion order, next traversal, and length boundary search.

Use Lua's length-seeded sampled string hash, summed double-word number hash,
and boolean hash. Object keys use stable state IDs. Retain deleted hash keys
for traversal. Constructors use Lua's floating-byte capacities and SETLIST
batches of fifty.

## Evidence

Captured snippets check mixed keys, collisions, deletions during traversal,
descending integer insertions, sparse arrays, signed zero, and constructor
boundaries at 49, 50, 51, 100, and 251 fields. A seeded model checks values,
complete terminating traversal, and valid length boundaries against a Go map.

On the Azure machine, one million sequential insertions took a median
287.782 ms versus 122.725 ms in Redis. Filling and traversing 100,000 string
keys took 73.325 ms versus 60.411 ms. See [benchmarks](../benchmarks.md).

## Consequences

The implementation carries more structure than a Go map. It reproduces Redis
ordering for non-pointer keys and provides average constant-time lookup,
insertion, and traversal advancement. Pointer-key ordering remains a permitted
compatibility difference.
