# BatchProcessor

## What it did

BatchProcessor (732 LOC in `batch.go`, ~2200 LOC in `batch_test.go`) grouped NFS operations by type (read, write, getattr, setattr, dirread) for batch execution. Operations were submitted to a queue and processed when the batch reached `MaxBatchSize` or a timer fired.

## Why it was built

The idea was that grouping similar operations would reduce context switching and improve throughput under concurrent workloads. It supported five batch types: read, write, getattr, setattr, and directory read.

## Current state at removal

- Disabled by default (`BatchOperations` flag, default `false`)
- Never actually fused I/O operations -- each request within a batch was still processed individually via the same file open/read/close or open/write/close path
- The batching added per-request overhead: mutex acquisition, queue insertion, channel wait, timer management
- The `AddRequest` / `processBatch` / per-type handler pipeline was a significant source of complexity in the codebase

## What would need to be true to reconsider

- Actual I/O fusion: merging adjacent reads into a single larger read, coalescing overlapping writes into a single write
- Benchmarks on real workloads showing measurable throughput improvement over direct execution
- Evidence that the batching overhead (queue management, synchronization) is smaller than the fusion savings
