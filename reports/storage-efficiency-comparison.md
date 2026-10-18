# Storage efficiency comparison

Measured 2026-10-18T08:40:40.346392+00:00 on Linux-6.18.54.1-microsoft-standard-WSL2-x86_64-with-glibc2.43 with go version go1.27.0 linux/amd64.

Baseline: `origin/main` (`0b2a5dfab341aa04e5e4b97e24a250a02a062aec`). Current: `1dbc91821e3fa91781ba9ab6466f1df3462e295e` with the working-tree fixes and benchmark.

Medians of 3 fresh databases per revision and workload. The same benchmark source runs in both revisions; execution order alternates each round. Compilation and fixture setup are excluded from workload timing. SQLite WAL and FULL synchronous durability are enabled in both revisions.

Each workload creates 100 items and executes 100 five-step application workflows with default search automation disabled. The definition starts with either an empty or 8 KiB variable, which the first step clears so later outputs do not repeat that variable. Run details are read once per run. Storage is sampled after a completed passive checkpoint, without VACUUM. The journal measurement is its retained physical high water.

Then 180 real maintenance ticks execute. Only their completion timestamps are moved three hours into the past before invoking the ordinary retention pass. All 100 application runs must survive that pass. This checks retention eligibility; it is not a multi-hour idle soak.

| Workload | Metric | origin/main | Current | Change |
| --- | --- | ---: | ---: | ---: |
| definition-0 | Main database | 2,884.00 KiB | 2,776.00 KiB | -3.7% |
| definition-0 | Main database growth during workload | 1,396.00 KiB | 1,284.00 KiB | -8.0% |
| definition-0 | Retained WAL | 4,156.24 KiB | 1,024.00 KiB | -75.4% |
| definition-0 | Step tables + primary-key indexes | 412.00 KiB | 312.00 KiB | -24.3% |
| definition-0 | Transactional output payloads | 85.92 KiB | 58.96 KiB | -31.4% |
| definition-0 | DBOS step output payloads | 115.20 KiB | 19.53 KiB | -83.0% |
| definition-0 | Create + complete workflow per item | 16.20 ms | 15.78 ms | -2.6% |
| definition-0 | Run details per run | 1.26 ms | 1.32 ms | +5.0% |
| definition-0 | Retention pass | 0.89 ms | 7.05 ms | +690.0% |
| definition-0 | Maintenance executions after retention | 180.00 rows | 0.00 rows | -100.0% |
| definition-8192 | Main database | 4,856.00 KiB | 2,780.00 KiB | -42.8% |
| definition-8192 | Main database growth during workload | 3,360.00 KiB | 1,280.00 KiB | -61.9% |
| definition-8192 | Retained WAL | 4,252.80 KiB | 1,024.00 KiB | -75.9% |
| definition-8192 | Step tables + primary-key indexes | 2,380.00 KiB | 308.00 KiB | -87.1% |
| definition-8192 | Transactional output payloads | 885.91 KiB | 58.96 KiB | -93.3% |
| definition-8192 | DBOS step output payloads | 1,181.61 KiB | 19.53 KiB | -98.3% |
| definition-8192 | Create + complete workflow per item | 15.47 ms | 16.08 ms | +4.0% |
| definition-8192 | Run details per run | 1.33 ms | 1.28 ms | -4.2% |
| definition-8192 | Retention pass | 0.90 ms | 7.82 ms | +773.3% |
| definition-8192 | Maintenance executions after retention | 180.00 rows | 0.00 rows | -100.0% |

Negative changes mean fewer bytes/rows or less elapsed time. Retention timing compares different amounts of work: the current revision deletes expired maintenance executions, while the baseline retains them. Timing results are local measurements, not throughput guarantees; filesystem caching, concurrent host activity and the workload shape affect them. Uploaded file bytes, external AI providers, OCR and native vector search are outside this workload.

Reproduce:

```bash
python3 scripts/native.py
python3 scripts/compare-storage.py --base 0b2a5dfab341aa04e5e4b97e24a250a02a062aec --repeats 3
```
