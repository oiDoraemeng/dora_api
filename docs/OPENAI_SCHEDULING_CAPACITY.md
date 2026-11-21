# OpenAI scheduling under concurrent load

The advanced OpenAI scheduler uses the existing success-rate, first-token
latency, load and queue scores. Enable it with the administrator setting
`openai_advanced_scheduler_enabled`. This setting applies to all OpenAI groups;
it does not enable weighted sticky routing or subscription priority.

The configured Top-K is a preferred set, not the entire available capacity.
If those accounts cannot acquire a slot, the scheduler tries the remaining
eligible accounts in score order before returning a wait plan. Slot acquisition
and database rechecks share the existing 64-probe budget. Known-full accounts
do not consume acquisition probes. Compact capability tiers and account/model
eligibility still apply to fallback candidates.

Session and previous-response ownership are resolved before load balancing.
Once a request enters load balancing, its weighted selection includes a local
sequence value so simultaneous spillovers from one session do not replay the
same account order. Existing pinned response ownership remains intact.

`load_factor` is the balancing denominator, whereas `concurrency` is the hard
slot limit. Set the load factor to the upstream's sustainable capacity, or leave
it unset to follow the hard limit. Increasing only the hard limit does not
increase an explicitly configured load factor. The legacy scheduler also tries
remaining hard capacity after all soft load targets are reached, before waiting
on a single account.

For Responses, Chat Completions, Messages and WebSocket inference on OpenAI
API-key pool accounts, exhausting one request's same-account retry
budget records one account/model failure. The first exhausted request does not
block the model; the second adds a 10-second cooldown, and subsequent exhausted
requests add a 45-second cooldown. A successful request clears this model's
failure streak. Other models remain available. Provider/request-scoped errors,
credential failures and explicit custom error-code policies retain their
existing handling. This uses bounded in-memory model state and adds no database
or Redis write to a successful request.

Relevant regression checks:

```sh
cd backend
go test -tags=unit ./internal/service -run 'Test(OpenAIAdvancedScheduler|OpenAILegacyScheduler|OpenAIPoolExhaust|Advanced.*Scheduler|LegacyScheduler)' -count=1
go test -tags=unit ./internal/handler -run 'Test.*(Failover|Retry|Cancel|GrokMedia|Credential)' -count=1
go test -tags=unit ./internal/service -run '^$' -bench '^BenchmarkOpenAISchedulerCapacityPlan24$'
```

Account capacity is shared across groups containing that account. Observe
throughput, final success rate, first-token tail latency and waiting counts;
the current active-request count alone is not a capacity benchmark.
